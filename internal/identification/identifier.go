package identification

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/disc"
	"spindle/internal/identification/keydb"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripping"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/stage"
	"spindle/internal/staging"
)

// Identifier performs disc identification using MakeMKV scanning and TMDB metadata.
type Identifier struct {
	store    *queue.Store
	cfg      *config.Config
	logger   *slog.Logger
	tmdb     *tmdbSearch
	tmdbInfo tmdb.Searcher
	keydb    *keydb.Catalog
	scanner  DiscScanner
	notifier notifications.Service
}

// DiscScanner defines disc scanning operations.
type DiscScanner interface {
	Scan(ctx context.Context, device string) (*disc.ScanResult, error)
}

// NewIdentifier creates a new stage handler.
func NewIdentifier(cfg *config.Config, store *queue.Store, logger *slog.Logger) *Identifier {
	client, err := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language)
	if err != nil {
		logger.Warn("tmdb client initialization failed; tmdb lookups disabled",
			logging.Error(err),
			logging.String(logging.FieldEventType, "tmdb_client_unavailable"),
			logging.String(logging.FieldErrorHint, "check tmdb_api_key and tmdb_base_url in config"),
			logging.String(logging.FieldImpact, "disc identification will route to review"),
		)
	}
	scanner := disc.NewScanner(cfg.MakemkvBinary())
	return NewIdentifierWithDependencies(cfg, store, logger, client, scanner, notifications.NewService(cfg))
}

// NewIdentifierWithDependencies allows injecting TMDB searcher and disc scanner (used in tests).
func NewIdentifierWithDependencies(cfg *config.Config, store *queue.Store, logger *slog.Logger, searcher tmdb.Searcher, scanner DiscScanner, notifier notifications.Service) *Identifier {
	var catalog *keydb.Catalog
	if cfg != nil {
		timeout := time.Duration(cfg.MakeMKV.KeyDBDownloadTimeout) * time.Second
		catalog = keydb.NewCatalog(cfg.MakeMKV.KeyDBPath, logger, cfg.MakeMKV.KeyDBDownloadURL, timeout)
	}
	id := &Identifier{
		store:    store,
		cfg:      cfg,
		tmdb:     newTMDBSearch(searcher),
		tmdbInfo: searcher,
		keydb:    catalog,
		scanner:  scanner,
		notifier: notifier,
	}
	id.SetLogger(logger)
	return id
}

// SetLogger updates the identifier's logging destination while preserving component labeling.
func (i *Identifier) SetLogger(logger *slog.Logger) {
	i.logger = logging.NewComponentLogger(logger, "identifier")
}

// Prepare initializes progress messaging prior to Execute.
func (i *Identifier) Prepare(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, i.logger)

	// Clean up stale staging directories (older than 48 hours) at job start
	i.cleanStaleStagingDirectories(ctx, logger)

	item.InitProgress("Identifying", "Fetching metadata")
	logger.Info("identification stage started",
		logging.String(logging.FieldEventType, "stage_start"),
		logging.String("processing_status", string(queue.StatusIdentifying)),
		logging.String("disc_title", strings.TrimSpace(item.DiscTitle)),
		logging.String("source_file", strings.TrimSpace(item.SourcePath)),
	)

	if i.notifier != nil && strings.TrimSpace(item.SourcePath) == "" {
		title := strings.TrimSpace(item.DiscTitle)
		if title == "" {
			title = "Unknown Disc"
		}
		if err := i.notifier.Publish(ctx, notifications.EventDiscDetected, notifications.Payload{
			"discTitle": title,
			"discType":  "unknown",
		}); err != nil {
			logger.Debug("disc detected notification failed",
				logging.String("error_message", "Unable to send disc detected notification"),
				logging.Error(err))
		}
	}
	return nil
}

// updateProgress persists stage progress to the queue database.
func (i *Identifier) updateProgress(ctx context.Context, item *queue.Item, message string, percent float64) error {
	item.ProgressStage = "Identifying"
	item.ProgressMessage = message
	item.ProgressPercent = percent
	if i.store == nil {
		return nil
	}
	return i.store.UpdateProgress(ctx, item)
}

// Execute performs disc scanning and TMDB identification.
func (i *Identifier) Execute(ctx context.Context, item *queue.Item) error {
	stageStart := time.Now()
	logger := logging.WithContext(ctx, i.logger)

	// Phase 1: Scan disc and capture fingerprint
	if err := i.updateProgress(ctx, item, "Phase 1/3 - Scanning disc", 10); err != nil {
		logger.Debug("failed to update scanning progress", logging.Error(err))
	}
	scanResult, titleCount, err := i.scanDiscAndCaptureFingerprint(ctx, item, logger)
	if err != nil {
		return err
	}
	if item.Status == queue.StatusFailed {
		return nil // Duplicate fingerprint triggered failure
	}

	discID := ""
	if scanResult != nil && scanResult.BDInfo != nil {
		discID = strings.TrimSpace(scanResult.BDInfo.DiscID)
	}

	title := strings.TrimSpace(item.DiscTitle)
	titleFromKeyDB := false

	if scanResult == nil || scanResult.BDInfo == nil {
		if i.keydb != nil {
			logger.Debug("keydb lookup skipped", logging.String("reason", "bdinfo unavailable"))
		}
	} else if i.keydb == nil {
		logger.Debug("keydb lookup skipped", logging.String("reason", "keydb catalog unavailable"))
	} else if discID == "" {
		logger.Debug("keydb lookup skipped", logging.String("reason", "disc id missing in bdinfo"))
	} else {
		// Phase 2: KeyDB lookup
		if err := i.updateProgress(ctx, item, "Phase 2/3 - Looking up KeyDB", 40); err != nil {
			logger.Debug("failed to update keydb progress", logging.Error(err))
		}
		keydbLookupStart := time.Now()
		entry, found, err := i.keydb.Lookup(discID)
		keydbLookupDuration := time.Since(keydbLookupStart)
		if err != nil {
			logger.Warn("keydb lookup failed",
				logging.String("disc_id", discID),
				logging.Error(err),
				logging.Duration("lookup_duration", keydbLookupDuration),
				logging.String("error_message", "Failed to query keydb catalog"),
				logging.String(logging.FieldErrorHint, "Verify keydb path and refresh connectivity"),
				logging.String(logging.FieldImpact, "title lookup skipped; using disc title from scan"),
				logging.String(logging.FieldEventType, "keydb_lookup_failed"))
		} else if found {
			keydbTitle := strings.TrimSpace(entry.Title)
			if keydbTitle != "" {
				logger.Info("title updated from keydb",
					logging.String(logging.FieldDecisionType, "title_source"),
					logging.String("decision_result", "updated"),
					logging.String("decision_reason", "keydb contains authoritative title for disc_id"),
					logging.String("decision_options", "keep, update"),
					logging.String("decision_selected", keydbTitle),
					logging.String("disc_id", discID),
					logging.String("original_title", title),
					logging.String("new_title", keydbTitle),
					logging.Duration("lookup_duration", keydbLookupDuration))
				title = keydbTitle
				item.DiscTitle = title
				titleFromKeyDB = true
			}
		} else {
			logger.Debug("keydb lookup produced no match",
				logging.String("disc_id", discID),
				logging.Duration("lookup_duration", keydbLookupDuration))
		}
	}

	if !titleFromKeyDB {
		// Determine best title using priority-based approach
		logger.Debug("determining best title",
			logging.String("current_title", title),
			logging.Int("makemkv_titles", len(scanResult.Titles)))

		if len(scanResult.Titles) > 0 {
			logger.Debug("makemkv title available",
				logging.String("makemkv_title", scanResult.Titles[0].Name))
		}

		if scanResult.BDInfo != nil {
			logger.Debug("bdinfo available",
				logging.String("bdinfo_name", scanResult.BDInfo.DiscName))
		}

		bestTitle := determineBestTitle(title, scanResult)
		if bestTitle != title {
			logger.Info("title updated based on priority sources",
				logging.String(logging.FieldDecisionType, "title_source"),
				logging.String("decision_result", "updated"),
				logging.String("decision_reason", "priority_source"),
				logging.String("decision_options", "keep, update"),
				logging.String("decision_selected", bestTitle),
				logging.String("original_title", title),
				logging.String("new_title", bestTitle),
				logging.String("decision_source", detectTitleSource(bestTitle, scanResult)))
			title = bestTitle
			item.DiscTitle = title
		}
	}

	if title == "" {
		title = "Unknown Disc"
		item.DiscTitle = title
	}

	// Prepare enhanced search options using bd_info data
	searchOpts := tmdb.SearchOptions{}

	if scanResult.BDInfo != nil {
		if scanResult.BDInfo.Year > 0 {
			searchOpts.Year = scanResult.BDInfo.Year
			logger.Debug("using bd_info year for TMDB search",
				logging.Int("year", scanResult.BDInfo.Year))
		}
		if scanResult.BDInfo.Studio != "" {
			logger.Debug("detected studio from bd_info",
				logging.String("studio", scanResult.BDInfo.Studio))
			// Note: Studio filtering would require company lookup API call
		}
		// Calculate runtime from main title duration
		if len(scanResult.Titles) > 0 && scanResult.Titles[0].Duration > 0 {
			searchOpts.Runtime = scanResult.Titles[0].Duration / 60 // Convert seconds to minutes
			logger.Debug("using title runtime for TMDB search",
				logging.Int("runtime_minutes", searchOpts.Runtime))
		}
	}

	discLabel := ""
	if scanResult != nil && scanResult.BDInfo != nil {
		discLabel = scanResult.BDInfo.VolumeIdentifier
	}

	discNumber := 0
	discSources := []string{title, discLabel}
	if scanResult != nil && scanResult.BDInfo != nil {
		if scanResult.BDInfo.DiscName != "" {
			discSources = append(discSources, scanResult.BDInfo.DiscName)
		}
		if scanResult.BDInfo.VolumeIdentifier != "" {
			discSources = append(discSources, scanResult.BDInfo.VolumeIdentifier)
		}
	}
	if n, ok := extractDiscNumber(discSources...); ok {
		discNumber = n
		logger.Debug("disc number detected", logging.Int("disc_number", discNumber))
	}

	attributes := make(map[string]any)
	if discNumber > 0 {
		attributes["disc_number"] = discNumber
	}
	if scanResult.HasForcedEnglishSubtitles() {
		attributes["has_forced_subtitle_track"] = true
		logger.Debug("forced subtitle track detected on disc",
			logging.Bool("has_forced_subtitle_track", true))
	}

	mediaHint := detectMediaKind(title, discLabel, scanResult)

	// Phase 3: TMDB search
	if err := i.updateProgress(ctx, item, "Phase 3/3 - Searching TMDB", 60); err != nil {
		logger.Debug("failed to update tmdb progress", logging.Error(err))
	}
	outcome, err := i.identifyWithTMDB(ctx, logger, item, identifyContext{
		Title:      title,
		DiscLabel:  discLabel,
		DiscNumber: discNumber,
		SearchOpts: searchOpts,
		MediaHint:  mediaHint,
		ScanResult: scanResult,
	})
	if err != nil {
		return err
	}

	mediaType := outcome.MediaType
	contentKey := outcome.ContentKey
	metadata := outcome.Metadata
	identified := outcome.Identified
	identifiedTitle := outcome.IdentifiedTitle
	tmdbID := outcome.TMDBID
	seasonNumber := outcome.SeasonNumber
	episodeMatches := outcome.EpisodeMatches
	matchedEpisodes := outcome.MatchedEpisodes

	if contentKey == "" {
		contentKey = unknownContentKey(item.DiscFingerprint)
	}
	if mediaType == "unknown" && mediaHint == mediaKindTV {
		mediaType = "tv"
	}
	if seasonNumber > 0 {
		metadata["season_number"] = seasonNumber
	}
	if len(matchedEpisodes) > 0 {
		metadata["episode_numbers"] = matchedEpisodes
	}
	metadata["media_type"] = mediaType
	if strings.TrimSpace(item.MetadataJSON) == "" {
		if encoded, err := json.Marshal(metadata); err == nil {
			item.MetadataJSON = string(encoded)
		} else {
			logger.Warn("failed to encode fallback metadata",
				logging.Error(err),
				logging.String("error_message", "Fallback metadata could not be serialized"),
				logging.String(logging.FieldErrorHint, "Retry identification or report JSON encoding issue"),
				logging.String(logging.FieldImpact, "metadata will be incomplete for downstream stages"),
				logging.String(logging.FieldEventType, "metadata_encode_failed"))
		}
	}

	// Use queue item fingerprint - it's mandatory at enqueue time
	ripFingerprint := strings.TrimSpace(item.DiscFingerprint)

	titleSpecs, episodeSpecs := buildRipSpecs(logger, scanResult, episodeMatches, identifiedTitle, title, metadata)

	spec := ripspec.Envelope{
		Fingerprint: ripFingerprint,
		ContentKey:  contentKey,
		Metadata:    metadata,
		Attributes:  attributes,
		Titles:      titleSpecs,
		Episodes:    episodeSpecs,
	}

	encodedSpec, err := spec.Encode()
	if err != nil {
		return services.Wrap(services.ErrTransient, "identification", "encode rip spec", "Failed to serialize rip specification", err)
	}
	item.RipSpecData = encodedSpec

	if !identified {
		logger.Info(
			"prepared unidentified rip specification",
			logging.String(logging.FieldDecisionType, "identification_outcome"),
			logging.String("decision_result", "unidentified"),
			logging.Int("title_count", len(titleSpecs)),
			logging.String("content_key", contentKey),
		)
	} else if selection, ok, candidates, rejects := rippingPrimaryTitleSummary(titleSpecs); ok {
		attrs := []logging.Attr{
			logging.String(logging.FieldDecisionType, "primary_title"),
			logging.String("decision_result", "selected"),
			logging.String("decision_selected", fmt.Sprintf("%d:%ds", selection.ID, selection.Duration)),
			logging.Int("candidate_count", len(candidates)),
			logging.Int("rejected_count", len(rejects)),
			logging.Int("title_id", selection.ID),
			logging.Int("duration_seconds", selection.Duration),
			logging.Int("chapters", selection.Chapters),
			logging.String("playlist", strings.TrimSpace(selection.Playlist)),
			logging.Int("segment_count", selection.SegmentCount),
		}
		for idx, candidate := range candidates {
			key := fmt.Sprintf("candidate_%d", idx+1)
			if id, ok := logging.ParseDecisionID(candidate); ok {
				key = fmt.Sprintf("candidate_%d", id)
			}
			attrs = append(attrs, logging.String(key, candidate))
		}
		for idx, reject := range rejects {
			key := fmt.Sprintf("rejected_%d", idx+1)
			if id, ok := logging.ParseDecisionID(reject); ok {
				key = fmt.Sprintf("rejected_%d", id)
			}
			attrs = append(attrs, logging.String(key, reject))
		}
		logger.Info("primary title decision", logging.Args(attrs...)...)
	}

	if err := i.validateIdentification(ctx, item); err != nil {
		return err
	}

	// Log stage summary with timing and key metrics
	i.logStageSummary(ctx, item, stageStart, identified, titleCount, tmdbID, mediaType)

	return nil
}

var rippingPrimaryTitleSummary = func(titles []ripspec.Title) (ripspec.Title, bool, []string, []string) {
	return ripping.PrimaryTitleDecisionSummary(titles)
}

// cleanStaleStagingDirectories removes staging directories older than 48 hours.
// This is called at the start of each job to prevent disk space accumulation
// from prior failed runs.
func (i *Identifier) cleanStaleStagingDirectories(ctx context.Context, logger *slog.Logger) {
	if i.cfg == nil {
		return
	}
	stagingDir := strings.TrimSpace(i.cfg.Paths.StagingDir)
	if stagingDir == "" {
		return
	}

	const maxAge = 48 * time.Hour
	result := staging.CleanStale(ctx, stagingDir, maxAge, logger)

	if len(result.Removed) > 0 {
		logger.Info("staging cleanup completed",
			logging.Int("removed_count", len(result.Removed)),
			logging.String(logging.FieldEventType, "staging_cleanup_complete"),
		)
	}
}

// HealthCheck verifies identifier dependencies required for successful execution.
func (i *Identifier) HealthCheck(ctx context.Context) stage.Health {
	const name = "identifier"
	if i.cfg == nil {
		return stage.Unhealthy(name, "configuration unavailable")
	}
	if strings.TrimSpace(i.cfg.TMDB.APIKey) == "" {
		return stage.Unhealthy(name, "tmdb api key missing")
	}
	if i.tmdb == nil || i.tmdb.client == nil {
		return stage.Unhealthy(name, "tmdb client unavailable")
	}
	if i.scanner == nil {
		return stage.Unhealthy(name, "disc scanner unavailable")
	}
	return stage.Healthy(name)
}
