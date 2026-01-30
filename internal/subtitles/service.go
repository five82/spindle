package subtitles

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/deps"
	"spindle/internal/logging"
	"spindle/internal/services"
	"spindle/internal/services/whisperx"
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
	OpenSubtitlesOnly         bool // Fail if OpenSubtitles doesn't produce a match (no WhisperX fallback)
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
	whisperxSvc *whisperx.Service

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

// WithWhisperXService injects a custom WhisperX service (used in tests).
func WithWhisperXService(svc *whisperx.Service) ServiceOption {
	return func(s *Service) {
		if svc != nil {
			s.whisperxSvc = svc
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
	customRunner := svc.run != nil
	if svc.run == nil {
		svc.run = svc.defaultCommandRunner
	}
	if svc.whisperxSvc == nil && cfg != nil {
		whisperxCfg := whisperx.Config{
			Model:       cfg.Subtitles.WhisperXModel,
			CUDAEnabled: cfg.Subtitles.WhisperXCUDAEnabled,
			VADMethod:   cfg.Subtitles.WhisperXVADMethod,
			HFToken:     cfg.Subtitles.WhisperXHuggingFace,
		}
		svc.whisperxSvc = whisperx.NewService(whisperxCfg, deps.ResolveFFmpegPath())
	}
	// If a custom command runner was provided (e.g., for tests), configure the
	// whisperx service to use it too so extraction and transcription are stubbed.
	if customRunner && svc.whisperxSvc != nil {
		svc.whisperxSvc.WithCommandRunner(svc.run)
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

	attempt := s.attemptOpenSubtitles(ctx, plan, req)
	if attempt.err != nil {
		return GenerateResult{}, attempt.err
	}
	if attempt.result != nil {
		return *attempt.result, nil
	}
	openSubsDecision := attempt.decision
	openSubsDetail := attempt.detail

	if req.AllowTranscriptCacheRead {
		if cached, ok, err := s.tryLoadTranscriptFromCache(plan, req); err != nil {
			if s.logger != nil {
				s.logger.Warn("transcript cache load failed; regenerating subtitles",
					logging.Error(err),
					logging.String(logging.FieldEventType, "transcript_cache_load_failed"),
					logging.String(logging.FieldErrorHint, "check whisperx_cache_dir permissions"),
				)
			}
		} else if ok {
			if s.logger != nil {
				s.logger.Info("transcript cache decision",
					logging.String(logging.FieldDecisionType, "transcript_cache"),
					logging.String("decision_result", "hit"),
					logging.String("decision_reason", "valid_cached_transcript_found"),
					logging.String("transcript_key", req.TranscriptKey),
				)
			}
			cached.OpenSubtitlesDecision = openSubsDecision
			cached.OpenSubtitlesDetail = openSubsDetail
			return cached, nil
		} else if s.logger != nil {
			s.logger.Info("transcript cache decision",
				logging.String(logging.FieldDecisionType, "transcript_cache"),
				logging.String("decision_result", "miss"),
				logging.String("decision_reason", "no_cached_transcript_found"),
				logging.String("transcript_key", req.TranscriptKey),
			)
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
		s.logger.Info("subtitle source selected",
			logging.String(logging.FieldEventType, "subtitle_source_selected"),
			logging.String(logging.FieldDecisionType, "subtitle_source"),
			logging.String("decision_result", "whisperx"),
			logging.String("decision_reason", openSubsDecision),
			logging.String("subtitle_file", plan.outputFile),
			logging.Int("segment_count", segmentCount),
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

// openSubtitlesAttempt captures the outcome of trying OpenSubtitles.
type openSubtitlesAttempt struct {
	decision string          // "used", "no_match", "error", "duration_mismatch", "skipped", "force_ai"
	detail   string          // human-readable explanation
	result   *GenerateResult // non-nil when decision is "used"
	err      error           // non-nil only when OpenSubtitlesOnly is true and lookup failed
}

// attemptOpenSubtitles encapsulates all OpenSubtitles decision logic.
func (s *Service) attemptOpenSubtitles(ctx context.Context, plan *generationPlan, req GenerateRequest) openSubtitlesAttempt {
	// Validate flag combinations
	if req.OpenSubtitlesOnly && !s.shouldUseOpenSubtitles() {
		return openSubtitlesAttempt{
			decision: "error",
			detail:   "--opensubtitles-only requires OpenSubtitles to be enabled and configured",
			err: services.Wrap(services.ErrConfiguration, "subtitles", "opensubtitles",
				"--opensubtitles-only requires OpenSubtitles to be enabled and configured", nil),
		}
	}
	if req.OpenSubtitlesOnly && req.ForceAI {
		return openSubtitlesAttempt{
			decision: "error",
			detail:   "--opensubtitles-only and --forceai are mutually exclusive",
			err: services.Wrap(services.ErrConfiguration, "subtitles", "flags",
				"--opensubtitles-only and --forceai are mutually exclusive", nil),
		}
	}

	// ForceAI bypasses OpenSubtitles
	if req.ForceAI {
		if s.logger != nil {
			s.logger.Debug("opensubtitles download skipped", logging.String("reason", "forceai flag enabled"))
		}
		return openSubtitlesAttempt{decision: "force_ai", detail: "forceai flag enabled"}
	}

	// OpenSubtitles not configured
	if !s.shouldUseOpenSubtitles() {
		reason := s.openSubtitlesDisabledReason()
		if s.logger != nil {
			s.logger.Debug("opensubtitles download skipped", logging.String("reason", reason))
		}
		return openSubtitlesAttempt{decision: "skipped", detail: reason}
	}

	// Attempt OpenSubtitles lookup
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

	result, ok, err := s.tryOpenSubtitles(ctx, plan, req)
	if err != nil {
		return s.handleOpenSubtitlesError(err, req, title)
	}
	if ok {
		result.Source = "opensubtitles"
		result.OpenSubtitlesDecision = "used"
		if s.logger != nil {
			s.logger.Info("subtitle source selected",
				logging.String(logging.FieldEventType, "subtitle_source_selected"),
				logging.String(logging.FieldDecisionType, "subtitle_source"),
				logging.String("decision_result", "opensubtitles"),
				logging.String("subtitle_file", result.SubtitlePath),
				logging.Int("segment_count", result.SegmentCount),
			)
		}
		return openSubtitlesAttempt{decision: "used", result: &result}
	}

	// No match found
	if req.OpenSubtitlesOnly {
		return openSubtitlesAttempt{
			decision: "no_match",
			detail:   "no suitable match found",
			err: services.Wrap(services.ErrTransient, "subtitles", "opensubtitles",
				"no suitable subtitle match found on OpenSubtitles", nil),
		}
	}
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
			logging.String(logging.FieldEventType, "opensubtitles_no_match"),
			logging.String(logging.FieldErrorHint, "verify title/season/episode metadata or use --forceai"),
		)
	}
	return openSubtitlesAttempt{decision: "no_match", detail: "no suitable match found"}
}

// handleOpenSubtitlesError processes errors from tryOpenSubtitles.
func (s *Service) handleOpenSubtitlesError(err error, req GenerateRequest, title string) openSubtitlesAttempt {
	var suspect suspectMisIdentificationError
	if errors.As(err, &suspect) {
		detail := fmt.Sprintf("all candidates rejected (median delta %.0fs)", suspect.medianAbsDelta())
		if req.OpenSubtitlesOnly {
			return openSubtitlesAttempt{
				decision: "duration_mismatch",
				detail:   detail,
				err: services.Wrap(services.ErrTransient, "subtitles", "opensubtitles",
					fmt.Sprintf("all candidates rejected due to duration mismatch (median delta %.0fs)", suspect.medianAbsDelta()), err),
			}
		}
		if s.logger != nil {
			s.logger.Warn("opensubtitles duration mismatch, falling back to whisperx",
				logging.Float64("median_delta_seconds", suspect.medianAbsDelta()),
				logging.String("title", title),
				logging.Int("season", req.Context.Season),
				logging.Int("episode", req.Context.Episode),
				logging.Alert("subtitle_fallback"),
				logging.String(logging.FieldEventType, "opensubtitles_duration_mismatch"),
				logging.String(logging.FieldErrorHint, "video may be extended/UHD cut with different runtime"),
			)
		}
		return openSubtitlesAttempt{decision: "duration_mismatch", detail: detail}
	}

	// Generic error
	detail := strings.TrimSpace(err.Error())
	if req.OpenSubtitlesOnly {
		return openSubtitlesAttempt{
			decision: "error",
			detail:   detail,
			err:      services.Wrap(services.ErrTransient, "subtitles", "opensubtitles", "all candidates rejected", err),
		}
	}
	if s.logger != nil {
		s.logger.Warn("opensubtitles candidates rejected, falling back to whisperx",
			logging.Error(err),
			logging.String("title", title),
			logging.Int64("tmdb_id", req.Context.TMDBID),
			logging.Int("season", req.Context.Season),
			logging.Int("episode", req.Context.Episode),
			logging.Alert("subtitle_fallback"),
			logging.String(logging.FieldEventType, "opensubtitles_candidates_rejected"),
			logging.String(logging.FieldErrorHint, "see candidate summary above for rejection details"),
		)
	}
	return openSubtitlesAttempt{decision: "error", detail: detail}
}

// openSubtitlesDisabledReason returns why OpenSubtitles is not available.
func (s *Service) openSubtitlesDisabledReason() string {
	if s.config == nil {
		return "configuration unavailable"
	}
	if !s.config.Subtitles.OpenSubtitlesEnabled {
		return "opensubtitles_enabled is false"
	}
	if strings.TrimSpace(s.config.Subtitles.OpenSubtitlesAPIKey) == "" {
		return "opensubtitles_api_key not set"
	}
	return "opensubtitles disabled"
}
