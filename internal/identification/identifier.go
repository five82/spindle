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
	"spindle/internal/workflow"
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

// Name identifies the handler for logging.
func (i *Identifier) Name() string { return "identifier" }

// TriggerStatus indicates this handler processes pending items.
func (i *Identifier) TriggerStatus() queue.Status { return queue.StatusPending }

// ProcessingStatus returns the in-flight status while identification runs.
func (i *Identifier) ProcessingStatus() queue.Status { return queue.StatusIdentifying }

// NextStatus indicates the status after successful identification.
func (i *Identifier) NextStatus() queue.Status { return queue.StatusIdentified }

// Prepare initializes progress messaging prior to Execute.
func (i *Identifier) Prepare(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, i.logger)
	if item.ProgressStage == "" {
		item.ProgressStage = "Identifying"
	}
	item.ProgressMessage = "Fetching metadata"
	item.ProgressPercent = 0

	if i.notifier != nil && strings.TrimSpace(item.SourcePath) == "" {
		title := strings.TrimSpace(item.DiscTitle)
		if title == "" {
			title = "Unknown Disc"
		}
		if err := i.notifier.NotifyDiscDetected(ctx, title, "unknown"); err != nil {
			logger.Warn("disc detected notification failed", zap.Error(err))
		}
	}
	return nil
}

// Execute performs disc scanning and TMDB identification.
func (i *Identifier) Execute(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, i.logger)
	scanResult, err := i.scanDisc(ctx)
	if err != nil {
		return err
	}

	if scanResult.Fingerprint != "" {
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
	if title == "" {
		title = "Unknown Disc"
	}

	response, err := i.searchTMDB(ctx, title)
	if err != nil {
		logger.Warn("tmdb search failed", zap.String("title", title), zap.Error(err))
		i.scheduleReview(ctx, item, "TMDB lookup failed")
		return nil
	}

	best := selectBestResult(title, response, i.cfg)
	if best == nil {
		i.scheduleReview(ctx, item, "No confident TMDB match")
		return nil
	}

	metadata := map[string]any{
		"id":           best.ID,
		"title":        pickTitle(*best),
		"overview":     best.Overview,
		"media_type":   best.MediaType,
		"vote_average": best.VoteAverage,
		"vote_count":   best.VoteCount,
	}
	encodedMetadata, err := json.Marshal(metadata)
	if err != nil {
		return services.Wrap(services.ErrorTransient, "identification", "encode metadata", "Failed to encode TMDB metadata", err)
	}
	item.MetadataJSON = string(encodedMetadata)
	item.ProgressStage = "Identified"
	item.ProgressPercent = 100
	item.ProgressMessage = fmt.Sprintf("Identified as: %s", pickTitle(*best))

	ripSpec := map[string]any{
		"fingerprint": scanResult.Fingerprint,
		"titles":      scanResult.Titles,
		"metadata":    metadata,
	}
	encodedSpec, err := json.Marshal(ripSpec)
	if err != nil {
		return services.Wrap(services.ErrorTransient, "identification", "encode rip spec", "Failed to serialize rip specification", err)
	}
	item.RipSpecData = string(encodedSpec)

	identifiedTitle := pickTitle(*best)
	if i.notifier != nil {
		mediaType := strings.ToLower(strings.TrimSpace(best.MediaType))
		if mediaType == "" {
			mediaType = "unknown"
		}
		if err := i.notifier.NotifyIdentificationComplete(ctx, identifiedTitle, mediaType); err != nil {
			logger.Warn("identification notification failed", zap.Error(err))
		}
	}

	return nil
}

// Rollback performs no additional work on failure.
func (i *Identifier) Rollback(ctx context.Context, item *queue.Item, stageErr error) error {
	return nil
}

var _ workflow.Stage = (*Identifier)(nil)

// HealthCheck verifies identifier dependencies required for successful execution.
func (i *Identifier) HealthCheck(ctx context.Context) workflow.StageHealth {
	name := i.Name()
	if i.cfg == nil {
		return workflow.UnhealthyStage(name, "configuration unavailable")
	}
	if strings.TrimSpace(i.cfg.TMDBAPIKey) == "" {
		return workflow.UnhealthyStage(name, "tmdb api key missing")
	}
	if i.tmdb == nil {
		return workflow.UnhealthyStage(name, "tmdb client unavailable")
	}
	if i.scanner == nil {
		return workflow.UnhealthyStage(name, "disc scanner unavailable")
	}
	return workflow.HealthyStage(name)
}

func (i *Identifier) scanDisc(ctx context.Context) (*disc.ScanResult, error) {
	if i.scanner == nil {
		return nil, services.WithHint(
			services.Wrap(services.ErrorConfiguration, "identification", "initialize scanner", "Disc scanner unavailable", nil),
			"Install MakeMKV and ensure it is in PATH",
		)
	}
	device := strings.TrimSpace(i.cfg.OpticalDrive)
	if device == "" {
		return nil, services.WithHint(
			services.Wrap(services.ErrorConfiguration, "identification", "resolve optical drive", "Optical drive path not configured", nil),
			"Set optical_drive in spindle config to your MakeMKV drive identifier",
		)
	}
	result, err := i.scanner.Scan(ctx, device)
	if err != nil {
		return nil, services.Wrap(services.ErrorExternalTool, "identification", "makemkv scan", "MakeMKV disc scan failed", err)
	}
	return result, nil
}

func (i *Identifier) handleDuplicateFingerprint(ctx context.Context, item *queue.Item) error {
	found, err := i.store.FindByFingerprint(ctx, item.DiscFingerprint)
	if err != nil {
		return services.Wrap(services.ErrorTransient, "identification", "lookup fingerprint", "Failed to query existing disc fingerprint", err)
	}
	if found != nil && found.ID != item.ID {
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
			if err := i.notifier.NotifyUnidentifiedMedia(ctx, label); err != nil {
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

func (i *Identifier) searchTMDB(ctx context.Context, title string) (*tmdb.Response, error) {
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

	resp, err := i.tmdb.SearchMovie(ctx, title)
	if err != nil {
		return nil, err
	}
	i.mu.Lock()
	i.cache[key] = cacheEntry{resp: resp, expires: time.Now().Add(i.cacheTTL)}
	i.mu.Unlock()
	return resp, nil
}

func selectBestResult(query string, response *tmdb.Response, cfg *config.Config) *tmdb.Result {
	if response == nil || len(response.Results) == 0 {
		return nil
	}
	threshold := cfg.TMDBConfidenceThreshold
	queryLower := strings.ToLower(query)
	var best *tmdb.Result
	bestScore := -1.0
	for idx := range response.Results {
		res := response.Results[idx]
		if res.VoteAverage/10 < threshold {
			continue
		}
		score := scoreResult(queryLower, res)
		if score > bestScore {
			best = &response.Results[idx]
			bestScore = score
		}
	}
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

// Rollback is intentionally a no-op today but kept for interface completeness.
