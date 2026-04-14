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
	"sync"
	"time"

	"github.com/five82/spindle/internal/fileutil"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/srtutil"
)

// Options configures the transcription service.
type Options struct {
	ASRModel              string
	ForcedAlignerModel    string
	Device                string
	DType                 string
	UseFlashAttention     bool
	MaxInferenceBatchSize int
	CacheDir              string
	RuntimeDir            string
}

// Service provides shared Qwen3-ASR transcription with caching.
type Service struct {
	opts     Options
	cacheDir string
	runtime  *Runtime
	worker   workerClient
	logger   *slog.Logger
	mu       sync.Mutex
}

// New creates a transcription service.
func New(opts Options, logger *slog.Logger) *Service {
	logger = logs.Default(logger)
	if opts.ASRModel == "" {
		opts.ASRModel = "Qwen/Qwen3-ASR-1.7B"
	}
	if opts.ForcedAlignerModel == "" {
		opts.ForcedAlignerModel = "Qwen/Qwen3-ForcedAligner-0.6B"
	}
	if opts.Device == "" {
		opts.Device = "cuda:0"
	}
	if opts.DType == "" {
		opts.DType = "bfloat16"
	}
	if opts.MaxInferenceBatchSize <= 0 {
		opts.MaxInferenceBatchSize = 1
	}
	service := &Service{
		opts:     opts,
		cacheDir: opts.CacheDir,
		runtime:  newRuntime(opts.RuntimeDir, logger),
		logger:   logger,
	}
	service.worker = newSubprocessWorker(service.runtime, workerConfig{
		ASRModel:              opts.ASRModel,
		ForcedAlignerModel:    opts.ForcedAlignerModel,
		Device:                opts.Device,
		DType:                 opts.DType,
		UseFlashAttention:     opts.UseFlashAttention,
		MaxInferenceBatchSize: opts.MaxInferenceBatchSize,
	}, logger)
	return service
}

// TranscribeRequest specifies what to transcribe.
type TranscribeRequest struct {
	InputPath        string
	AudioIndex       int // audio-relative index (maps to ffmpeg 0:a:N)
	Language         string
	OutputDir        string
	Model            string // Optional ASR model override
	ContentKey       string // Content-stable cache identity
	RequireAlignment bool   // Fail when aligned subtitle timestamps are unavailable
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
	TranscribeTime time.Duration // time spent on Qwen3-ASR
}

// RuntimeStatus reports transcription runtime health.
func (s *Service) RuntimeStatus(ctx context.Context) (*RuntimeStatus, error) {
	return s.runtime.HealthCheck(ctx, workerConfig{
		ASRModel:              s.opts.ASRModel,
		ForcedAlignerModel:    s.opts.ForcedAlignerModel,
		Device:                s.opts.Device,
		DType:                 s.opts.DType,
		UseFlashAttention:     s.opts.UseFlashAttention,
		MaxInferenceBatchSize: s.opts.MaxInferenceBatchSize,
	})
}

// Close stops the managed transcription worker.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.worker == nil {
		return nil
	}
	return s.worker.Close()
}

// Transcribe runs Qwen3-ASR transcription with caching.
func (s *Service) Transcribe(ctx context.Context, req TranscribeRequest, progress ...ProgressFunc) (*TranscribeResult, error) {
	var onProgress ProgressFunc
	if len(progress) > 0 {
		onProgress = progress[0]
	}

	model := req.Model
	if model == "" {
		model = s.opts.ASRModel
	}
	if model != s.opts.ASRModel {
		return nil, fmt.Errorf("per-request ASR model override unsupported; configure transcription.asr_model instead")
	}

	key := cacheKey(req, model, s.opts.ForcedAlignerModel)
	if result, ok := s.Lookup(key); ok {
		s.logger.Info("transcription cache hit",
			"decision_type", logs.DecisionTranscriptionCache,
			"decision_result", "hit",
			"decision_reason", fmt.Sprintf("key=%s segments=%d", key[:12], result.Segments),
		)
		result.Cached = true
		return result, nil
	}
	s.logger.Info("transcription cache miss",
		"decision_type", logs.DecisionTranscriptionCache,
		"decision_result", "miss",
		"decision_reason", fmt.Sprintf("key=%s", key[:12]),
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
	langName, alignedSupported := qwenLanguageName(req.Language)
	if langName == "" {
		langName = "English"
	}
	if req.RequireAlignment && !alignedSupported {
		return nil, fmt.Errorf("subtitle alignment unsupported for language %q", req.Language)
	}
	returnTimestamps := alignedSupported
	s.logger.Info("running Qwen3-ASR transcription",
		"event_type", "transcription_qwen3_asr",
		"asr_model", model,
		"forced_aligner_model", s.opts.ForcedAlignerModel,
		"language", req.Language,
		"return_timestamps", returnTimestamps,
	)
	transcribeStart := time.Now()
	resp, err := s.worker.Transcribe(ctx, workerTranscribeRequest{
		AudioPath:        wavPath,
		Language:         langName,
		ReturnTimeStamps: returnTimestamps,
	})
	if err != nil {
		return nil, fmt.Errorf("qwen3 transcription: %w", err)
	}
	transcribeTime := time.Since(transcribeStart)
	if onProgress != nil {
		onProgress(PhaseTranscribe, transcribeTime)
	}
	resp.Text = normalizeWorkerText(resp.Text)
	if strings.TrimSpace(resp.Text) == "" && len(resp.TimeStamps) > 0 {
		var b strings.Builder
		for _, stamp := range resp.TimeStamps {
			b.WriteString(stamp.Text)
		}
		resp.Text = normalizeWorkerText(b.String())
	}
	if req.RequireAlignment && len(resp.TimeStamps) == 0 {
		return nil, fmt.Errorf("qwen3 forced aligner returned no timestamps for language %q", req.Language)
	}
	artifacts, err := buildTranscriptArtifacts(req.OutputDir, req.Language, resp)
	if err != nil {
		return nil, err
	}
	result := &TranscribeResult{
		SRTPath:        artifacts.SRTPath,
		JSONPath:       artifacts.JSONPath,
		Duration:       artifacts.Duration,
		Segments:       artifacts.Segments,
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
func cacheKey(req TranscribeRequest, asrModel, forcedAlignerModel string) string {
	parts := []string{canonicalSchemaVersion, asrModel, forcedAlignerModel, req.Language}
	if req.ContentKey != "" {
		parts = append(parts, req.ContentKey)
	} else {
		parts = append(parts, req.InputPath, strconv.Itoa(req.AudioIndex))
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])
}

// Lookup checks the cache for a previously stored canonical transcription result.
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
	return &TranscribeResult{SRTPath: srtPath, JSONPath: jsonPath, Duration: duration, Segments: segments}, true
}

// Store copies canonical transcription artifacts into the cache directory under the given key.
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

// analyzeSRT reads an SRT file once and returns both the segment count and the duration.
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

// Config returns the service configuration for display/debug output.
func (s *Service) Config() Options {
	return s.opts
}
