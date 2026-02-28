package identification

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/disc"
	"spindle/internal/discidcache"
	"spindle/internal/identification/keydb"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripping"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/services/llm"
	"spindle/internal/stage"
	"spindle/internal/staging"
	"spindle/internal/textutil"
)

// Identifier performs disc identification using MakeMKV scanning and TMDB metadata.
type Identifier struct {
	store       *queue.Store
	cfg         *config.Config
	logger      *slog.Logger
	tmdb        *tmdbSearch
	tmdbInfo    tmdb.Searcher
	keydb       *keydb.Catalog
	discIDCache *discidcache.Cache
	scanner     DiscScanner
	notifier    notifications.Service
}

// DiscScanner defines disc scanning operations.
type DiscScanner interface {
	Scan(ctx context.Context, device string) (*disc.ScanResult, error)
}

// NewIdentifier creates a new stage handler.
func NewIdentifier(cfg *config.Config, store *queue.Store, logger *slog.Logger, notifier notifications.Service) *Identifier {
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
	scanner.SetSettleDelay(time.Duration(cfg.MakeMKV.DiscSettleDelay) * time.Second)
	return NewIdentifierWithDependencies(cfg, store, logger, client, scanner, notifier)
}

// NewIdentifierWithDependencies allows injecting TMDB searcher and disc scanner (used in tests).
func NewIdentifierWithDependencies(cfg *config.Config, store *queue.Store, logger *slog.Logger, searcher tmdb.Searcher, scanner DiscScanner, notifier notifications.Service) *Identifier {
	var catalog *keydb.Catalog
	var cache *discidcache.Cache
	if cfg != nil {
		timeout := time.Duration(cfg.MakeMKV.KeyDBDownloadTimeout) * time.Second
		catalog = keydb.NewCatalog(cfg.MakeMKV.KeyDBPath, logger, cfg.MakeMKV.KeyDBDownloadURL, timeout)

		// Initialize disc ID cache if enabled
		if cfg.DiscIDCache.Enabled {
			cache = discidcache.NewCache(cfg.DiscIDCache.Path, logger)
		}
	}
	id := &Identifier{
		store:       store,
		cfg:         cfg,
		tmdb:        newTMDBSearch(searcher),
		tmdbInfo:    searcher,
		keydb:       catalog,
		discIDCache: cache,
		scanner:     scanner,
		notifier:    notifier,
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

	// Fast path: check disc ID cache (skips KeyDB + TMDB search)
	if i.discIDCache != nil && discID != "" {
		if cacheEntry, found := i.discIDCache.Lookup(discID); found {
			logger.Info("identification from disc id cache",
				logging.String(logging.FieldDecisionType, "disc_id_cache"),
				logging.String("decision_result", "cache_hit"),
				logging.String("disc_id", discID),
				logging.Int64("tmdb_id", cacheEntry.TMDBID),
				logging.String("title", cacheEntry.Title))
			return i.completeIdentificationFromCache(ctx, logger, item, scanResult, cacheEntry, stageStart, titleCount)
		}
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

	mediaHint, mediaReason := detectMediaKindWithReason(title, discLabel, scanResult)
	logger.Info("media type detection",
		logging.String(logging.FieldDecisionType, "media_type_detection"),
		logging.String("decision_result", mediaHint.String()),
		logging.String("decision_reason", mediaReason),
	)

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

	// Identified path: finalize through shared method.
	if outcome.Identified {
		r := identificationResult{
			IdentifiedTitle: outcome.IdentifiedTitle,
			MediaType:       outcome.MediaType,
			TMDBID:          outcome.TMDBID,
			Year:            outcome.Year,
			ReleaseDate:     outcome.ReleaseDate,
			FirstAirDate:    outcome.FirstAirDate,
			Overview:        outcome.Overview,
			SeasonNumber:    outcome.SeasonNumber,
			VoteAverage:     outcome.VoteAverage,
			VoteCount:       outcome.VoteCount,
			Edition:         outcome.Edition,
			EpisodeMatches:  outcome.EpisodeMatches,
			ScanResult:      scanResult,
			DiscSources:     discSources,
			FallbackTitle:   title,
		}
		if err := i.finalizeIdentifiedItem(ctx, logger, item, r); err != nil {
			return err
		}
		if discID != "" {
			i.populateDiscIDCache(logger, discidcache.Entry{
				DiscID:       discID,
				TMDBID:       outcome.TMDBID,
				MediaType:    outcome.MediaType,
				Title:        outcome.IdentifiedTitle,
				Edition:      outcome.Edition,
				SeasonNumber: outcome.SeasonNumber,
				Year:         outcome.Year,
				CachedAt:     time.Now(),
			})
		}
		i.logStageSummary(ctx, item, stageStart, true, titleCount, outcome.TMDBID, outcome.MediaType)
		return nil
	}

	// Unidentified path: build fallback metadata and rip spec.
	mediaType := outcome.MediaType
	contentKey := outcome.ContentKey
	metadata := outcome.Metadata

	if contentKey == "" {
		contentKey = unknownContentKey(item.DiscFingerprint)
	}
	if mediaType == "unknown" && mediaHint == mediaKindTV {
		mediaType = "tv"
	}
	if outcome.SeasonNumber > 0 {
		metadata["season_number"] = outcome.SeasonNumber
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

	// Build attributes for unidentified path.
	attributes := make(map[string]any)
	if discNumber > 0 {
		attributes["disc_number"] = discNumber
	}
	hasForcedTrack := scanResult.HasForcedEnglishSubtitles()
	if hasForcedTrack {
		attributes["has_forced_subtitle_track"] = true
	}
	logger.Info("forced subtitle detection",
		logging.String(logging.FieldDecisionType, "forced_subtitle_detection"),
		logging.String("decision_result", textutil.Ternary(hasForcedTrack, "detected", "none")),
		logging.String("decision_reason", textutil.Ternary(hasForcedTrack, "disc_has_forced_track", "no_forced_track_found")),
		logging.Bool("has_forced_subtitle_track", hasForcedTrack))

	ripFingerprint := strings.TrimSpace(item.DiscFingerprint)
	titleSpecs, episodeSpecs := buildRipSpecs(logger, scanResult, outcome.EpisodeMatches, outcome.IdentifiedTitle, title, discNumber, metadata)

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

	logger.Info("prepared unidentified rip specification",
		logging.String(logging.FieldDecisionType, "identification_outcome"),
		logging.String("decision_result", "unidentified"),
		logging.Int("title_count", len(titleSpecs)),
		logging.String("content_key", contentKey),
	)

	if err := i.validateIdentification(ctx, item); err != nil {
		return err
	}

	i.logStageSummary(ctx, item, stageStart, false, titleCount, 0, mediaType)
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

// detectMovieEdition determines if a disc is an alternate movie edition.
// It first tries regex patterns for known editions, then falls back to LLM
// for ambiguous cases. Returns the edition label or empty string if not an edition.
func (i *Identifier) detectMovieEdition(ctx context.Context, logger *slog.Logger, discTitle, tmdbTitle, titleWithYear string) string {
	// Step 1: Try known patterns first (no LLM needed)
	if label, found := ExtractKnownEdition(discTitle); found {
		logger.Info("edition detected via regex",
			logging.String(logging.FieldDecisionType, "edition_detection"),
			logging.String("decision_result", "detected"),
			logging.String("decision_reason", "regex_pattern_match"),
			logging.String("disc_title", discTitle),
			logging.String("edition_label", label))
		return label
	}

	// Step 2: Check if there's ambiguous extra content that might be an edition
	if !HasAmbiguousEditionMarker(discTitle, tmdbTitle) {
		logger.Debug("no edition markers detected",
			logging.String("disc_title", discTitle),
			logging.String("tmdb_title", tmdbTitle))
		return ""
	}

	// Step 3: LLM fallback for ambiguous cases
	llmCfg := i.cfg.GetLLM()
	if llmCfg.APIKey == "" {
		// LLM not configured - skip ambiguous editions
		logger.Debug("edition detection skipped for ambiguous title",
			logging.String(logging.FieldDecisionType, "edition_detection"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "llm_not_configured"),
			logging.String("disc_title", discTitle))
		return ""
	}

	client := llm.NewClientFrom(llmCfg)

	decision, err := DetectEditionWithLLM(ctx, client, discTitle, titleWithYear)
	if err != nil {
		logger.Warn("edition detection LLM call failed",
			logging.String(logging.FieldEventType, "edition_llm_failed"),
			logging.String("disc_title", discTitle),
			logging.Error(err),
			logging.String(logging.FieldErrorHint, "Check LLM configuration and network"),
			logging.String(logging.FieldImpact, "edition detection skipped"))
		return ""
	}

	const confidenceThreshold = 0.8
	if !decision.IsEdition || decision.Confidence < confidenceThreshold {
		logger.Info("edition not confirmed by LLM",
			logging.String(logging.FieldDecisionType, "edition_detection"),
			logging.String("decision_result", "not_edition"),
			logging.String("decision_reason", "llm_rejected"),
			logging.String("disc_title", discTitle),
			logging.Bool("is_edition", decision.IsEdition),
			logging.Float64("confidence", decision.Confidence),
			logging.String("llm_reason", decision.Reason))
		return ""
	}

	// Extract the edition label from the difference
	label := ExtractEditionLabel(discTitle, tmdbTitle)
	if label == "" {
		logger.Warn("edition confirmed but label extraction failed",
			logging.String(logging.FieldDecisionType, "edition_detection"),
			logging.String("decision_result", "extraction_failed"),
			logging.String("decision_reason", "no_label_extracted"),
			logging.String("disc_title", discTitle),
			logging.String("tmdb_title", tmdbTitle))
		return ""
	}

	logger.Info("edition detected via LLM",
		logging.String(logging.FieldDecisionType, "edition_detection"),
		logging.String("decision_result", "detected"),
		logging.String("decision_reason", "llm_confirmed"),
		logging.String("disc_title", discTitle),
		logging.String("edition_label", label),
		logging.Float64("confidence", decision.Confidence),
		logging.String("llm_reason", decision.Reason))

	return label
}
