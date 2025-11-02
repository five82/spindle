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
)

var (
	inspectMedia            = ffprobe.Inspect
	defaultHTTPClient       = &http.Client{Timeout: 10 * time.Second}
	errPyannoteUnauthorized = errors.New("pyannote token unauthorized")
)

type commandRunner func(ctx context.Context, name string, args ...string) error
type tokenValidator func(ctx context.Context, token string) (tokenValidationResult, error)

const huggingFaceWhoAmIEndpoint = "https://huggingface.co/api/whoami-v2"

type tokenValidationResult struct {
	Account string
}

// GenerateRequest describes the inputs for subtitle generation.
type GenerateRequest struct {
	SourcePath string
	WorkDir    string
	OutputDir  string
	Language   string
	BaseName   string
}

// GenerateResult reports the generated subtitle file and summary stats.
type GenerateResult struct {
	SubtitlePath string
	SegmentCount int
	Duration     time.Duration
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

// NewService constructs a subtitle generation service.
func NewService(cfg *config.Config, logger *slog.Logger, opts ...ServiceOption) *Service {
	serviceLogger := logger
	if serviceLogger != nil {
		serviceLogger = serviceLogger.With(logging.String("component", "subtitles"))
	}
	token := ""
	if cfg != nil {
		token = strings.TrimSpace(cfg.WhisperXHuggingFaceToken)
	}
	svc := &Service{
		config:  cfg,
		logger:  serviceLogger,
		run:     defaultCommandRunner,
		hfToken: token,
		hfCheck: defaultTokenValidator,
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

	if err := s.extractPrimaryAudio(ctx, plan.source, plan.audioIndex, plan.audioPath); err != nil {
		return GenerateResult{}, err
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
		s.logger.Info("subtitle generation complete",
			logging.String("output", plan.outputFile),
			logging.Int("segments", segmentCount),
			logging.Float64("duration_seconds", finalDuration),
		)
	}

	result := GenerateResult{
		SubtitlePath: plan.outputFile,
		SegmentCount: segmentCount,
		Duration:     time.Duration(finalDuration * float64(time.Second)),
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

func defaultCommandRunner(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec
	var stderr strings.Builder
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
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
