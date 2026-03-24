// Package transcription provides shared WhisperX transcription with caching.
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
)

// Service provides WhisperX transcription with caching.
type Service struct {
	model       string
	cudaEnabled bool
	vadMethod   string
	hfToken     string
	cacheDir    string
	logger      *slog.Logger
}

// New creates a transcription service.
func New(model string, cudaEnabled bool, vadMethod, hfToken, cacheDir string, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if model == "" {
		model = "large-v3"
	}
	if vadMethod == "" {
		vadMethod = "silero"
	}
	return &Service{
		model:       model,
		cudaEnabled: cudaEnabled,
		vadMethod:   vadMethod,
		hfToken:     hfToken,
		cacheDir:    cacheDir,
		logger:      logger,
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

// ProgressFunc is called at phase boundaries during transcription.
// phase is "extract" or "transcribe". elapsed is zero at phase start
// and non-zero at phase end.
type ProgressFunc func(phase string, elapsed time.Duration)

// TranscribeResult contains transcription output.
type TranscribeResult struct {
	SRTPath        string
	Duration       float64       // total SRT duration (seconds) from last cue timestamp
	Segments       int
	Cached         bool
	ExtractTime    time.Duration // time spent on ffmpeg audio extraction
	TranscribeTime time.Duration // time spent on WhisperX
}

// Transcribe runs WhisperX transcription with caching.
//
// Steps:
//  1. Compute cache key.
//  2. Check cache. If hit, return cached result.
//  3. Extract audio via FFmpeg.
//  4. Run WhisperX via uvx.
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

	model := req.Model
	if model == "" {
		model = s.model
	}

	key := cacheKey(req, model)

	// Check cache.
	if result, ok := s.Lookup(key); ok {
		s.logger.Info("transcription cache hit",
			"decision_type", "transcription_cache",
			"decision_result", "hit",
			"decision_reason", fmt.Sprintf("key=%s segments=%d", key[:12], result.Segments),
		)
		result.Cached = true
		return result, nil
	}
	s.logger.Info("transcription cache miss",
		"decision_type", "transcription_cache",
		"decision_result", "miss",
		"decision_reason", fmt.Sprintf("key=%s", key[:12]),
	)

	// Ensure output directory exists.
	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	// Extract audio via FFmpeg.
	if onProgress != nil {
		onProgress("extract", 0)
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
		onProgress("extract", extractTime)
	}

	// Run WhisperX via uvx.
	if onProgress != nil {
		onProgress("transcribe", 0)
	}
	whisperArgs := []string{
		"whisperx", wavPath,
		"--model", model,
		"--language", req.Language,
		"--output_format", "srt",
		"--output_dir", req.OutputDir,
		"--vad_method", s.vadMethod,
		"--max_line_width", "42",
		"--max_line_count", "2",
	}
	if s.cudaEnabled {
		whisperArgs = append(whisperArgs, "--device", "cuda")
	} else {
		whisperArgs = append(whisperArgs, "--device", "cpu")
	}
	if s.hfToken != "" {
		whisperArgs = append(whisperArgs, "--hf_token", s.hfToken)
	}

	s.logger.Info("running WhisperX transcription",
		"event_type", "transcription_whisperx",
		"model", model,
		"language", req.Language,
	)
	transcribeStart := time.Now()
	whisperCmd := exec.CommandContext(ctx, "uvx", whisperArgs...)
	if output, err := whisperCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("whisperx transcription: %w: %s", err, output)
	}
	transcribeTime := time.Since(transcribeStart)
	if onProgress != nil {
		onProgress("transcribe", transcribeTime)
	}

	// Find SRT output. WhisperX names it after the input wav.
	srtPath := filepath.Join(req.OutputDir, "audio.srt")
	if _, err := os.Stat(srtPath); err != nil {
		return nil, fmt.Errorf("srt output not found at %s: %w", srtPath, err)
	}

	segments, duration, err := analyzeSRT(srtPath)
	if err != nil {
		return nil, fmt.Errorf("analyze srt: %w", err)
	}

	result := &TranscribeResult{
		SRTPath:        srtPath,
		Duration:       duration,
		Segments:       segments,
		Cached:         false,
		ExtractTime:    extractTime,
		TranscribeTime: transcribeTime,
	}

	// Store in cache.
	if err := s.Store(key, result); err != nil {
		return nil, fmt.Errorf("cache store: %w", err)
	}
	s.logger.Debug("transcription result cached", "key", key[:12], "segments", result.Segments)

	return result, nil
}

// cacheKey computes a deterministic cache key for a transcription request.
// If ContentKey is non-empty, it is used for content-stable caching.
// Otherwise, the input path and audio index are used as a fallback.
func cacheKey(req TranscribeRequest, model string) string {
	var input string
	if req.ContentKey != "" {
		input = req.ContentKey + "\x00" + model + "\x00" + req.Language
	} else {
		input = req.InputPath + "\x00" + strconv.Itoa(req.AudioIndex) + "\x00" + model + "\x00" + req.Language
	}
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])
}

// Lookup checks the cache for a previously stored transcription result.
// It returns the result and true if a valid cached SRT exists with > 0 cues.
func (s *Service) Lookup(key string) (*TranscribeResult, bool) {
	dir := filepath.Join(s.cacheDir, key)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".srt") {
			continue
		}
		srtPath := filepath.Join(dir, entry.Name())
		segments, duration, err := analyzeSRT(srtPath)
		if err != nil || segments == 0 {
			continue
		}
		return &TranscribeResult{
			SRTPath:  srtPath,
			Duration: duration,
			Segments: segments,
		}, true
	}
	return nil, false
}

// Store copies the SRT from a transcription result into the cache directory
// under the given key.
func (s *Service) Store(key string, result *TranscribeResult) error {
	dir := filepath.Join(s.cacheDir, key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	src, err := os.ReadFile(result.SRTPath)
	if err != nil {
		return fmt.Errorf("read srt: %w", err)
	}

	dst := filepath.Join(dir, filepath.Base(result.SRTPath))
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		return fmt.Errorf("write cached srt: %w", err)
	}

	return nil
}

// analyzeSRT reads an SRT file once and returns both the segment count and
// the duration (end timestamp of the last cue, in seconds).
func analyzeSRT(path string) (segments int, duration float64, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return 0, 0, nil
	}

	blocks := strings.Split(content, "\n\n")
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		lines := strings.SplitN(block, "\n", 3)
		// A cue block starts with a sequence number.
		if _, atoiErr := strconv.Atoi(strings.TrimSpace(lines[0])); atoiErr != nil {
			continue
		}
		segments++
		// Parse end timestamp from the timing line.
		if len(lines) >= 2 {
			if idx := strings.Index(lines[1], "-->"); idx >= 0 {
				endPart := strings.TrimSpace(lines[1][idx+3:])
				if secs := parseSRTTimestamp(endPart); secs > 0 {
					duration = secs
				}
			}
		}
	}
	return segments, duration, nil
}

// parseSRTTimestamp parses "HH:MM:SS,mmm" into seconds.
func parseSRTTimestamp(s string) float64 {
	s = strings.TrimSpace(s)
	// Expected format: "01:38:12,456"
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 {
		return 0
	}
	hours, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	minutes, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	secParts := strings.SplitN(parts[2], ",", 2)
	if len(secParts) != 2 {
		return 0
	}
	secs, err := strconv.Atoi(secParts[0])
	if err != nil {
		return 0
	}
	millis, err := strconv.Atoi(secParts[1])
	if err != nil {
		return 0
	}
	return float64(hours)*3600 + float64(minutes)*60 + float64(secs) + float64(millis)/1000
}

// Config returns the service's WhisperX configuration for display purposes.
func (s *Service) Config() (model, device, vadMethod string) {
	model = s.model
	if s.cudaEnabled {
		device = "cuda"
	} else {
		device = "cpu"
	}
	vadMethod = s.vadMethod
	return
}

