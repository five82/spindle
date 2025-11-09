package identification

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/disc"
	discfingerprint "spindle/internal/disc/fingerprint"
	"spindle/internal/identification/keydb"
	"spindle/internal/identification/overrides"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/stage"
)

// Identifier performs disc identification using MakeMKV scanning and TMDB metadata.
type Identifier struct {
	store     *queue.Store
	cfg       *config.Config
	logger    *slog.Logger
	tmdb      *tmdbSearch
	tmdbInfo  TMDBSearcher
	keydb     *keydb.Catalog
	overrides *overrides.Catalog
	scanner   DiscScanner
	notifier  notifications.Service
}

// DiscScanner defines disc scanning operations.
type DiscScanner interface {
	Scan(ctx context.Context, device string) (*disc.ScanResult, error)
}

func isPlaceholderTitle(title, discLabel string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	if t == "" {
		return true
	}
	if t == "unknown disc" || strings.HasPrefix(t, "unknown disc") {
		return true
	}
	if strings.TrimSpace(discLabel) != "" && strings.EqualFold(strings.TrimSpace(title), strings.TrimSpace(discLabel)) {
		return true
	}
	return false
}

// NewIdentifier creates a new stage handler.
func NewIdentifier(cfg *config.Config, store *queue.Store, logger *slog.Logger) *Identifier {
	client, err := tmdb.New(cfg.TMDBAPIKey, cfg.TMDBBaseURL, cfg.TMDBLanguage)
	if err != nil {
		logger.Warn("tmdb client initialization failed", logging.Error(err))
	}
	scanner := disc.NewScanner(cfg.MakemkvBinary())
	return NewIdentifierWithDependencies(cfg, store, logger, client, scanner, notifications.NewService(cfg))
}

// NewIdentifierWithDependencies allows injecting TMDB searcher and disc scanner (used in tests).
func NewIdentifierWithDependencies(cfg *config.Config, store *queue.Store, logger *slog.Logger, searcher TMDBSearcher, scanner DiscScanner, notifier notifications.Service) *Identifier {
	var catalog *keydb.Catalog
	if cfg != nil {
		timeout := time.Duration(cfg.KeyDBDownloadTimeout) * time.Second
		catalog = keydb.NewCatalog(cfg.KeyDBPath, logger, cfg.KeyDBDownloadURL, timeout)
	}
	var overrideCatalog *overrides.Catalog
	if cfg != nil {
		overrideCatalog = overrides.NewCatalog(cfg.IdentificationOverridesPath, logger)
	}
	id := &Identifier{
		store:     store,
		cfg:       cfg,
		tmdb:      newTMDBSearch(searcher),
		tmdbInfo:  searcher,
		keydb:     catalog,
		overrides: overrideCatalog,
		scanner:   scanner,
		notifier:  notifier,
	}
	id.SetLogger(logger)
	return id
}

// SetLogger updates the identifier's logging destination while preserving component labeling.
func (i *Identifier) SetLogger(logger *slog.Logger) {
	stageLogger := logger
	if stageLogger == nil {
		stageLogger = logging.NewNop()
	}
	i.logger = stageLogger.With(logging.String("component", "identifier"))
}

// Prepare initializes progress messaging prior to Execute.
func (i *Identifier) Prepare(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, i.logger)
	if item.ProgressStage == "" {
		item.ProgressStage = "Identifying"
	}
	item.ProgressMessage = "Fetching metadata"
	item.ProgressPercent = 0

	displayTitle := strings.TrimSpace(item.DiscTitle)
	if displayTitle == "" {
		displayTitle = deriveTitle(item.SourcePath)
	}
	logger.Info(
		"starting disc identification",
		logging.String("disc_title", displayTitle),
		logging.String("source_path", strings.TrimSpace(item.SourcePath)),
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
			logger.Warn("disc detected notification failed", logging.Error(err))
		}
	}
	return nil
}

// Execute performs disc scanning and TMDB identification.
func (i *Identifier) Execute(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, i.logger)
	device := strings.TrimSpace(i.cfg.OpticalDrive)
	logger.Info("scanning disc with makemkv", logging.String("device", device))
	scanResult, err := i.scanDisc(ctx)
	if err != nil {
		return err
	}
	if scanResult != nil {
		titleCount := len(scanResult.Titles)
		logger.Info("disc scan completed",
			logging.Int("title_count", titleCount),
			logging.Bool("bd_info_available", scanResult.BDInfo != nil))
		if scanResult.BDInfo != nil {
			logger.Info("bd_info details",
				logging.String("disc_id", strings.TrimSpace(scanResult.BDInfo.DiscID)),
				logging.String("volume_identifier", scanResult.BDInfo.VolumeIdentifier),
				logging.String("disc_name", scanResult.BDInfo.DiscName),
				logging.Bool("is_blu_ray", scanResult.BDInfo.IsBluRay),
				logging.Bool("has_aacs", scanResult.BDInfo.HasAACS))
		}
	}

	if scanResult.Fingerprint != "" {
		logger.Info("disc fingerprint captured", logging.String("fingerprint", scanResult.Fingerprint))
		item.DiscFingerprint = scanResult.Fingerprint
		if err := i.handleDuplicateFingerprint(ctx, item); err != nil {
			return err
		}
		if item.Status == queue.StatusReview {
			return nil
		}
	}

	discID := ""
	if scanResult != nil && scanResult.BDInfo != nil {
		discID = strings.TrimSpace(scanResult.BDInfo.DiscID)
	}
	var overrideMatch *overrides.Override
	if i.overrides != nil {
		if match, ok, err := i.overrides.Lookup(item.DiscFingerprint, discID); err != nil {
			logger.Warn("override lookup failed", logging.Error(err))
		} else if ok {
			overrideMatch = &match
			logger.Info("identification override matched", logging.String("override_title", match.Title), logging.Int64("override_tmdb_id", match.TMDBID))
		}
	}

	title := strings.TrimSpace(item.DiscTitle)
	titleFromKeyDB := false

	if scanResult != nil && scanResult.BDInfo != nil {
		switch {
		case discID == "" && i.keydb != nil:
			logger.Info("keydb lookup skipped", logging.String("reason", "disc id missing in bdinfo"))
		case i.keydb == nil:
			logger.Info("keydb lookup skipped", logging.String("reason", "keydb catalog unavailable"))
		case discID != "" && i.keydb != nil:
			entry, found, err := i.keydb.Lookup(discID)
			if err != nil {
				logger.Warn("keydb lookup failed",
					logging.String("disc_id", discID),
					logging.Error(err))
			} else if found {
				keydbTitle := strings.TrimSpace(entry.Title)
				if keydbTitle != "" {
					logger.Info("title updated from keydb",
						logging.String("disc_id", discID),
						logging.String("new_title", keydbTitle))
					title = keydbTitle
					item.DiscTitle = title
					titleFromKeyDB = true
				}
			} else {
				logger.Info("keydb lookup produced no match",
					logging.String("disc_id", discID))
			}
		}
	} else if i.keydb != nil {
		logger.Info("keydb lookup skipped", logging.String("reason", "bdinfo unavailable"))
	}

	if !titleFromKeyDB {
		// Determine best title using priority-based approach
		logger.Info("determining best title",
			logging.String("current_title", title),
			logging.Int("makemkv_titles", len(scanResult.Titles)))

		if len(scanResult.Titles) > 0 {
			logger.Info("makemkv title available",
				logging.String("makemkv_title", scanResult.Titles[0].Name))
		}

		if scanResult.BDInfo != nil {
			logger.Info("bdinfo available",
				logging.String("bdinfo_name", scanResult.BDInfo.DiscName))
		}

		bestTitle := determineBestTitle(title, scanResult)
		if bestTitle != title {
			logger.Info("title updated based on priority sources",
				logging.String("original_title", title),
				logging.String("new_title", bestTitle),
				logging.String("source", detectTitleSource(bestTitle, scanResult)))
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
			logger.Info("using bd_info year for TMDB search",
				logging.Int("year", scanResult.BDInfo.Year))
		}
		if scanResult.BDInfo.Studio != "" {
			logger.Info("detected studio from bd_info",
				logging.String("studio", scanResult.BDInfo.Studio))
			// Note: Studio filtering would require company lookup API call
		}
		// Calculate runtime from main title duration
		if len(scanResult.Titles) > 0 && scanResult.Titles[0].Duration > 0 {
			searchOpts.Runtime = scanResult.Titles[0].Duration / 60 // Convert seconds to minutes
			logger.Info("using title runtime for TMDB search",
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
	if overrideMatch != nil && strings.TrimSpace(overrideMatch.Title) != "" {
		discSources = append(discSources, overrideMatch.Title)
	}
	if n, ok := extractDiscNumber(discSources...); ok {
		discNumber = n
		logger.Info("disc number detected", logging.Int("disc_number", discNumber))
	}

	// Default metadata assumes unidentified content until TMDB lookup succeeds.
	metadata := map[string]any{
		"title": strings.TrimSpace(title),
	}
	var attributes map[string]any
	if discNumber > 0 {
		metadata["disc_number"] = discNumber
		attributes = map[string]any{"disc_number": discNumber}
	}
	mediaHint := detectMediaKind(title, discLabel, scanResult)
	if hint := mediaHint.String(); hint != "unknown" {
		metadata["media_type"] = hint
	}
	mediaType := fmt.Sprintf("%v", metadata["media_type"])
	if mediaType == "<nil>" || strings.TrimSpace(mediaType) == "" {
		mediaType = "unknown"
	}
	contentKey := unknownContentKey(item.DiscFingerprint)
	identified := false
	var (
		identifiedTitle string
		year            string
		tmdbID          int64
		seasonNumber    int
		episodeMatches  map[int]episodeAnnotation
		matchedEpisodes []int
	)
	if overrideMatch != nil && overrideMatch.Season > 0 {
		seasonNumber = overrideMatch.Season
	}

	showHintSources := []string{title}
	if discLabel != "" {
		showHintSources = append(showHintSources, discLabel)
	}
	if scanResult != nil && scanResult.BDInfo != nil {
		if scanResult.BDInfo.DiscName != "" {
			showHintSources = append(showHintSources, scanResult.BDInfo.DiscName)
		}
		if scanResult.BDInfo.VolumeIdentifier != "" {
			showHintSources = append(showHintSources, scanResult.BDInfo.VolumeIdentifier)
		}
	}
	if overrideMatch != nil && strings.TrimSpace(overrideMatch.Title) != "" {
		showHintSources = append(showHintSources, overrideMatch.Title)
	}
	showHint, hintedSeason := deriveShowHint(showHintSources...)
	if seasonNumber == 0 && hintedSeason > 0 {
		seasonNumber = hintedSeason
	}

	if season, ok := extractSeasonNumber(title, discLabel); ok {
		seasonNumber = season
	}
	logger.Info("identification heuristics",
		logging.String("media_hint", mediaHint.String()),
		logging.Int("season_guess", seasonNumber))

	queryInputs := []string{title, showHint}
	if overrideMatch != nil {
		queryInputs = append(queryInputs, overrideMatch.Title)
	}
	if discLabel != "" {
		queryInputs = append(queryInputs, discLabel)
	}
	queries := buildQueryList(queryInputs...)
	if len(queries) == 0 {
		queries = []string{strings.TrimSpace(title)}
	}

	if isPlaceholderTitle(title, discLabel) {
		logger.Info("tmdb lookup skipped for placeholder title",
			logging.String("title", title),
			logging.String("disc_label", discLabel))
		i.scheduleReview(ctx, item, "Disc title placeholder; manual identification required")
	} else {
		var (
			best      *tmdb.Result
			response  *tmdb.Response
			modeUsed  searchMode
			searchErr error
		)
		for _, candidate := range queries {
			resp, mode, err := i.performTMDBSearch(ctx, logger, candidate, searchOpts, mediaHint)
			if err != nil {
				searchErr = err
				logger.Warn("tmdb search attempt failed", logging.String("query", candidate), logging.Error(err))
				continue
			}
			response = resp
			modeUsed = mode
			if response != nil {
				logger.Info("tmdb response received",
					logging.Int("result_count", len(response.Results)),
					logging.Int("search_year", searchOpts.Year),
					logging.Int("search_runtime", searchOpts.Runtime),
					logging.String("search_mode", string(modeUsed)),
					logging.String("query", candidate))
				for idx, result := range response.Results {
					logger.Info("tmdb search result",
						logging.Int("index", idx),
						logging.Int64("tmdb_id", result.ID),
						logging.String("title", result.Title),
						logging.String("release_date", result.ReleaseDate),
						logging.Float64("vote_average", result.VoteAverage),
						logging.Int64("vote_count", result.VoteCount),
						logging.Float64("popularity", result.Popularity),
						logging.String("media_type", result.MediaType))
				}
			}
			best = selectBestResult(logger, candidate, response)
			if best != nil {
				break
			}
		}
		if best == nil {
			lastQuery := queries[len(queries)-1]
			if searchErr != nil {
				logger.Warn("tmdb search failed", logging.String("query", lastQuery), logging.Error(searchErr))
				i.scheduleReview(ctx, item, "TMDB lookup failed")
			} else {
				logger.Warn("tmdb confidence scoring failed",
					logging.String("query", lastQuery),
					logging.String("reason", "No result met confidence threshold"))
				i.scheduleReview(ctx, item, "No confident TMDB match")
			}
		} else {
			identified = true
			mediaType = strings.ToLower(strings.TrimSpace(best.MediaType))
			if mediaType == "" {
				switch modeUsed {
				case searchModeTV:
					mediaType = "tv"
				case searchModeMulti:
					mediaType = strings.TrimSpace(best.MediaType)
					if mediaType == "" {
						mediaType = "movie"
					}
				default:
					mediaType = "movie"
				}
			}
			isMovie := mediaType != "tv"
			identifiedTitle = pickTitle(*best)
			year = ""
			titleWithYear := identifiedTitle
			releaseDate := best.ReleaseDate
			if mediaType == "tv" && strings.TrimSpace(best.FirstAirDate) != "" {
				releaseDate = best.FirstAirDate
			}
			if releaseDate != "" && len(releaseDate) >= 4 {
				year = releaseDate[:4]
				titleWithYear = fmt.Sprintf("%s (%s)", identifiedTitle, year)
			}
			tmdbID = best.ID
			if mediaType == "tv" {
				if seasonNumber == 0 {
					if season, ok := extractSeasonNumber(item.DiscTitle, title, discLabel); ok {
						seasonNumber = season
					}
				}
				if seasonNumber == 0 {
					seasonNumber = 1
				}
				matches, episodes := i.annotateEpisodes(ctx, logger, tmdbID, seasonNumber, discNumber, scanResult)
				episodeMatches = matches
				matchedEpisodes = episodes
			}
			metadata = map[string]any{
				"id":             best.ID,
				"title":          identifiedTitle,
				"overview":       best.Overview,
				"media_type":     mediaType,
				"release_date":   releaseDate,
				"first_air_date": best.FirstAirDate,
				"vote_average":   best.VoteAverage,
				"vote_count":     best.VoteCount,
				"movie":          isMovie,
				"season_number":  seasonNumber,
			}
			if len(matchedEpisodes) > 0 {
				metadata["episode_numbers"] = matchedEpisodes
			}
			if mediaType == "tv" {
				metadata["show_title"] = identifiedTitle
			}
			var metaRecord queue.Metadata
			if mediaType == "tv" {
				metaRecord = queue.NewTVMetadata(identifiedTitle, seasonNumber, matchedEpisodes, fmt.Sprintf("%s Season %02d", identifiedTitle, seasonNumber))
			} else {
				metaRecord = queue.NewBasicMetadata(titleWithYear, true)
			}
			metadata["filename"] = metaRecord.GetFilename()
			if mediaType == "tv" {
				metadata["show_title"] = identifiedTitle
			}

			encodedMetadata, encodeErr := json.Marshal(metadata)
			if encodeErr != nil {
				return services.Wrap(services.ErrTransient, "identification", "encode metadata", "Failed to encode TMDB metadata", encodeErr)
			}
			item.MetadataJSON = string(encodedMetadata)
			// Update DiscTitle to the proper TMDB title with season/year for subsequent stages
			displayTitle := titleWithYear
			if mediaType == "tv" {
				displayTitle = fmt.Sprintf("%s Season %02d", identifiedTitle, seasonNumber)
				if strings.TrimSpace(year) != "" {
					displayTitle = fmt.Sprintf("%s Season %02d (%s)", identifiedTitle, seasonNumber, year)
				}
			}
			item.DiscTitle = displayTitle
			item.ProgressStage = "Identified"
			item.ProgressPercent = 100
			item.ProgressMessage = fmt.Sprintf("Identified as: %s", item.DiscTitle)
			tmdbID = best.ID
			contentKey = fmt.Sprintf("tmdb:%s:%d", mediaType, tmdbID)

			logger.Info(
				"disc identified",
				logging.Int64("tmdb_id", best.ID),
				logging.String("identified_title", identifiedTitle),
				logging.String("media_type", strings.TrimSpace(best.MediaType)),
			)
			if i.notifier != nil {
				notifyType := mediaType
				if notifyType == "" {
					notifyType = "unknown"
				}
				if strings.TrimSpace(year) != "" {
					payload := notifications.Payload{
						"title":        identifiedTitle,
						"year":         strings.TrimSpace(year),
						"mediaType":    notifyType,
						"displayTitle": titleWithYear,
					}
					if err := i.notifier.Publish(ctx, notifications.EventIdentificationCompleted, payload); err != nil {
						logger.Warn("identification notification failed", logging.Error(err))
					}
				}
			}
		}
	}

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
			logger.Warn("failed to encode fallback metadata", logging.Error(err))
		}
	}

	ripFingerprint := strings.TrimSpace(scanResult.Fingerprint)
	if ripFingerprint == "" {
		fallback := strings.TrimSpace(item.DiscFingerprint)
		if fallback != "" {
			logger.Warn(
				"scanner fingerprint missing; using queue fingerprint",
				logging.String("fallback_fingerprint", fallback),
			)
			ripFingerprint = fallback
		}
	}

	episodeSpecs := make([]ripspec.Episode, 0, len(episodeMatches))
	titleSpecs := make([]ripspec.Title, 0, len(scanResult.Titles))
	for _, t := range scanResult.Titles {
		fp := discfingerprint.TitleFingerprint(t)
		spec := ripspec.Title{
			ID:                 t.ID,
			Name:               t.Name,
			Duration:           t.Duration,
			ContentFingerprint: fp,
		}
		if annotation, ok := episodeMatches[t.ID]; ok {
			spec.Season = annotation.Season
			spec.Episode = annotation.Episode
			spec.EpisodeTitle = annotation.Title
			spec.EpisodeAirDate = annotation.Air
			if annotation.Season > 0 && annotation.Episode > 0 {
				showLabel := identifiedTitle
				if strings.TrimSpace(showLabel) == "" {
					if value, ok := metadata["title"].(string); ok && strings.TrimSpace(value) != "" {
						showLabel = value
					} else {
						showLabel = title
					}
				}
				episodeSpecs = append(episodeSpecs, ripspec.Episode{
					Key:                ripspec.EpisodeKey(annotation.Season, annotation.Episode),
					TitleID:            t.ID,
					Season:             annotation.Season,
					Episode:            annotation.Episode,
					EpisodeTitle:       annotation.Title,
					EpisodeAirDate:     annotation.Air,
					RuntimeSeconds:     t.Duration,
					ContentFingerprint: fp,
					OutputBasename:     episodeOutputBasename(showLabel, annotation.Season, annotation.Episode),
				})
			}
		}
		titleSpecs = append(titleSpecs, spec)
		logFields := []any{
			logging.Int("title_id", t.ID),
			logging.Int("duration_seconds", t.Duration),
			logging.String("title_name", strings.TrimSpace(t.Name)),
			logging.String("content_fingerprint", truncateFingerprint(fp)),
		}
		if spec.Season > 0 && spec.Episode > 0 {
			logFields = append(logFields,
				logging.Int("season", spec.Season),
				logging.Int("episode", spec.Episode),
				logging.String("episode_title", strings.TrimSpace(spec.EpisodeTitle)))
		}
		logger.Info("prepared title fingerprint", logFields...)
	}

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
			logging.Int("title_count", len(titleSpecs)),
			logging.String("content_key", contentKey),
		)
	}

	if err := i.validateIdentification(ctx, item); err != nil {
		return err
	}

	return nil
}

// HealthCheck verifies identifier dependencies required for successful execution.
func (i *Identifier) HealthCheck(ctx context.Context) stage.Health {
	const name = "identifier"
	if i.cfg == nil {
		return stage.Unhealthy(name, "configuration unavailable")
	}
	if strings.TrimSpace(i.cfg.TMDBAPIKey) == "" {
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

func (i *Identifier) scanDisc(ctx context.Context) (*disc.ScanResult, error) {
	if i.scanner == nil {
		return nil, services.Wrap(
			services.ErrConfiguration,
			"identification",
			"initialize scanner",
			"Disc scanner unavailable; install MakeMKV and ensure it is in PATH",
			nil,
		)
	}
	device := strings.TrimSpace(i.cfg.OpticalDrive)
	if device == "" {
		return nil, services.Wrap(
			services.ErrConfiguration,
			"identification",
			"resolve optical drive",
			"Optical drive path not configured; set optical_drive in spindle config to your MakeMKV drive identifier",
			nil,
		)
	}
	result, err := i.scanner.Scan(ctx, device)
	if err != nil {
		return nil, services.Wrap(services.ErrExternalTool, "identification", "makemkv scan", "MakeMKV disc scan failed", err)
	}
	return result, nil
}

func (i *Identifier) validateIdentification(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, i.logger)
	fingerprint := strings.TrimSpace(item.DiscFingerprint)
	if fingerprint == "" {
		logger.Error("identification validation failed", logging.String("reason", "missing fingerprint"))
		return services.Wrap(
			services.ErrValidation,
			"identification",
			"validate fingerprint",
			"Disc fingerprint missing after identification; rerun identification to capture MakeMKV scan results",
			nil,
		)
	}

	ripSpecRaw := strings.TrimSpace(item.RipSpecData)
	if ripSpecRaw == "" {
		logger.Error("identification validation failed", logging.String("reason", "missing rip spec"))
		return services.Wrap(
			services.ErrValidation,
			"identification",
			"validate rip spec",
			"Rip specification missing after identification; unable to determine ripping instructions",
			nil,
		)
	}

	spec, err := ripspec.Parse(ripSpecRaw)
	if err != nil {
		logger.Error("identification validation failed", logging.String("reason", "invalid rip spec"), logging.Error(err))
		return services.Wrap(
			services.ErrValidation,
			"identification",
			"parse rip spec",
			"Rip specification is invalid JSON; cannot continue",
			err,
		)
	}
	if specFingerprint := strings.TrimSpace(spec.Fingerprint); !strings.EqualFold(specFingerprint, fingerprint) {
		logger.Error(
			"identification validation failed",
			logging.String("reason", "fingerprint mismatch"),
			logging.String("item_fingerprint", fingerprint),
			logging.String("spec_fingerprint", specFingerprint),
		)
		return services.Wrap(
			services.ErrValidation,
			"identification",
			"validate rip spec fingerprint",
			"Rip specification fingerprint does not match queue item fingerprint",
			nil,
		)
	}

	if err := i.ensureStagingSkeleton(item); err != nil {
		return err
	}

	logger.Info(
		"identification validation succeeded",
		logging.String("fingerprint", fingerprint),
		logging.String("staging_root", item.StagingRoot(i.cfg.StagingDir)),
	)

	return nil
}

func (i *Identifier) performTMDBSearch(ctx context.Context, logger *slog.Logger, title string, opts tmdb.SearchOptions, hint mediaKind) (*tmdb.Response, searchMode, error) {
	orders := searchOrderForHint(hint)
	var lastErr error
	var lastResp *tmdb.Response
	modeUsed := searchModeMovie
	for _, mode := range orders {
		logger.Info("tmdb query details",
			logging.String("query", title),
			logging.String("mode", string(mode)),
			logging.Int("year", opts.Year),
			logging.String("studio", opts.Studio),
			logging.Int("runtime_minutes", opts.Runtime),
			logging.String("runtime_range", fmt.Sprintf("%d-%d", opts.Runtime-10, opts.Runtime+10)))
		resp, err := i.tmdb.search(ctx, title, opts, mode)
		if err != nil {
			lastErr = err
			logger.Warn("tmdb search attempt failed", logging.String("mode", string(mode)), logging.Error(err))
			continue
		}
		if resp != nil {
			lastResp = resp
			modeUsed = mode
			if len(resp.Results) > 0 {
				return resp, mode, nil
			}
		}
	}
	return lastResp, modeUsed, lastErr
}

func searchOrderForHint(h mediaKind) []searchMode {
	switch h {
	case mediaKindTV:
		return []searchMode{searchModeTV, searchModeMovie, searchModeMulti}
	case mediaKindMovie:
		return []searchMode{searchModeMovie, searchModeTV, searchModeMulti}
	default:
		return []searchMode{searchModeMovie, searchModeTV, searchModeMulti}
	}
}

type episodeAnnotation struct {
	Season  int
	Episode int
	Title   string
	Air     string
}

func (i *Identifier) annotateEpisodes(ctx context.Context, logger *slog.Logger, tmdbID int64, seasonNumber int, discNumber int, scanResult *disc.ScanResult) (map[int]episodeAnnotation, []int) {
	if tmdbID == 0 || seasonNumber <= 0 || scanResult == nil || len(scanResult.Titles) == 0 {
		return nil, nil
	}
	if i.tmdbInfo == nil {
		logger.Warn("tmdb season lookup unavailable", logging.String("reason", "tmdb client missing"))
		return nil, nil
	}
	season, err := i.tmdbInfo.GetSeasonDetails(ctx, tmdbID, seasonNumber)
	if err != nil {
		logger.Warn("tmdb season lookup failed",
			logging.Int64("tmdb_id", tmdbID),
			logging.Int("season", seasonNumber),
			logging.Error(err))
		return nil, nil
	}
	if season == nil || len(season.Episodes) == 0 {
		logger.Info("tmdb season lookup returned no episodes",
			logging.Int64("tmdb_id", tmdbID),
			logging.Int("season", seasonNumber))
		return nil, nil
	}
	matches, numbers := mapEpisodesToTitles(scanResult.Titles, season.Episodes, discNumber)
	return matches, numbers
}

func mapEpisodesToTitles(titles []disc.Title, episodes []tmdb.Episode, discNumber int) (map[int]episodeAnnotation, []int) {
	if len(titles) == 0 || len(episodes) == 0 {
		return nil, nil
	}
	assigned := make(map[int]episodeAnnotation)
	used := make([]bool, len(episodes))
	epTitles := make([]disc.Title, 0, len(titles))
	for _, title := range titles {
		if isEpisodeRuntime(title.Duration) {
			epTitles = append(epTitles, title)
		}
	}
	if len(epTitles) == 0 {
		return nil, nil
	}
	start := estimateEpisodeStart(discNumber, len(epTitles), len(episodes))
	for _, title := range epTitles {
		idx := chooseEpisodeForTitle(title.Duration, episodes, used, start)
		if idx == -1 {
			continue
		}
		used[idx] = true
		ep := episodes[idx]
		assigned[title.ID] = episodeAnnotation{
			Season:  ep.SeasonNumber,
			Episode: ep.EpisodeNumber,
			Title:   strings.TrimSpace(ep.Name),
			Air:     strings.TrimSpace(ep.AirDate),
		}
	}
	if len(assigned) == 0 {
		return nil, nil
	}
	numbers := make([]int, 0, len(assigned))
	for _, ann := range assigned {
		if ann.Episode > 0 {
			numbers = append(numbers, ann.Episode)
		}
	}
	sort.Ints(numbers)
	return assigned, numbers
}

func estimateEpisodeStart(discNumber int, discEpisodes int, totalEpisodes int) int {
	if discNumber <= 1 || discEpisodes <= 0 || totalEpisodes == 0 {
		return 0
	}
	start := (discNumber - 1) * discEpisodes
	if start >= totalEpisodes {
		start = totalEpisodes - discEpisodes
		if start < 0 {
			start = 0
		}
	}
	return start
}

func chooseEpisodeForTitle(durationSeconds int, episodes []tmdb.Episode, used []bool, startIndex int) int {
	if len(episodes) == 0 {
		return -1
	}
	bestIdx := -1
	bestDelta := int(^uint(0) >> 1)
	if startIndex < 0 {
		startIndex = 0
	}
	if startIndex > len(episodes) {
		startIndex = len(episodes)
	}
	for idx := startIndex; idx < len(episodes); idx++ {
		ep := episodes[idx]
		if idx < len(used) && used[idx] {
			continue
		}
		if ep.SeasonNumber <= 0 {
			continue
		}
		runtime := ep.Runtime
		if runtime <= 0 {
			runtime = durationSeconds / 60
			if runtime == 0 {
				runtime = 22
			}
		}
		delta := absInt(runtime*60 - durationSeconds)
		if delta < bestDelta {
			bestDelta = delta
			bestIdx = idx
		}
	}
	const maxAcceptableDelta = 5 * 60
	if bestIdx != -1 && bestDelta <= maxAcceptableDelta {
		return bestIdx
	}
	for idx := 0; idx < len(episodes); idx++ {
		if idx < startIndex {
			if idx < len(used) && used[idx] {
				continue
			}
			ep := episodes[idx]
			delta := episodeDurationDelta(durationSeconds, ep)
			if delta < bestDelta {
				bestDelta = delta
				bestIdx = idx
			}
		}
	}
	if bestIdx != -1 && bestDelta <= maxAcceptableDelta {
		return bestIdx
	}
	for idx := range episodes {
		if idx < len(used) && used[idx] {
			continue
		}
		return idx
	}
	return -1
}

func episodeDurationDelta(durationSeconds int, ep tmdb.Episode) int {
	runtime := ep.Runtime
	if runtime <= 0 {
		runtime = durationSeconds / 60
		if runtime == 0 {
			runtime = 22
		}
	}
	return absInt(runtime*60 - durationSeconds)
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func episodeOutputBasename(show string, season, episode int) string {
	show = strings.TrimSpace(show)
	if show == "" {
		show = "Manual Import"
	}
	display := fmt.Sprintf("%s Season %02d", show, season)
	meta := queue.NewTVMetadata(show, season, []int{episode}, display)
	name := meta.GetFilename()
	if strings.TrimSpace(name) == "" {
		return fmt.Sprintf("%s - S%02dE%02d", strings.TrimSpace(show), season, episode)
	}
	return name
}

func (i *Identifier) ensureStagingSkeleton(item *queue.Item) error {
	if i.cfg == nil {
		return services.Wrap(
			services.ErrConfiguration,
			"identification",
			"resolve configuration",
			"Configuration unavailable; cannot allocate staging directory",
			nil,
		)
	}
	base := strings.TrimSpace(i.cfg.StagingDir)
	if base == "" {
		return services.Wrap(
			services.ErrConfiguration,
			"identification",
			"resolve staging dir",
			"staging_dir is empty; configure staging directories before ripping",
			nil,
		)
	}
	root := strings.TrimSpace(item.StagingRoot(base))
	if root == "" {
		return services.Wrap(
			services.ErrValidation,
			"identification",
			"determine staging root",
			"Unable to determine staging directory for fingerprint",
			nil,
		)
	}
	for _, sub := range []string{"", "rips", "encoded", "organizing"} {
		path := root
		if sub != "" {
			path = filepath.Join(root, sub)
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return services.Wrap(
				services.ErrConfiguration,
				"identification",
				"create staging directories",
				fmt.Sprintf("Failed to create staging directory %q", path),
				err,
			)
		}
	}
	return nil
}

func unknownContentKey(fingerprint string) string {
	fp := strings.TrimSpace(fingerprint)
	if fp == "" {
		return "unknown:pending"
	}
	if len(fp) > 16 {
		fp = fp[:16]
	}
	return fmt.Sprintf("unknown:%s", strings.ToLower(fp))
}

func truncateFingerprint(value string) string {
	v := strings.TrimSpace(value)
	if len(v) <= 12 {
		return v
	}
	return v[:12]
}

func determineBestTitle(currentTitle string, scanResult *disc.ScanResult) string {
	// Priority 1: MakeMKV title (highest quality - reads actual disc metadata)
	if len(scanResult.Titles) > 0 {
		makemkvTitle := strings.TrimSpace(scanResult.Titles[0].Name)
		if makemkvTitle != "" && !isTechnicalLabel(makemkvTitle) {
			return makemkvTitle
		}
	}

	// Priority 2: BDInfo disc name (Blu-ray specific, good quality)
	if scanResult.BDInfo != nil {
		bdName := strings.TrimSpace(scanResult.BDInfo.DiscName)
		if bdName != "" && !isTechnicalLabel(bdName) {
			return bdName
		}
	}

	// Priority 3: Current title (usually raw disc label, lowest quality)
	if currentTitle != "" && !isTechnicalLabel(currentTitle) {
		return currentTitle
	}

	// Priority 4: Try to derive from source path (file-based identification)
	derived := strings.TrimSpace(deriveTitle(""))
	if derived != "" && !disc.IsGenericLabel(derived) {
		return derived
	}

	return "Unknown Disc"
}

func isTechnicalLabel(title string) bool {
	if strings.TrimSpace(title) == "" {
		return true
	}

	upper := strings.ToUpper(title)

	// Common technical/generic patterns
	technicalPatterns := []string{
		"LOGICAL_VOLUME_ID",
		"DVD_VIDEO",
		"BLURAY",
		"BD_ROM",
		"UNTITLED",
		"UNKNOWN DISC",
		"VOLUME_",
		"VOLUME ID",
		"DISK_",
		"TRACK_",
	}

	for _, pattern := range technicalPatterns {
		if strings.Contains(upper, pattern) {
			return true
		}
	}

	// All uppercase with underscores (likely technical label)
	if strings.Contains(title, "_") && title == strings.ToUpper(title) && len(title) > 8 {
		return true
	}

	// All numbers or very short uppercase codes
	if regexp.MustCompile(`^\d+$`).MatchString(title) || regexp.MustCompile(`^[A-Z0-9_]{1,4}$`).MatchString(title) {
		return true
	}

	return false
}

func detectTitleSource(title string, scanResult *disc.ScanResult) string {
	if len(scanResult.Titles) > 0 {
		makemkvTitle := strings.TrimSpace(scanResult.Titles[0].Name)
		if makemkvTitle == title {
			return "MakeMKV"
		}
	}

	if scanResult.BDInfo != nil {
		bdName := strings.TrimSpace(scanResult.BDInfo.DiscName)
		if bdName == title {
			return "BDInfo"
		}
	}

	if title == "Unknown Disc" {
		return "Default"
	}

	return "Original"
}
