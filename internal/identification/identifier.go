package identification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"go.uber.org/zap"

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
	store       *queue.Store
	cfg         *config.Config
	logger      *zap.Logger
	tmdb        TMDBSearcher
	scanner     DiscScanner
	cache       map[string]cacheEntry
	cacheTTL    time.Duration
	rateLimit   time.Duration
	mu          sync.Mutex
	lastRequest time.Time
	notifier    notifications.Service
}

type cacheEntry struct {
	resp    *tmdb.Response
	expires time.Time
}

// TMDBSearcher defines the subset of TMDB client functionality used by the identifier.
type TMDBSearcher interface {
	SearchMovie(ctx context.Context, query string) (*tmdb.Response, error)
	SearchMovieWithOptions(ctx context.Context, query string, opts tmdb.SearchOptions) (*tmdb.Response, error)
}

// DiscScanner defines disc scanning operations.
type DiscScanner interface {
	Scan(ctx context.Context, device string) (*disc.ScanResult, error)
}

// NewIdentifier creates a new stage handler.
func NewIdentifier(cfg *config.Config, store *queue.Store, logger *zap.Logger) *Identifier {
	client, err := tmdb.New(cfg.TMDBAPIKey, cfg.TMDBBaseURL, cfg.TMDBLanguage)
	if err != nil {
		logger.Warn("tmdb client initialization failed", zap.Error(err))
	}
	scanner := disc.NewScanner(cfg.MakemkvBinary())
	return NewIdentifierWithDependencies(cfg, store, logger, client, scanner, notifications.NewService(cfg))
}

// NewIdentifierWithClient creates a new identifier with an injected TMDB client (used for testing).
func NewIdentifierWithClient(cfg *config.Config, store *queue.Store, logger *zap.Logger, searcher TMDBSearcher) *Identifier {
	scanner := disc.NewScanner(cfg.MakemkvBinary())
	return NewIdentifierWithDependencies(cfg, store, logger, searcher, scanner, notifications.NewService(cfg))
}

// NewIdentifierWithDependencies allows injecting TMDB searcher and disc scanner (used in tests).
func NewIdentifierWithDependencies(cfg *config.Config, store *queue.Store, logger *zap.Logger, searcher TMDBSearcher, scanner DiscScanner, notifier notifications.Service) *Identifier {
	stageLogger := logger
	if stageLogger != nil {
		stageLogger = stageLogger.With(zap.String("component", "identifier"))
	}
	return &Identifier{
		store:       store,
		cfg:         cfg,
		logger:      stageLogger,
		tmdb:        searcher,
		scanner:     scanner,
		cache:       make(map[string]cacheEntry),
		cacheTTL:    10 * time.Minute,
		rateLimit:   250 * time.Millisecond,
		lastRequest: time.Unix(0, 0),
		notifier:    notifier,
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
		zap.String("disc_title", displayTitle),
		zap.String("source_path", strings.TrimSpace(item.SourcePath)),
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
			logger.Warn("disc detected notification failed", zap.Error(err))
		}
	}
	return nil
}

// Execute performs disc scanning and TMDB identification.
func (i *Identifier) Execute(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, i.logger)
	device := strings.TrimSpace(i.cfg.OpticalDrive)
	logger.Info("scanning disc with makemkv", zap.String("device", device))
	scanResult, err := i.scanDisc(ctx)
	if err != nil {
		return err
	}
	if scanResult != nil {
		titleCount := len(scanResult.Titles)
		logger.Info("disc scan completed",
			zap.Int("title_count", titleCount),
			zap.Bool("bd_info_available", scanResult.BDInfo != nil))
		if scanResult.BDInfo != nil {
			logger.Info("bd_info details",
				zap.String("volume_identifier", scanResult.BDInfo.VolumeIdentifier),
				zap.String("disc_name", scanResult.BDInfo.DiscName),
				zap.Bool("is_blu_ray", scanResult.BDInfo.IsBluRay),
				zap.Bool("has_aacs", scanResult.BDInfo.HasAACS))
		}
	}

	if scanResult.Fingerprint != "" {
		logger.Info("disc fingerprint captured", zap.String("fingerprint", scanResult.Fingerprint))
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
			zap.String("original_title", originalTitle),
			zap.String("bd_info_title", scanResult.BDInfo.DiscName),
			zap.String("volume_identifier", scanResult.BDInfo.VolumeIdentifier))
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
				zap.Int("year", scanResult.BDInfo.Year))
		}
		if scanResult.BDInfo.Studio != "" {
			logger.Info("detected studio from bd_info",
				zap.String("studio", scanResult.BDInfo.Studio))
			// Note: Studio filtering would require company lookup API call
		}
		// Calculate runtime from main title duration
		if len(scanResult.Titles) > 0 && scanResult.Titles[0].Duration > 0 {
			searchOpts.Runtime = scanResult.Titles[0].Duration / 60 // Convert seconds to minutes
			logger.Info("using title runtime for TMDB search",
				zap.Int("runtime_minutes", searchOpts.Runtime))
		}
	}

	// Log the complete TMDB query details
	logger.Info("tmdb query details",
		zap.String("query", title),
		zap.Int("year", searchOpts.Year),
		zap.String("studio", searchOpts.Studio),
		zap.Int("runtime_minutes", searchOpts.Runtime),
		zap.String("runtime_range", fmt.Sprintf("%d-%d", searchOpts.Runtime-10, searchOpts.Runtime+10)))

	response, err := i.searchTMDBWithOptions(ctx, title, searchOpts)
	if err != nil {
		logger.Warn("tmdb search failed", zap.String("title", title), zap.Error(err))
		i.scheduleReview(ctx, item, "TMDB lookup failed")
		return nil
	}
	if response != nil {
		logger.Info("tmdb response received",
			zap.Int("result_count", len(response.Results)),
			zap.Int("search_year", searchOpts.Year),
			zap.Int("search_runtime", searchOpts.Runtime))

		// Log detailed results for debugging
		for i, result := range response.Results {
			logger.Info("tmdb search result",
				zap.Int("index", i),
				zap.Int64("tmdb_id", result.ID),
				zap.String("title", result.Title),
				zap.String("release_date", result.ReleaseDate),
				zap.Float64("vote_average", result.VoteAverage),
				zap.Int64("vote_count", result.VoteCount),
				zap.Float64("popularity", result.Popularity),
				zap.String("media_type", result.MediaType))
		}
	}

	best := selectBestResult(logger, title, response, i.cfg)
	if best == nil {
		logger.Warn("tmdb confidence scoring failed",
			zap.String("query", title),
			zap.String("reason", "No result met confidence threshold"))
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
	titleWithYear := identifiedTitle
	if best.ReleaseDate != "" && len(best.ReleaseDate) >= 4 {
		year := best.ReleaseDate[:4] // Extract YYYY from YYYY-MM-DD
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
		zap.Int64("tmdb_id", best.ID),
		zap.String("identified_title", identifiedTitle),
		zap.String("media_type", strings.TrimSpace(best.MediaType)),
	)
	if i.notifier != nil {
		mediaType := strings.ToLower(strings.TrimSpace(best.MediaType))
		if mediaType == "" {
			mediaType = "unknown"
		}
		if err := i.notifier.Publish(ctx, notifications.EventIdentificationCompleted, notifications.Payload{
			"title":     identifiedTitle,
			"mediaType": mediaType,
		}); err != nil {
			logger.Warn("identification notification failed", zap.Error(err))
		}
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
	if i.tmdb == nil {
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

func (i *Identifier) handleDuplicateFingerprint(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, i.logger)
	found, err := i.store.FindByFingerprint(ctx, item.DiscFingerprint)
	if err != nil {
		return services.Wrap(services.ErrTransient, "identification", "lookup fingerprint", "Failed to query existing disc fingerprint", err)
	}
	if found != nil && found.ID != item.ID {
		logger.Info(
			"duplicate disc fingerprint detected",
			zap.Int64("existing_item_id", found.ID),
			zap.String("fingerprint", item.DiscFingerprint),
		)
		i.flagReview(ctx, item, "Duplicate disc fingerprint", true)
		item.ErrorMessage = "Duplicate disc fingerprint"
	}
	return nil
}

func (i *Identifier) scheduleReview(ctx context.Context, item *queue.Item, message string) {
	i.flagReview(ctx, item, message, false)
}

func (i *Identifier) flagReview(ctx context.Context, item *queue.Item, message string, immediate bool) {
	logger := logging.WithContext(ctx, i.logger)
	logger.Info(
		"flagging queue item for review",
		zap.String("reason", message),
		zap.Bool("immediate", immediate),
	)
	item.NeedsReview = true
	item.ReviewReason = message
	if strings.TrimSpace(item.ProgressStage) == "" || item.ProgressStage == "Identifying" {
		item.ProgressStage = "Needs review"
	}
	item.ProgressPercent = 100
	item.ProgressMessage = message
	item.ErrorMessage = message
	if immediate {
		item.Status = queue.StatusReview
		if i.notifier != nil {
			label := strings.TrimSpace(item.DiscTitle)
			if label == "" {
				label = item.DiscFingerprint
			}
			if label == "" {
				label = "Unidentified Disc"
			}
			if err := i.notifier.Publish(ctx, notifications.EventUnidentifiedMedia, notifications.Payload{"label": label}); err != nil {
				logger.Warn("unidentified media notification failed", zap.Error(err))
			}
		}
	} else {
		switch item.Status {
		case queue.StatusReview:
			// leave untouched if already review
		case queue.StatusIdentifying, queue.StatusPending, "":
			item.Status = queue.StatusIdentified
		default:
			// preserve existing status so workflow manager can decide
		}
	}
}

func (i *Identifier) searchTMDBWithOptions(ctx context.Context, title string, opts tmdb.SearchOptions) (*tmdb.Response, error) {
	if i.tmdb == nil {
		return nil, errors.New("tmdb client unavailable")
	}
	key := strings.ToLower(strings.TrimSpace(title))
	now := time.Now()
	i.mu.Lock()
	if entry, ok := i.cache[key]; ok && now.Before(entry.expires) {
		resp := entry.resp
		i.mu.Unlock()
		return resp, nil
	}
	wait := i.rateLimit - now.Sub(i.lastRequest)
	if wait > 0 {
		i.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		i.mu.Lock()
	}
	i.lastRequest = time.Now()
	i.mu.Unlock()

	resp, err := i.tmdb.SearchMovieWithOptions(ctx, title, opts)
	if err != nil {
		return nil, err
	}
	i.mu.Lock()
	i.cache[key] = cacheEntry{resp: resp, expires: time.Now().Add(i.cacheTTL)}
	i.mu.Unlock()
	return resp, nil
}

func selectBestResult(logger *zap.Logger, query string, response *tmdb.Response, cfg *config.Config) *tmdb.Result {
	if response == nil || len(response.Results) == 0 {
		return nil
	}
	queryLower := strings.ToLower(query)
	var best *tmdb.Result
	bestScore := -1.0

	logger.Info("confidence scoring analysis",
		zap.String("query", query),
		zap.Int("total_results", len(response.Results)))

	for idx := range response.Results {
		res := response.Results[idx]
		score := scoreResult(queryLower, res)

		title := pickTitle(res)
		titleLower := strings.ToLower(title)
		exactMatch := titleLower == queryLower

		logger.Info("calculating confidence score",
			zap.Int("result_index", idx),
			zap.Int64("tmdb_id", res.ID),
			zap.String("title", title),
			zap.Float64("calculated_score", score),
			zap.Float64("vote_average", res.VoteAverage),
			zap.Int64("vote_count", res.VoteCount),
			zap.Bool("exact_title_match", exactMatch),
			zap.String("match_type", func() string {
				if exactMatch {
					return "exact"
				}
				if strings.Contains(titleLower, queryLower) {
					return "contains"
				}
				return "partial"
			}()))

		if score > bestScore {
			best = &response.Results[idx]
			bestScore = score
		}
	}

	// If we have no results, return nil
	if best == nil {
		return nil
	}

	// Log the best result before applying thresholds
	title := pickTitle(*best)
	titleLower := strings.ToLower(title)
	exactMatch := titleLower == queryLower

	logger.Info("best result before confidence thresholds",
		zap.Int64("tmdb_id", best.ID),
		zap.String("title", title),
		zap.Float64("best_score", bestScore),
		zap.Float64("vote_average", best.VoteAverage),
		zap.Int64("vote_count", best.VoteCount),
		zap.Bool("exact_title_match", exactMatch))

	if exactMatch {
		// Exact title match - accept even with lower vote scores
		// Only filter out extremely low-rated content (vote_average < 2.0)
		if best.VoteAverage < 2.0 {
			logger.Warn("exact match rejected: vote average too low",
				zap.Float64("vote_average", best.VoteAverage),
				zap.Float64("threshold", 2.0))
			return nil
		}
		logger.Info("exact match accepted: confidence passed",
			zap.Float64("vote_average", best.VoteAverage),
			zap.Float64("threshold", 2.0))
		return best
	}

	// For partial matches, use the original threshold logic but be more reasonable
	// Check if vote average is reasonable (not extremely low)
	if best.VoteAverage < 3.0 {
		logger.Warn("partial match rejected: vote average too low",
			zap.Float64("vote_average", best.VoteAverage),
			zap.Float64("threshold", 3.0))
		return nil
	}

	// Apply a more lenient score threshold that accounts for the scoring formula
	// Minimum score of: 1.0 (title match) + 0.3 (reasonable vote avg) + vote_count bonus
	minExpectedScore := 1.3 + float64(best.VoteCount)/1000.0
	if bestScore < minExpectedScore {
		logger.Warn("partial match rejected: confidence score too low",
			zap.Float64("best_score", bestScore),
			zap.Float64("min_expected_score", minExpectedScore),
			zap.String("formula", "1.3 + (vote_count/1000.0)"))
		return nil
	}

	logger.Info("partial match accepted: confidence passed",
		zap.Float64("best_score", bestScore),
		zap.Float64("min_expected_score", minExpectedScore))

	return best
}

func scoreResult(query string, result tmdb.Result) float64 {
	title := pickTitle(result)
	if title == "" {
		return 0
	}
	titleLower := strings.ToLower(title)
	match := 0.0
	if strings.Contains(titleLower, query) {
		match = 1.0
	}
	return match + (result.VoteAverage / 10.0) + float64(result.VoteCount)/1000.0
}

func pickTitle(result tmdb.Result) string {
	if result.Title != "" {
		return result.Title
	}
	if result.Name != "" {
		return result.Name
	}
	return ""
}

func deriveTitle(sourcePath string) string {
	if sourcePath == "" {
		return "Unknown Disc"
	}
	base := filepath.Base(sourcePath)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	cleaned := strings.Builder{}
	prevSpace := false
	for _, r := range base {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			cleaned.WriteRune(r)
			prevSpace = false
		case unicode.IsSpace(r) || r == '-' || r == '_' || r == '.':
			if !prevSpace {
				cleaned.WriteRune(' ')
				prevSpace = true
			}
		}
	}
	title := strings.TrimSpace(cleaned.String())
	if title == "" {
		title = "Unknown Disc"
	}
	return cases.Title(language.Und).String(title)
}
