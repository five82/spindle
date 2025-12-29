package subtitles

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/services"
	"spindle/internal/subtitles/opensubtitles"
)

type openSubtitlesClient interface {
	Search(ctx context.Context, req opensubtitles.SearchRequest) (opensubtitles.SearchResponse, error)
	Download(ctx context.Context, fileID int64, opts opensubtitles.DownloadOptions) (opensubtitles.DownloadResult, error)
}

type commandRunner func(ctx context.Context, name string, args ...string) error
type tokenValidator func(ctx context.Context, token string) (tokenValidationResult, error)

type tokenValidationResult struct {
	Account string
}

// GenerateRequest describes the inputs for subtitle generation.
type GenerateRequest struct {
	SourcePath                string
	WorkDir                   string
	OutputDir                 string
	Language                  string
	BaseName                  string
	Context                   SubtitleContext
	Languages                 []string
	ForceAI                   bool
	TranscriptKey             string
	AllowTranscriptCacheRead  bool
	AllowTranscriptCacheWrite bool
}

// GenerateResult reports the generated subtitle file and summary stats.
type GenerateResult struct {
	SubtitlePath string
	SegmentCount int
	Duration     time.Duration
	Source       string // "opensubtitles" or "whisperx"
	// OpenSubtitlesDecision captures what happened with OpenSubtitles during
	// generation. It is intended for observability (logs/UI) so operators can
	// understand when/why WhisperX was used.
	//
	// Values:
	// - "used": a subtitle was downloaded from OpenSubtitles
	// - "no_match": OpenSubtitles lookup succeeded but no suitable match was found
	// - "error": OpenSubtitles lookup failed (see OpenSubtitlesDetail)
	// - "skipped": OpenSubtitles was not attempted (see OpenSubtitlesDetail)
	// - "force_ai": OpenSubtitles was bypassed because ForceAI was true
	OpenSubtitlesDecision string
	OpenSubtitlesDetail   string
}

// Service orchestrates WhisperX execution and Stable-TS formatted subtitle output.
type Service struct {
	config      *config.Config
	logger      *slog.Logger
	run         commandRunner
	hfToken     string
	hfCheck     tokenValidator
	skipCheck   bool
	vadOverride string

	tokenOnce           sync.Once
	tokenErr            error
	tokenResult         *tokenValidationResult
	tokenSuccessLogged  bool
	tokenFallbackLogged bool
	readyOnce           sync.Once
	readyErr            error
	openSubsOnce        sync.Once
	openSubsErr         error
	openSubs            openSubtitlesClient
	openSubsReadyLogged bool
	openSubsCache       *opensubtitles.Cache
	openSubsLastCall    time.Time
	openSubsMu          sync.Mutex
	transcriptCacheOnce sync.Once
	transcriptCacheErr  error
	transcriptCache     *transcriptCache
	languages           []string
}

// ServiceOption customizes a Service.
type ServiceOption func(*Service)

// WithCommandRunner injects a custom command runner (primarily for tests).
func WithCommandRunner(r commandRunner) ServiceOption {
	return func(s *Service) {
		if r != nil {
			s.run = r
		}
	}
}

// WithTokenValidator overrides the Hugging Face token validator (used in tests).
func WithTokenValidator(v tokenValidator) ServiceOption {
	return func(s *Service) {
		if v != nil {
			s.hfCheck = v
		}
	}
}

// WithoutDependencyCheck disables external binary detection (used in tests).
func WithoutDependencyCheck() ServiceOption {
	return func(s *Service) {
		s.skipCheck = true
	}
}

// SetLogger swaps the service logger (used by workflow to route logs into per-item files).
func (s *Service) SetLogger(logger *slog.Logger) {
	if s == nil {
		return
	}
	s.logger = logging.NewComponentLogger(logger, "subtitles")
}

// WithOpenSubtitlesClient injects a custom OpenSubtitles client (used in tests).
func WithOpenSubtitlesClient(client openSubtitlesClient) ServiceOption {
	return func(s *Service) {
		if client != nil {
			s.openSubs = client
		}
	}
}

// NewService constructs a subtitle generation service.
func NewService(cfg *config.Config, logger *slog.Logger, opts ...ServiceOption) *Service {
	serviceLogger := logging.NewComponentLogger(logger, "subtitles")
	token := ""
	if cfg != nil {
		token = strings.TrimSpace(cfg.Subtitles.WhisperXHuggingFace)
	}
	languages := []string{"en"}
	if cfg != nil && len(cfg.Subtitles.OpenSubtitlesLanguages) > 0 {
		languages = append([]string(nil), cfg.Subtitles.OpenSubtitlesLanguages...)
	}
	svc := &Service{
		config:    cfg,
		logger:    serviceLogger,
		run:       nil,
		hfToken:   token,
		hfCheck:   defaultTokenValidator,
		languages: languages,
	}
	for _, opt := range opts {
		opt(svc)
	}
	if svc.run == nil {
		svc.run = svc.defaultCommandRunner
	}
	return svc
}

// Generate produces an SRT file for the provided source.
func (s *Service) Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	if s == nil {
		return GenerateResult{}, services.Wrap(services.ErrConfiguration, "subtitles", "init", "Subtitle service unavailable", nil)
	}

	if len(req.Languages) == 0 {
		req.Languages = append([]string(nil), s.languages...)
	} else {
		req.Languages = normalizeLanguageList(req.Languages)
	}
	if req.Language == "" && len(req.Languages) > 0 {
		req.Language = req.Languages[0]
	}

	if err := s.ensureReady(); err != nil {
		return GenerateResult{}, err
	}

	if err := s.ensureTokenReady(ctx); err != nil {
		return GenerateResult{}, err
	}

	plan, err := s.prepareGenerationPlan(ctx, req)
	if err != nil {
		return GenerateResult{}, err
	}
	if plan.cleanup != nil {
		defer plan.cleanup()
	}

	if req.Context.Language == "" {
		req.Context.Language = plan.language
	}
	if req.Language == "" {
		req.Language = plan.language
	}
	if len(req.Languages) == 0 {
		req.Languages = []string{plan.language}
	}

	if err := s.extractPrimaryAudio(ctx, plan.source, plan.audioIndex, plan.audioPath); err != nil {
		return GenerateResult{}, err
	}

	openSubsDecision := ""
	openSubsDetail := ""
	if !req.ForceAI && s.shouldUseOpenSubtitles() {
		title := strings.TrimSpace(req.Context.Title)
		parentID := req.Context.ParentID()
		episodeID := req.Context.EpisodeID()
		if s.logger != nil {
			s.logger.Debug("attempting opensubtitles fetch",
				logging.String("title", title),
				logging.String("media_type", strings.TrimSpace(req.Context.MediaType)),
				logging.Int64("tmdb_id", req.Context.TMDBID),
				logging.Int64("parent_tmdb_id", parentID),
				logging.Int64("episode_tmdb_id", episodeID),
				logging.String("imdb_id", strings.TrimSpace(req.Context.IMDBID)),
				logging.Int("season", req.Context.Season),
				logging.Int("episode", req.Context.Episode),
				logging.String("languages", strings.Join(req.Languages, ",")),
			)
		}
		if result, ok, err := s.tryOpenSubtitles(ctx, plan, req); err != nil {
			openSubsDecision = "error"
			openSubsDetail = strings.TrimSpace(err.Error())
			var suspect suspectMisIdentificationError
			if errors.As(err, &suspect) {
				if s.logger != nil {
					s.logger.Warn("opensubtitles suggests mis-identification",
						logging.Float64("median_delta_seconds", suspect.medianAbsDelta()),
						logging.String("title", title),
						logging.Int("season", req.Context.Season),
						logging.Int("episode", req.Context.Episode),
						logging.Alert("review"),
					)
				}
				return GenerateResult{}, err
			}
			if s.logger != nil {
				s.logger.Warn("opensubtitles fetch failed",
					logging.Error(err),
					logging.String("title", title),
					logging.Int64("tmdb_id", req.Context.TMDBID),
					logging.Int64("parent_tmdb_id", parentID),
					logging.Int64("episode_tmdb_id", episodeID),
					logging.Int("season", req.Context.Season),
					logging.Int("episode", req.Context.Episode),
					logging.Alert("subtitle_fallback"),
				)
			}
		} else if ok {
			result.Source = "opensubtitles"
			result.OpenSubtitlesDecision = "used"
			if s.logger != nil {
				s.logger.Debug("using opensubtitles subtitles",
					logging.String("subtitle_path", result.SubtitlePath),
					logging.Int("segment_count", result.SegmentCount),
					logging.String("subtitle_source", result.Source),
				)
			}
			return result, nil
		} else {
			openSubsDecision = "no_match"
			openSubsDetail = "no suitable match found"
			if s.logger != nil {
				s.logger.Warn("opensubtitles match not found, falling back to whisperx",
					logging.String("title", title),
					logging.Int64("tmdb_id", req.Context.TMDBID),
					logging.Int64("parent_tmdb_id", parentID),
					logging.Int64("episode_tmdb_id", episodeID),
					logging.Int("season", req.Context.Season),
					logging.Int("episode", req.Context.Episode),
					logging.String("languages", strings.Join(req.Languages, ",")),
					logging.Alert("subtitle_fallback"),
				)
			}
		}
	} else {
		reason := "forceai flag enabled"
		if req.ForceAI {
			openSubsDecision = "force_ai"
			openSubsDetail = reason
		} else {
			openSubsDecision = "skipped"
			reason = "opensubtitles disabled"
			if s.config == nil {
				reason = "configuration unavailable"
			} else if !s.config.Subtitles.OpenSubtitlesEnabled {
				reason = "opensubtitles_enabled is false"
			} else if strings.TrimSpace(s.config.Subtitles.OpenSubtitlesAPIKey) == "" {
				reason = "opensubtitles_api_key not set"
			}
			openSubsDetail = reason
		}
		if s.logger != nil {
			s.logger.Debug("opensubtitles download skipped", logging.String("reason", reason))
		}
	}

	if req.AllowTranscriptCacheRead {
		if cached, ok, err := s.tryLoadTranscriptFromCache(plan, req); err != nil {
			if s.logger != nil {
				s.logger.Warn("transcript cache load failed", logging.Error(err))
			}
		} else if ok {
			cached.OpenSubtitlesDecision = openSubsDecision
			cached.OpenSubtitlesDetail = openSubsDetail
			return cached, nil
		}
	}

	if err := s.invokeWhisperX(ctx, plan); err != nil {
		return GenerateResult{}, err
	}

	if err := s.reshapeSubtitles(ctx, plan.whisperSRT, plan.whisperJSON, plan.outputFile, plan.language, plan.totalSeconds); err != nil {
		return GenerateResult{}, err
	}

	segmentCount, err := countSRTCues(plan.outputFile)
	if err != nil {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "analyze srt", "Failed to inspect formatted subtitles", err)
	}
	if segmentCount == 0 {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "format", "Subtitle formatter produced no cues", nil)
	}

	finalDuration := plan.totalSeconds
	if finalDuration <= 0 {
		if last, err := lastSRTTimestamp(plan.outputFile); err == nil && last > 0 {
			finalDuration = last
		}
	}

	if s.logger != nil {
		s.logger.Debug("subtitle generation complete",
			logging.String("output", plan.outputFile),
			logging.Int("segments", segmentCount),
			logging.Float64("duration_seconds", finalDuration),
			logging.String("subtitle_source", "whisperx"),
			logging.String("opensubtitles_decision", openSubsDecision),
		)
	}

	s.tryStoreTranscriptInCache(req, plan, segmentCount)

	result := GenerateResult{
		SubtitlePath:          plan.outputFile,
		SegmentCount:          segmentCount,
		Duration:              time.Duration(finalDuration * float64(time.Second)),
		Source:                "whisperx",
		OpenSubtitlesDecision: openSubsDecision,
		OpenSubtitlesDetail:   openSubsDetail,
	}
	return result, nil
}
