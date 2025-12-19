package subtitles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
	"spindle/internal/services"
	"spindle/internal/subtitles/opensubtitles"
)

var (
	inspectMedia            = ffprobe.Inspect
	defaultHTTPClient       = &http.Client{Timeout: 10 * time.Second}
	errPyannoteUnauthorized = errors.New("pyannote token unauthorized")
)

type openSubtitlesClient interface {
	Search(ctx context.Context, req opensubtitles.SearchRequest) (opensubtitles.SearchResponse, error)
	Download(ctx context.Context, fileID int64, opts opensubtitles.DownloadOptions) (opensubtitles.DownloadResult, error)
}

type commandRunner func(ctx context.Context, name string, args ...string) error
type tokenValidator func(ctx context.Context, token string) (tokenValidationResult, error)

const huggingFaceWhoAmIEndpoint = "https://huggingface.co/api/whoami-v2"

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
		token = strings.TrimSpace(cfg.WhisperXHuggingFaceToken)
	}
	languages := []string{"en"}
	if cfg != nil && len(cfg.OpenSubtitlesLanguages) > 0 {
		languages = append([]string(nil), cfg.OpenSubtitlesLanguages...)
	}
	svc := &Service{
		config:    cfg,
		logger:    serviceLogger,
		run:       defaultCommandRunner,
		hfToken:   token,
		hfCheck:   defaultTokenValidator,
		languages: languages,
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

func (s *Service) ensureReady() error {
	if s == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "init", "Subtitle service unavailable", nil)
	}
	s.readyOnce.Do(func() {
		if s.skipCheck {
			return
		}
		if _, err := exec.LookPath(whisperXCommand); err != nil {
			s.readyErr = services.Wrap(services.ErrConfiguration, "subtitles", "locate whisperx", fmt.Sprintf("Could not find %q on PATH", whisperXCommand), err)
			return
		}
		if _, err := exec.LookPath(ffmpegCommand); err != nil {
			s.readyErr = services.Wrap(services.ErrConfiguration, "subtitles", "locate ffmpeg", fmt.Sprintf("Could not find %q on PATH", ffmpegCommand), err)
			return
		}
	})
	if s.readyErr != nil {
		return s.readyErr
	}
	vadMethod := strings.ToLower(strings.TrimSpace(s.configuredVADMethod()))
	if vadMethod == whisperXVADMethodPyannote && strings.TrimSpace(s.hfToken) == "" {
		return services.Wrap(services.ErrConfiguration, "subtitles", "validate vad", "pyannote VAD selected but no Hugging Face token configured (set whisperx_hf_token)", nil)
	}
	return nil
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
			} else if !s.config.OpenSubtitlesEnabled {
				reason = "opensubtitles_enabled is false"
			} else if strings.TrimSpace(s.config.OpenSubtitlesAPIKey) == "" {
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

func (s *Service) ffprobeBinary() string {
	if s != nil && s.config != nil {
		return s.config.FFprobeBinary()
	}
	return "ffprobe"
}

func (s *Service) buildWhisperXArgs(source, outputDir, language string) []string {
	cudaEnabled := s != nil && s.config != nil && s.config.WhisperXCUDAEnabled

	args := make([]string, 0, 32)
	if cudaEnabled {
		args = append(args,
			"--index-url", whisperXCUDAIndexURL,
			"--extra-index-url", whisperXPypiIndexURL,
		)
	} else {
		args = append(args,
			"--index-url", whisperXPypiIndexURL,
		)
	}

	args = append(args,
		"whisperx",
		source,
		"--model", whisperXModel,
		"--align_model", whisperXAlignModel,
		"--batch_size", whisperXBatchSize,
		"--output_dir", outputDir,
		"--output_format", whisperXOutputFormat,
		"--segment_resolution", whisperXSegmentRes,
		"--chunk_size", whisperXChunkSize,
		"--vad_onset", whisperXVADOnset,
		"--vad_offset", whisperXVADOffset,
		"--beam_size", whisperXBeamSize,
		"--best_of", whisperXBestOf,
		"--temperature", whisperXTemperature,
		"--patience", whisperXPatience,
	)

	vadMethod := s.activeVADMethod()
	args = append(args, "--vad_method", vadMethod)
	if vadMethod == whisperXVADMethodPyannote && s != nil {
		token := strings.TrimSpace(s.hfToken)
		if token != "" {
			args = append(args, "--hf_token", token)
		}
	}

	if lang := normalizeWhisperLanguage(language); lang != "" {
		args = append(args, "--language", lang)
	}
	if cudaEnabled {
		args = append(args, "--device", whisperXCUDADevice)
	} else {
		args = append(args, "--device", whisperXCPUDevice, "--compute_type", whisperXCPUComputeType)
	}
	// Ensure highlight_words is disabled (default false) without passing CLI flag.
	return args
}

func (s *Service) extractPrimaryAudio(ctx context.Context, source string, audioIndex int, destination string) error {
	if audioIndex < 0 {
		return services.Wrap(services.ErrValidation, "subtitles", "extract audio", "Invalid audio track index", nil)
	}
	start := time.Now()
	if s.logger != nil {
		s.logger.Debug("extracting primary audio",
			logging.String("source", source),
			logging.Int("audio_index", audioIndex),
			logging.String("destination", destination),
		)
	}
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-i", source,
		"-map", fmt.Sprintf("0:%d", audioIndex),
		"-vn",
		"-sn",
		"-dn",
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		destination,
	}
	if err := s.run(ctx, ffmpegCommand, args...); err != nil {
		return services.Wrap(services.ErrExternalTool, "subtitles", "extract audio", "Failed to extract primary audio track with ffmpeg", err)
	}
	if s.logger != nil {
		if info, err := os.Stat(destination); err == nil {
			s.logger.Debug("primary audio extracted",
				logging.String("destination", destination),
				logging.Float64("size_mb", float64(info.Size())/1_048_576),
				logging.Duration("elapsed", time.Since(start)),
			)
		} else {
			s.logger.Debug("primary audio extracted",
				logging.String("destination", destination),
				logging.Duration("elapsed", time.Since(start)),
			)
		}
	}
	return nil
}

func (s *Service) formatWithStableTS(ctx context.Context, whisperJSON, outputPath, language string) error {
	if strings.TrimSpace(whisperJSON) == "" {
		return os.ErrNotExist
	}

	tmpPath := outputPath + ".tmp"
	defer os.Remove(tmpPath)

	args := []string{
		"--from", stableTSPackage,
		"python", "-c", stableTSFormatterScript,
		whisperJSON,
		tmpPath,
	}
	if trimmed := strings.TrimSpace(language); trimmed != "" {
		args = append(args, "--language", trimmed)
	}
	if err := s.run(ctx, stableTSCommand, args...); err != nil {
		return services.Wrap(services.ErrExternalTool, "subtitles", "stable_ts_formatter", "StableTS formatter failed", err)
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		return services.Wrap(services.ErrTransient, "subtitles", "finalize stablets formatter", "Failed to finalize formatted subtitles", err)
	}
	return nil
}

func (s *Service) reshapeSubtitles(ctx context.Context, whisperSRT, whisperJSON, outputPath, language string, totalDuration float64) error {
	if err := s.formatWithStableTS(ctx, whisperJSON, outputPath, normalizeWhisperLanguage(language)); err != nil {
		if s.logger != nil {
			s.logger.Warn("stable-ts formatter failed, delivering raw whisper subtitles", logging.Error(err))
		}
		if strings.TrimSpace(whisperSRT) == "" {
			return err
		}
		data, readErr := os.ReadFile(whisperSRT)
		if readErr != nil {
			return services.Wrap(services.ErrTransient, "subtitles", "fallback copy", "Failed to read WhisperX subtitles after Stable-TS failure", readErr)
		}
		if writeErr := os.WriteFile(outputPath, data, 0o644); writeErr != nil {
			return services.Wrap(services.ErrTransient, "subtitles", "fallback copy", "Failed to write WhisperX subtitles after Stable-TS failure", writeErr)
		}
	}
	return nil
}

func (s *Service) ensureTranscriptCache() error {
	if s == nil || s.config == nil {
		return errors.New("subtitle service unavailable")
	}
	s.transcriptCacheOnce.Do(func() {
		dir := strings.TrimSpace(s.config.WhisperXCacheDir)
		if dir == "" {
			s.transcriptCache = nil
			s.transcriptCacheErr = nil
			return
		}
		cache, err := newTranscriptCache(dir, s.logger)
		if err != nil {
			s.transcriptCacheErr = err
			return
		}
		s.transcriptCache = cache
	})
	return s.transcriptCacheErr
}

func (s *Service) tryLoadTranscriptFromCache(plan *generationPlan, req GenerateRequest) (GenerateResult, bool, error) {
	if plan == nil || s == nil || !req.AllowTranscriptCacheRead || strings.TrimSpace(req.TranscriptKey) == "" {
		return GenerateResult{}, false, nil
	}
	if err := s.ensureTranscriptCache(); err != nil {
		return GenerateResult{}, false, err
	}
	if s.transcriptCache == nil {
		return GenerateResult{}, false, nil
	}
	data, meta, ok, err := s.transcriptCache.Load(req.TranscriptKey)
	if err != nil {
		return GenerateResult{}, false, err
	}
	if !ok || len(data) == 0 {
		return GenerateResult{}, false, nil
	}
	if err := os.WriteFile(plan.outputFile, data, 0o644); err != nil {
		return GenerateResult{}, false, err
	}
	segmentCount, err := countSRTCues(plan.outputFile)
	if err != nil {
		return GenerateResult{}, false, err
	}
	if segmentCount == 0 {
		return GenerateResult{}, false, nil
	}
	finalDuration := plan.totalSeconds
	if finalDuration <= 0 {
		if last, err := lastSRTTimestamp(plan.outputFile); err == nil && last > 0 {
			finalDuration = last
		}
	}
	if s.logger != nil {
		s.logger.Debug("whisperx transcript cache hit",
			logging.String("cache_key", req.TranscriptKey),
			logging.Int("segments", segmentCount),
			logging.String("language", strings.TrimSpace(meta.Language)),
		)
	}
	result := GenerateResult{
		SubtitlePath: plan.outputFile,
		SegmentCount: segmentCount,
		Duration:     time.Duration(finalDuration * float64(time.Second)),
		Source:       "whisperx",
	}
	return result, true, nil
}

func (s *Service) tryStoreTranscriptInCache(req GenerateRequest, plan *generationPlan, segmentCount int) {
	if plan == nil || s == nil || !req.AllowTranscriptCacheWrite || strings.TrimSpace(req.TranscriptKey) == "" {
		return
	}
	if err := s.ensureTranscriptCache(); err != nil {
		if s.logger != nil {
			s.logger.Warn("whisperx transcript cache unavailable", logging.Error(err))
		}
		return
	}
	if s.transcriptCache == nil {
		return
	}
	data, err := os.ReadFile(plan.outputFile)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("whisperx transcript cache read failed", logging.Error(err))
		}
		return
	}
	language := strings.TrimSpace(req.Context.Language)
	if language == "" {
		language = plan.language
	}
	if _, err := s.transcriptCache.Store(req.TranscriptKey, language, segmentCount, data); err != nil {
		if s.logger != nil {
			s.logger.Warn("whisperx transcript cache store failed", logging.Error(err))
		}
		return
	}
	if s.logger != nil {
		s.logger.Debug("whisperx transcript cached",
			logging.String("cache_key", req.TranscriptKey),
			logging.Int("segments", segmentCount),
		)
	}
}

func normalizeWhisperLanguage(language string) string {
	lang := strings.TrimSpace(strings.ToLower(language))
	if len(lang) == 2 {
		return lang
	}
	if len(lang) == 3 {
		switch lang {
		case "eng":
			return "en"
		case "spa":
			return "es"
		case "fra":
			return "fr"
		case "ger", "deu":
			return "de"
		case "ita":
			return "it"
		case "por":
			return "pt"
		case "dut", "nld":
			return "nl"
		case "rus":
			return "ru"
		case "jpn":
			return "ja"
		case "kor":
			return "ko"
		}
	}
	return ""
}

func normalizeLanguageList(languages []string) []string {
	if len(languages) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(languages))
	seen := make(map[string]struct{}, len(languages))
	for _, lang := range languages {
		trimmed := strings.ToLower(strings.TrimSpace(lang))
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 2 {
			if mapped := normalizeWhisperLanguage(trimmed); mapped != "" {
				trimmed = mapped
			}
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func defaultCommandRunner(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec
	var stderr strings.Builder
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr

	// Torch 2.6 changed torch.load default to weights_only=true, breaking WhisperX/pyannote.
	// Force legacy behavior so bundled WhisperX binaries can load checkpoints safely.
	if os.Getenv("TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD") == "" {
		cmd.Env = append(os.Environ(), "TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1")
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (s *Service) configuredVADMethod() string {
	if s == nil || s.config == nil {
		return whisperXVADMethodSilero
	}
	method := strings.ToLower(strings.TrimSpace(s.config.WhisperXVADMethod))
	switch method {
	case whisperXVADMethodPyannote, whisperXVADMethodSilero:
		return method
	case "":
		return whisperXVADMethodSilero
	default:
		return whisperXVADMethodSilero
	}
}

func (s *Service) activeVADMethod() string {
	if s == nil {
		return whisperXVADMethodSilero
	}
	if s.vadOverride != "" {
		return s.vadOverride
	}
	return s.configuredVADMethod()
}

func (s *Service) ensureTokenReady(ctx context.Context) error {
	if s == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "token", "Subtitle service unavailable", nil)
	}
	if s.configuredVADMethod() != whisperXVADMethodPyannote {
		return nil
	}
	if strings.TrimSpace(s.hfToken) == "" {
		return services.Wrap(services.ErrConfiguration, "subtitles", "validate vad", "pyannote VAD selected but no Hugging Face token configured (set whisperx_hf_token)", nil)
	}

	s.tokenOnce.Do(func() {
		if s.hfCheck == nil {
			s.tokenErr = services.Wrap(services.ErrConfiguration, "subtitles", "pyannote auth", "Token validator unavailable", nil)
			return
		}
		result, err := s.hfCheck(ctx, s.hfToken)
		if err != nil {
			s.tokenErr = err
			s.vadOverride = whisperXVADMethodSilero
			return
		}
		s.tokenResult = &result
	})

	if s.tokenErr != nil {
		if !s.tokenFallbackLogged && s.logger != nil {
			s.logger.Warn("pyannote authentication failed, falling back to silero", logging.Error(s.tokenErr))
			s.tokenFallbackLogged = true
		}
		return nil
	}

	if !s.tokenSuccessLogged && s.tokenResult != nil && s.logger != nil {
		account := strings.TrimSpace(s.tokenResult.Account)
		if account == "" {
			account = "huggingface"
		}
		s.logger.Info("pyannote authentication verified", logging.String("account", account))
		s.tokenSuccessLogged = true
	}

	return nil
}

func defaultTokenValidator(ctx context.Context, token string) (tokenValidationResult, error) {
	if strings.TrimSpace(token) == "" {
		return tokenValidationResult{}, services.Wrap(services.ErrConfiguration, "subtitles", "pyannote auth", "Empty Hugging Face token", nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, huggingFaceWhoAmIEndpoint, nil)
	if err != nil {
		return tokenValidationResult{}, services.Wrap(services.ErrTransient, "subtitles", "pyannote auth", "Failed to build validation request", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		return tokenValidationResult{}, services.Wrap(services.ErrTransient, "subtitles", "pyannote auth", "Failed to contact Hugging Face", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var payload struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return tokenValidationResult{}, services.Wrap(services.ErrTransient, "subtitles", "pyannote auth", "Failed to parse Hugging Face response", err)
		}
		account := strings.TrimSpace(payload.Name)
		if account == "" {
			account = "huggingface"
		}
		return tokenValidationResult{Account: account}, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		base := services.Wrap(services.ErrValidation, "subtitles", "pyannote auth", fmt.Sprintf("Hugging Face rejected token (%s)", resp.Status), nil)
		return tokenValidationResult{}, fmt.Errorf("%w: %w", errPyannoteUnauthorized, base)
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return tokenValidationResult{}, services.Wrap(services.ErrTransient, "subtitles", "pyannote auth", fmt.Sprintf("Unexpected Hugging Face response: %s", msg), nil)
	}
}

func baseNameWithoutExt(path string) string {
	filename := filepath.Base(strings.TrimSpace(path))
	if filename == "" {
		return "subtitle"
	}
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}

func inferLanguage(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	keys := []string{"language", "LANGUAGE", "lang", "LANG"}
	for _, key := range keys {
		if value, ok := tags[key]; ok {
			value = strings.TrimSpace(strings.ReplaceAll(value, "\u0000", ""))
			if value != "" {
				return strings.ToLower(value)
			}
		}
	}
	return ""
}
