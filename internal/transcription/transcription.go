// Package transcription provides shared canonical transcription with caching.
package transcription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/five82/spindle/internal/fileutil"
	internallang "github.com/five82/spindle/internal/language"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/srtutil"
)

const (
	defaultEngine    = "parakeet"
	defaultModel     = "nvidia/parakeet-tdt-0.6b-v2"
	defaultDevice    = "auto"
	defaultPrecision = "bf16"
)

// Config defines transcription service settings.
type Config struct {
	Engine    string
	Model     string
	Device    string
	Precision string
	CacheDir  string
	Logger    *slog.Logger
}

// Service provides canonical transcription with caching.
type Service struct {
	engine    string
	model     string
	device    string
	precision string
	cacheDir  string
	logger    *slog.Logger
	runtime   *runtimeEnv
}

// New creates a transcription service.
func New(cfg Config) *Service {
	logger := logs.Default(cfg.Logger)
	engine := strings.ToLower(strings.TrimSpace(cfg.Engine))
	if engine == "" {
		engine = defaultEngine
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = defaultModel
	}
	device := strings.ToLower(strings.TrimSpace(cfg.Device))
	if device == "" {
		device = defaultDevice
	}
	precision := strings.ToLower(strings.TrimSpace(cfg.Precision))
	if precision == "" {
		precision = defaultPrecision
	}
	return &Service{
		engine:    engine,
		model:     model,
		device:    device,
		precision: precision,
		cacheDir:  cfg.CacheDir,
		logger:    logger,
		runtime:   newRuntimeEnv(cfg.CacheDir, engine),
	}
}

// TranscribeRequest specifies what to transcribe.
type TranscribeRequest struct {
	InputPath  string
	AudioIndex int // audio-relative index (maps to ffmpeg 0:a:N)
	Language   string
	OutputDir  string
	Model      string // Override default model
	ContentKey string // Content-stable cache identity
}

// Phase identifies a transcription progress phase.
type Phase string

const (
	PhaseExtract    Phase = "extract"
	PhaseTranscribe Phase = "transcribe"
)

// ProgressFunc is called at phase boundaries during transcription.
// phase is the current transcription phase. elapsed is zero at phase start
// and non-zero at phase end.
type ProgressFunc func(phase Phase, elapsed time.Duration)

// TranscribeResult contains canonical transcription output.
type TranscribeResult struct {
	SRTPath        string
	JSONPath       string
	Duration       float64 // total SRT duration (seconds) from last cue timestamp
	Segments       int
	Cached         bool
	ExtractTime    time.Duration // time spent on ffmpeg audio extraction
	TranscribeTime time.Duration // time spent on Parakeet transcription
}

// Transcribe runs canonical transcription with caching.
//
// Steps:
//  1. Compute cache key.
//  2. Check cache. If hit, return cached result.
//  3. Extract audio via FFmpeg.
//  4. Run the configured transcription engine.
//  5. Read SRT, count segments, parse duration.
//  6. Store in cache.
//  7. Return result.
//
// If progress is non-nil, it is called at the start and end of each phase.
func (s *Service) Transcribe(ctx context.Context, req TranscribeRequest, progress ...ProgressFunc) (*TranscribeResult, error) {
	var onProgress ProgressFunc
	if len(progress) > 0 {
		onProgress = progress[0]
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = s.model
	}
	language := normalizeLanguage(req.Language)
	if err := s.validateLanguage(language); err != nil {
		return nil, err
	}

	key := cacheKey(req, s.engine, model, language)

	if result, ok := s.Lookup(key); ok {
		s.logger.Info("transcription cache hit",
			"decision_type", logs.DecisionTranscriptionCache,
			"decision_result", "hit",
			"decision_reason", fmt.Sprintf("engine=%s key=%s segments=%d", s.engine, key[:12], result.Segments),
		)
		result.Cached = true
		return result, nil
	}
	s.logger.Info("transcription cache miss",
		"decision_type", logs.DecisionTranscriptionCache,
		"decision_result", "miss",
		"decision_reason", fmt.Sprintf("engine=%s key=%s", s.engine, key[:12]),
	)

	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	if onProgress != nil {
		onProgress(PhaseExtract, 0)
	}
	wavPath := filepath.Join(req.OutputDir, "audio.wav")
	ffmpegArgs := []string{
		"-i", req.InputPath,
		"-map", fmt.Sprintf("0:a:%d", req.AudioIndex),
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		"-vn", "-sn", "-dn",
		"-y",
		wavPath,
	}

	s.logger.Info("extracting audio for transcription",
		"event_type", "transcription_extract",
		"input", req.InputPath,
		"audio_index", req.AudioIndex,
	)
	extractStart := time.Now()
	ffmpegCmd := exec.CommandContext(ctx, "ffmpeg", ffmpegArgs...)
	if output, err := ffmpegCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg audio extraction: %w: %s", err, output)
	}
	extractTime := time.Since(extractStart)
	if onProgress != nil {
		onProgress(PhaseExtract, extractTime)
	}

	if onProgress != nil {
		onProgress(PhaseTranscribe, 0)
	}
	pythonPath, helperPath, err := s.ensureRuntime(ctx)
	if err != nil {
		return nil, err
	}
	args := []string{
		helperPath,
		"--input", wavPath,
		"--output-dir", req.OutputDir,
		"--model", model,
		"--device", s.device,
		"--dtype", s.precision,
		"--language", language,
	}

	s.logger.Info("running Parakeet transcription",
		"event_type", "transcription_parakeet",
		"engine", s.engine,
		"model", model,
		"device", s.device,
		"precision", s.precision,
		"language", language,
	)
	transcribeStart := time.Now()
	output, err := runCommand(ctx, pythonPath, args, s.runtimeEnv())
	if err != nil {
		return nil, fmt.Errorf("parakeet transcription: %w: %s", err, strings.TrimSpace(output))
	}
	if reason := prefixedOutputLine(output, "precision_fallback:"); reason != "" {
		s.logger.Info("transcription precision adjusted",
			"decision_type", logs.DecisionTranscriptionPrecision,
			"decision_result", "fallback_to_fp32",
			"decision_reason", strings.TrimSpace(reason),
			"requested_precision", s.precision,
			"effective_precision", "fp32",
		)
	}
	transcribeTime := time.Since(transcribeStart)
	if onProgress != nil {
		onProgress(PhaseTranscribe, transcribeTime)
	}

	srtPath := filepath.Join(req.OutputDir, "audio.srt")
	if _, err := os.Stat(srtPath); err != nil {
		return nil, fmt.Errorf("srt output not found at %s: %w", srtPath, err)
	}
	jsonPath := filepath.Join(req.OutputDir, "audio.json")
	if _, err := os.Stat(jsonPath); err != nil {
		return nil, fmt.Errorf("json output not found at %s: %w", jsonPath, err)
	}

	segments, duration, err := analyzeSRT(srtPath)
	if err != nil {
		return nil, fmt.Errorf("analyze srt: %w", err)
	}

	result := &TranscribeResult{
		SRTPath:        srtPath,
		JSONPath:       jsonPath,
		Duration:       duration,
		Segments:       segments,
		Cached:         false,
		ExtractTime:    extractTime,
		TranscribeTime: transcribeTime,
	}
	if err := s.Store(key, result); err != nil {
		return nil, fmt.Errorf("cache store: %w", err)
	}
	s.logger.Debug("transcription result cached", "key", key[:12], "segments", result.Segments)
	return result, nil
}

// cacheKey computes a deterministic cache key for a transcription request.
// If ContentKey is non-empty, it is used for content-stable caching.
// Otherwise, the input path and audio index are used as a fallback.
func cacheKey(req TranscribeRequest, engine, model, language string) string {
	var input string
	if req.ContentKey != "" {
		input = engine + "\x00" + req.ContentKey + "\x00" + model + "\x00" + language
	} else {
		input = engine + "\x00" + req.InputPath + "\x00" + strconv.Itoa(req.AudioIndex) + "\x00" + model + "\x00" + language
	}
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])
}

// Lookup checks the cache for a previously stored canonical transcription result.
// It returns the result and true if valid cached SRT and JSON artifacts exist
// and the SRT has > 0 cues.
func (s *Service) Lookup(key string) (*TranscribeResult, bool) {
	dir := filepath.Join(s.cacheDir, key)
	srtPath, jsonPath, ok := cacheArtifactPaths(dir)
	if !ok {
		return nil, false
	}
	segments, duration, err := analyzeSRT(srtPath)
	if err != nil || segments == 0 {
		return nil, false
	}
	return &TranscribeResult{
		SRTPath:  srtPath,
		JSONPath: jsonPath,
		Duration: duration,
		Segments: segments,
	}, true
}

// Store copies canonical transcription artifacts into the cache directory under
// the given key.
func (s *Service) Store(key string, result *TranscribeResult) error {
	dir := filepath.Join(s.cacheDir, key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	if err := copyCacheArtifact(result.SRTPath, dir, "srt"); err != nil {
		return err
	}
	if err := copyCacheArtifact(result.JSONPath, dir, "json"); err != nil {
		return err
	}
	return nil
}

func prefixedOutputLine(output, prefix string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func cacheArtifactPaths(dir string) (srtPath, jsonPath string, ok bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		switch {
		case strings.HasSuffix(entry.Name(), ".srt"):
			srtPath = path
		case strings.HasSuffix(entry.Name(), ".json"):
			jsonPath = path
		}
	}
	if srtPath == "" || jsonPath == "" {
		return "", "", false
	}
	return srtPath, jsonPath, true
}

func copyCacheArtifact(srcPath, dir, kind string) error {
	if strings.TrimSpace(srcPath) == "" {
		return fmt.Errorf("missing %s artifact path", kind)
	}
	dst := filepath.Join(dir, filepath.Base(srcPath))
	if err := fileutil.CopyFile(srcPath, dst); err != nil {
		return fmt.Errorf("cache %s artifact: %w", kind, err)
	}
	return nil
}

// analyzeSRT reads an SRT file once and returns both the segment count and
// the duration (end timestamp of the last cue, in seconds).
func analyzeSRT(path string) (segments int, duration float64, err error) {
	cues, err := srtutil.ParseFile(path)
	if err != nil {
		return 0, 0, err
	}
	if len(cues) == 0 {
		return 0, 0, nil
	}
	return len(cues), cues[len(cues)-1].End, nil
}

func normalizeLanguage(value string) string {
	lang := internallang.ToISO2(strings.TrimSpace(value))
	if lang == "" {
		return "en"
	}
	return lang
}

func (s *Service) validateLanguage(language string) error {
	switch s.engine {
	case defaultEngine:
		if language != "en" {
			return fmt.Errorf("parakeet-tdt-0.6b-v2 only supports English audio (got language=%s)", language)
		}
		return nil
	default:
		return fmt.Errorf("unsupported transcription engine %q", s.engine)
	}
}

// Config returns the service configuration for display purposes.
func (s *Service) Config() (engine, model, device, precision string) {
	return s.engine, s.model, s.device, s.precision
}
