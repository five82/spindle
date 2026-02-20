package subtitles

import (
	"context"
	"strings"
	"sync"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/deps"
	langpkg "spindle/internal/language"
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
	SourcePath  string
	WorkDir     string
	OutputDir   string
	Language    string
	BaseName    string
	Context     SubtitleContext
	Languages   []string
	FetchForced bool // Also search for forced (foreign-parts-only) subtitles on OpenSubtitles
}

// GenerateResult reports the generated subtitle file and summary stats.
type GenerateResult struct {
	SubtitlePath       string
	ForcedSubtitlePath string // Path to forced (foreign-parts-only) subtitle, if fetched
	SegmentCount       int
	Duration           time.Duration
	Source             string // always "whisperx"
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
		req.Languages = langpkg.NormalizeList(req.Languages)
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

	if err := s.invokeWhisperX(ctx, plan); err != nil {
		return GenerateResult{}, err
	}

	if err := s.reshapeSubtitles(ctx, plan.whisperSRT, plan.whisperJSON, plan.outputFile, plan.language); err != nil {
		return GenerateResult{}, err
	}

	// Filter WhisperX hallucinations and credits noise.
	// filterTranscriptionOutput returns the final cue count so we can skip a
	// separate file read when filtering succeeds.
	segmentCount, filterErr := s.filterTranscriptionOutput(plan.outputFile, plan.totalSeconds)
	if filterErr != nil {
		if s.logger != nil {
			s.logger.Warn("whisperx post-filter failed, keeping unfiltered output",
				logging.Error(filterErr),
				logging.String(logging.FieldEventType, "whisperx_filter_failed"),
			)
		}
		// Fall back to counting from the unfiltered file.
		segmentCount, err = countSRTCues(plan.outputFile)
		if err != nil {
			return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "analyze srt", "Failed to inspect formatted subtitles", err)
		}
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
			logging.String("decision_reason", "whisperx_only"),
			logging.String("subtitle_file", plan.outputFile),
			logging.Int("segment_count", segmentCount),
		)
	}

	result := GenerateResult{
		SubtitlePath: plan.outputFile,
		SegmentCount: segmentCount,
		Duration:     time.Duration(finalDuration * float64(time.Second)),
		Source:       "whisperx",
	}
	s.tryAttachForcedSubtitles(ctx, plan, req, &result)
	return result, nil
}

// tryAttachForcedSubtitles searches for forced subtitles if FetchForced is set.
func (s *Service) tryAttachForcedSubtitles(ctx context.Context, plan *generationPlan, req GenerateRequest, result *GenerateResult) {
	if !req.FetchForced {
		return
	}
	if !s.shouldUseOpenSubtitles() {
		if s.logger != nil {
			s.logger.Info("forced subtitle decision",
				logging.String(logging.FieldDecisionType, "forced_subtitle_fetch"),
				logging.String("decision_result", "skipped"),
				logging.String("decision_reason", s.openSubtitlesDisabledReason()),
			)
		}
		return
	}
	baseName := plan.outputFile[:len(plan.outputFile)-len(".srt")]
	// Use the aligned regular subtitle as reference for forced subtitle alignment.
	// Forced subtitles are sparse (few cues) and audio-based alignment often fails.
	// Subtitle-to-subtitle alignment is more reliable for sparse content.
	referenceSubtitle := result.SubtitlePath
	forcedPath, err := s.tryForcedSubtitles(ctx, plan, req, baseName, referenceSubtitle)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("forced subtitle fetch failed",
				logging.Error(err),
				logging.String(logging.FieldEventType, "forced_subtitle_fetch_failed"),
			)
		}
		return
	}
	if forcedPath != "" {
		result.ForcedSubtitlePath = forcedPath
		if s.logger != nil {
			s.logger.Info("forced subtitle decision",
				logging.String(logging.FieldDecisionType, "forced_subtitle_fetch"),
				logging.String("decision_result", "downloaded"),
				logging.String("decision_reason", "match_found"),
				logging.String("forced_subtitle_file", forcedPath),
			)
		}
	} else if s.logger != nil {
		s.logger.Info("forced subtitle decision",
			logging.String(logging.FieldDecisionType, "forced_subtitle_fetch"),
			logging.String("decision_result", "not_found"),
			logging.String("decision_reason", "no_match_on_opensubtitles"),
		)
	}
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
