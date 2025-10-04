package identification

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/disc"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/services"
	"spindle/internal/stage"
)

// Identifier performs disc identification using MakeMKV scanning and TMDB metadata.
type Identifier struct {
	store    *queue.Store
	cfg      *config.Config
	logger   *slog.Logger
	tmdb     *tmdbSearch
	scanner  DiscScanner
	notifier notifications.Service
}

// DiscScanner defines disc scanning operations.
type DiscScanner interface {
	Scan(ctx context.Context, device string) (*disc.ScanResult, error)
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
	stageLogger := logger
	if stageLogger != nil {
		stageLogger = stageLogger.With(logging.String("component", "identifier"))
	}
	return &Identifier{
		store:    store,
		cfg:      cfg,
		logger:   stageLogger,
		tmdb:     newTMDBSearch(searcher),
		scanner:  scanner,
		notifier: notifier,
	}
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

	title := item.DiscTitle
	if title == "" {
		title = deriveTitle(item.SourcePath)
		item.DiscTitle = title
	}
	if title == "" && len(scanResult.Titles) > 0 {
		title = scanResult.Titles[0].Name
	}
	// Use bd_info disc name if title is empty or generic
	if (title == "" || disc.IsGenericLabel(title)) && scanResult.BDInfo != nil && scanResult.BDInfo.DiscName != "" {
		originalTitle := title
		title = scanResult.BDInfo.DiscName
		item.DiscTitle = title
		logger.Info("using bd_info disc name for identification",
			logging.String("original_title", originalTitle),
			logging.String("bd_info_title", scanResult.BDInfo.DiscName),
			logging.String("volume_identifier", scanResult.BDInfo.VolumeIdentifier))
	}
	if title == "" {
		title = "Unknown Disc"
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

	// Log the complete TMDB query details
	logger.Info("tmdb query details",
		logging.String("query", title),
		logging.Int("year", searchOpts.Year),
		logging.String("studio", searchOpts.Studio),
		logging.Int("runtime_minutes", searchOpts.Runtime),
		logging.String("runtime_range", fmt.Sprintf("%d-%d", searchOpts.Runtime-10, searchOpts.Runtime+10)))

	response, err := i.tmdb.search(ctx, title, searchOpts)
	if err != nil {
		logger.Warn("tmdb search failed", logging.String("title", title), logging.Error(err))
		i.scheduleReview(ctx, item, "TMDB lookup failed")
		return nil
	}
	if response != nil {
		logger.Info("tmdb response received",
			logging.Int("result_count", len(response.Results)),
			logging.Int("search_year", searchOpts.Year),
			logging.Int("search_runtime", searchOpts.Runtime))

		// Log detailed results for debugging
		for i, result := range response.Results {
			logger.Info("tmdb search result",
				logging.Int("index", i),
				logging.Int64("tmdb_id", result.ID),
				logging.String("title", result.Title),
				logging.String("release_date", result.ReleaseDate),
				logging.Float64("vote_average", result.VoteAverage),
				logging.Int64("vote_count", result.VoteCount),
				logging.Float64("popularity", result.Popularity),
				logging.String("media_type", result.MediaType))
		}
	}

	best := selectBestResult(logger, title, response)
	if best == nil {
		logger.Warn("tmdb confidence scoring failed",
			logging.String("query", title),
			logging.String("reason", "No result met confidence threshold"))
		i.scheduleReview(ctx, item, "No confident TMDB match")
		return nil
	}

	metadata := map[string]any{
		"id":           best.ID,
		"title":        pickTitle(*best),
		"overview":     best.Overview,
		"media_type":   best.MediaType,
		"release_date": best.ReleaseDate,
		"vote_average": best.VoteAverage,
		"vote_count":   best.VoteCount,
	}
	encodedMetadata, err := json.Marshal(metadata)
	if err != nil {
		return services.Wrap(services.ErrTransient, "identification", "encode metadata", "Failed to encode TMDB metadata", err)
	}
	item.MetadataJSON = string(encodedMetadata)
	// Update DiscTitle to the proper TMDB title with year for use in subsequent stages
	identifiedTitle := pickTitle(*best)
	year := ""
	titleWithYear := identifiedTitle
	if best.ReleaseDate != "" && len(best.ReleaseDate) >= 4 {
		year = best.ReleaseDate[:4] // Extract YYYY from YYYY-MM-DD
		titleWithYear = fmt.Sprintf("%s (%s)", identifiedTitle, year)
	}
	item.DiscTitle = titleWithYear
	item.ProgressStage = "Identified"
	item.ProgressPercent = 100
	item.ProgressMessage = fmt.Sprintf("Identified as: %s", titleWithYear)

	ripSpec := map[string]any{
		"fingerprint": scanResult.Fingerprint,
		"titles":      scanResult.Titles,
		"metadata":    metadata,
	}
	encodedSpec, err := json.Marshal(ripSpec)
	if err != nil {
		return services.Wrap(services.ErrTransient, "identification", "encode rip spec", "Failed to serialize rip specification", err)
	}
	item.RipSpecData = string(encodedSpec)

	logger.Info(
		"disc identified",
		logging.Int64("tmdb_id", best.ID),
		logging.String("identified_title", identifiedTitle),
		logging.String("media_type", strings.TrimSpace(best.MediaType)),
	)
	if i.notifier != nil {
		mediaType := strings.ToLower(strings.TrimSpace(best.MediaType))
		if mediaType == "" {
			mediaType = "unknown"
		}
		if strings.TrimSpace(year) != "" {
			payload := notifications.Payload{
				"title":        identifiedTitle,
				"year":         strings.TrimSpace(year),
				"mediaType":    mediaType,
				"displayTitle": titleWithYear,
			}
			if err := i.notifier.Publish(ctx, notifications.EventIdentificationCompleted, payload); err != nil {
				logger.Warn("identification notification failed", logging.Error(err))
			}
		}
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

	var ripSpec struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal([]byte(ripSpecRaw), &ripSpec); err != nil {
		logger.Error("identification validation failed", logging.String("reason", "invalid rip spec"), logging.Error(err))
		return services.Wrap(
			services.ErrValidation,
			"identification",
			"parse rip spec",
			"Rip specification is invalid JSON; cannot continue",
			err,
		)
	}
	if specFingerprint := strings.TrimSpace(ripSpec.Fingerprint); !strings.EqualFold(specFingerprint, fingerprint) {
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
