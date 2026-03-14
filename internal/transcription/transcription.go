// Package transcription provides shared WhisperX transcription with caching.
package transcription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Service provides WhisperX transcription with caching.
type Service struct {
	model       string
	cudaEnabled bool
	vadMethod   string
	hfToken     string
	cacheDir    string
}

// New creates a transcription service.
func New(model string, cudaEnabled bool, vadMethod, hfToken, cacheDir string) *Service {
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

// TranscribeResult contains transcription output.
type TranscribeResult struct {
	SRTPath  string
	Duration float64
	Segments int
	Cached   bool
}

// Transcribe runs WhisperX transcription with caching.
//
// Steps:
//  1. Compute cache key.
//  2. Check cache. If hit, return cached result.
//  3. Extract audio via FFmpeg.
//  4. Run WhisperX via uvx.
//  5. Read SRT, count segments.
//  6. Store in cache.
//  7. Return result.
func (s *Service) Transcribe(ctx context.Context, req TranscribeRequest) (*TranscribeResult, error) {
	model := req.Model
	if model == "" {
		model = s.model
	}

	key := cacheKey(req, model)

	// Check cache.
	if result, ok := s.Lookup(key); ok {
		result.Cached = true
		return result, nil
	}

	// Ensure output directory exists.
	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	// Extract audio via FFmpeg.
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

	ffmpegCmd := exec.CommandContext(ctx, "ffmpeg", ffmpegArgs...)
	if output, err := ffmpegCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg audio extraction: %w: %s", err, output)
	}

	// Run WhisperX via uvx.
	whisperArgs := []string{
		"whisperx", wavPath,
		"--model", model,
		"--language", req.Language,
		"--output_format", "srt",
		"--output_dir", req.OutputDir,
		"--vad_method", s.vadMethod,
	}
	if s.cudaEnabled {
		whisperArgs = append(whisperArgs, "--device", "cuda")
	} else {
		whisperArgs = append(whisperArgs, "--device", "cpu")
	}
	if s.hfToken != "" {
		whisperArgs = append(whisperArgs, "--hf_token", s.hfToken)
	}

	whisperCmd := exec.CommandContext(ctx, "uvx", whisperArgs...)
	if output, err := whisperCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("whisperx transcription: %w: %s", err, output)
	}

	// Find SRT output. WhisperX names it after the input wav.
	srtPath := filepath.Join(req.OutputDir, "audio.srt")
	if _, err := os.Stat(srtPath); err != nil {
		return nil, fmt.Errorf("srt output not found at %s: %w", srtPath, err)
	}

	segments, err := countSRTSegments(srtPath)
	if err != nil {
		return nil, fmt.Errorf("count srt segments: %w", err)
	}

	result := &TranscribeResult{
		SRTPath:  srtPath,
		Segments: segments,
		Cached:   false,
	}

	// Store in cache.
	if err := s.Store(key, result); err != nil {
		return nil, fmt.Errorf("cache store: %w", err)
	}

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
		segments, err := countSRTSegments(srtPath)
		if err != nil || segments == 0 {
			continue
		}
		return &TranscribeResult{
			SRTPath:  srtPath,
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

// countSRTSegments counts the number of cues in an SRT file.
// Cues are blank-line-delimited blocks starting with a number.
func countSRTSegments(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return 0, nil
	}

	// Split by blank lines (one or more empty lines).
	blocks := strings.Split(content, "\n\n")
	count := 0
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		// A cue block starts with a sequence number (digits).
		firstLine := strings.SplitN(block, "\n", 2)[0]
		firstLine = strings.TrimSpace(firstLine)
		if _, err := strconv.Atoi(firstLine); err == nil {
			count++
		}
	}
	return count, nil
}
