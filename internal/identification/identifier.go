package identification

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/disc"
	discfingerprint "spindle/internal/disc/fingerprint"
	"spindle/internal/identification/keydb"
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
	keydb    *keydb.Catalog
	scanner  DiscScanner
	notifier notifications.Service
}

// DiscScanner defines disc scanning operations.
type DiscScanner interface {
	Scan(ctx context.Context, device string) (*disc.ScanResult, error)
}

type ripSpecEnvelope struct {
	Fingerprint string         `json:"fingerprint"`
	ContentKey  string         `json:"content_key"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Titles      []ripSpecTitle `json:"titles"`
}

type ripSpecTitle struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	Duration           int    `json:"duration"`
	ContentFingerprint string `json:"content_fingerprint"`
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
	id := &Identifier{
		store:    store,
		cfg:      cfg,
		tmdb:     newTMDBSearch(searcher),
		keydb:    catalog,
		scanner:  scanner,
		notifier: notifier,
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

	title := strings.TrimSpace(item.DiscTitle)
	titleFromKeyDB := false

	if scanResult != nil && scanResult.BDInfo != nil {
		discID := strings.TrimSpace(scanResult.BDInfo.DiscID)
		if discID != "" && i.keydb != nil {
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
			}
		}
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

	// Default metadata assumes unidentified content until TMDB lookup succeeds.
	metadata := map[string]any{
		"title": strings.TrimSpace(title),
	}
	mediaType := "unknown"
	contentKey := unknownContentKey(item.DiscFingerprint)
	identified := false
	var (
		identifiedTitle string
		year            string
		tmdbID          int64
	)

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
	} else {
		if response != nil {
			logger.Info("tmdb response received",
				logging.Int("result_count", len(response.Results)),
				logging.Int("search_year", searchOpts.Year),
				logging.Int("search_runtime", searchOpts.Runtime))

			// Log detailed results for debugging
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

		best := selectBestResult(logger, title, response)
		if best == nil {
			logger.Warn("tmdb confidence scoring failed",
				logging.String("query", title),
				logging.String("reason", "No result met confidence threshold"))
			i.scheduleReview(ctx, item, "No confident TMDB match")
		} else {
			identified = true
			mediaType = strings.ToLower(strings.TrimSpace(best.MediaType))
			if mediaType == "" {
				mediaType = "movie"
			}
			isMovie := mediaType != "tv"
			identifiedTitle = pickTitle(*best)
			year = ""
			titleWithYear := identifiedTitle
			if best.ReleaseDate != "" && len(best.ReleaseDate) >= 4 {
				year = best.ReleaseDate[:4] // Extract YYYY from YYYY-MM-DD
				titleWithYear = fmt.Sprintf("%s (%s)", identifiedTitle, year)
			}
			filenameMeta := queue.NewBasicMetadata(titleWithYear, isMovie)
			metadata = map[string]any{
				"id":           best.ID,
				"title":        identifiedTitle,
				"overview":     best.Overview,
				"media_type":   mediaType,
				"release_date": best.ReleaseDate,
				"vote_average": best.VoteAverage,
				"vote_count":   best.VoteCount,
				"movie":        isMovie,
				"filename":     filenameMeta.GetFilename(),
			}

			encodedMetadata, encodeErr := json.Marshal(metadata)
			if encodeErr != nil {
				return services.Wrap(services.ErrTransient, "identification", "encode metadata", "Failed to encode TMDB metadata", encodeErr)
			}
			item.MetadataJSON = string(encodedMetadata)
			// Update DiscTitle to the proper TMDB title with year for use in subsequent stages
			item.DiscTitle = titleWithYear
			item.ProgressStage = "Identified"
			item.ProgressPercent = 100
			item.ProgressMessage = fmt.Sprintf("Identified as: %s", titleWithYear)
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
	metadata["media_type"] = mediaType

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

	titleSpecs := make([]ripSpecTitle, 0, len(scanResult.Titles))
	for _, t := range scanResult.Titles {
		fp := discfingerprint.TitleFingerprint(t)
		titleSpecs = append(titleSpecs, ripSpecTitle{
			ID:                 t.ID,
			Name:               t.Name,
			Duration:           t.Duration,
			ContentFingerprint: fp,
		})
		logger.Info(
			"prepared title fingerprint",
			logging.Int("title_id", t.ID),
			logging.Int("duration_seconds", t.Duration),
			logging.String("title_name", strings.TrimSpace(t.Name)),
			logging.String("content_fingerprint", truncateFingerprint(fp)),
		)
	}

	ripSpec := ripSpecEnvelope{
		Fingerprint: ripFingerprint,
		ContentKey:  contentKey,
		Metadata:    metadata,
		Titles:      titleSpecs,
	}

	encodedSpec, err := json.Marshal(ripSpec)
	if err != nil {
		return services.Wrap(services.ErrTransient, "identification", "encode rip spec", "Failed to serialize rip specification", err)
	}
	item.RipSpecData = string(encodedSpec)

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
