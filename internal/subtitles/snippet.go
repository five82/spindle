package subtitles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"spindle/internal/logging"
	"spindle/internal/services"
)

// SnippetRequest describes a small audio segment to be transcribed via WhisperX.
// It is used by pre-encoding analysis workflows (for example commentary detection).
type SnippetRequest struct {
	SourcePath      string
	AudioIndex      int
	StartSeconds    float64
	DurationSeconds float64
	Language        string
	WorkDir         string

	TranscriptKey             string
	AllowTranscriptCacheRead  bool
	AllowTranscriptCacheWrite bool
}

// SnippetResult captures the WhisperX output for a snippet.
type SnippetResult struct {
	PlainText    string
	SegmentCount int
	Language     string
	Source       string // "cache" or "whisperx"
}

func (s *Service) TranscribeSnippetPlainText(ctx context.Context, req SnippetRequest) (SnippetResult, error) {
	if s == nil {
		return SnippetResult{}, services.Wrap(services.ErrConfiguration, "subtitles", "init", "Subtitle service unavailable", nil)
	}
	source := strings.TrimSpace(req.SourcePath)
	if source == "" {
		return SnippetResult{}, services.Wrap(services.ErrValidation, "subtitles", "validate input", "Source path is empty", nil)
	}
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SnippetResult{}, services.Wrap(services.ErrNotFound, "subtitles", "stat source", "Source file not found", err)
		}
		return SnippetResult{}, services.Wrap(services.ErrValidation, "subtitles", "stat source", "Failed to inspect source file", err)
	}
	if req.AudioIndex < 0 {
		return SnippetResult{}, services.Wrap(services.ErrValidation, "subtitles", "validate audio", "Audio track index must be >= 0", nil)
	}
	if req.DurationSeconds <= 0 {
		return SnippetResult{}, services.Wrap(services.ErrValidation, "subtitles", "validate window", "DurationSeconds must be positive", nil)
	}
	if req.StartSeconds < 0 {
		req.StartSeconds = 0
	}

	if err := s.ensureReady(); err != nil {
		return SnippetResult{}, err
	}

	workDir := strings.TrimSpace(req.WorkDir)
	if workDir == "" {
		workDir = filepath.Dir(source)
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return SnippetResult{}, services.Wrap(services.ErrConfiguration, "subtitles", "ensure workdir", "Failed to create work directory", err)
	}

	language := strings.TrimSpace(req.Language)
	if language == "" {
		language = "en"
	}

	key := strings.TrimSpace(req.TranscriptKey)
	if key == "" {
		key = buildSnippetTranscriptKey(source, req.AudioIndex, req.StartSeconds, req.DurationSeconds, language)
	}

	if req.AllowTranscriptCacheRead {
		if err := s.ensureTranscriptCache(); err == nil && s.transcriptCache != nil {
			if data, meta, ok, err := s.transcriptCache.Load(key); err != nil {
				if s.logger != nil {
					s.logger.Warn("whisperx snippet cache read failed", logging.Error(err))
				}
			} else if ok && len(data) > 0 {
				plain, segments, err := plainTextFromSRTBytes(data)
				if err == nil && strings.TrimSpace(plain) != "" {
					if s.logger != nil {
						s.logger.Debug("whisperx snippet cache hit",
							logging.String("cache_key", key),
							logging.Int("segments", segments),
							logging.String("language", strings.TrimSpace(meta.Language)),
						)
					}
					return SnippetResult{
						PlainText:    plain,
						SegmentCount: segments,
						Language:     meta.Language,
						Source:       "cache",
					}, nil
				}
			}
		}
	}

	started := time.Now()
	runDir := filepath.Join(workDir, "whisperx-snippets")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return SnippetResult{}, services.Wrap(services.ErrConfiguration, "subtitles", "ensure whisperx dir", "Failed to create WhisperX snippet directory", err)
	}
	base := fmt.Sprintf("a%d_s%d_d%d",
		req.AudioIndex,
		int64(req.StartSeconds*1000),
		int64(req.DurationSeconds*1000),
	)
	audioPath := filepath.Join(runDir, base+".wav")
	srtPath := filepath.Join(runDir, base+".srt")

	defer func() {
		if os.Getenv("SPD_DEBUG_COMMENTARY_KEEP") != "" {
			return
		}
		_ = os.Remove(audioPath)
		_ = os.Remove(srtPath)
	}()

	if err := extractAudioSnippet(ctx, s.run, source, req.AudioIndex, req.StartSeconds, req.DurationSeconds, audioPath); err != nil {
		return SnippetResult{}, err
	}
	args := s.buildWhisperXArgs(audioPath, runDir, language)
	if err := s.run(ctx, whisperXCommand, args...); err != nil {
		return SnippetResult{}, services.Wrap(services.ErrExternalTool, "subtitles", "whisperx", "WhisperX execution failed", err)
	}
	data, err := os.ReadFile(srtPath)
	if err != nil {
		return SnippetResult{}, services.Wrap(services.ErrTransient, "subtitles", "whisperx output", "Failed to read WhisperX SRT output", err)
	}
	plain, segments, err := plainTextFromSRTBytes(data)
	if err != nil {
		return SnippetResult{}, services.Wrap(services.ErrTransient, "subtitles", "parse whisperx", "Failed to parse WhisperX subtitles", err)
	}

	if req.AllowTranscriptCacheWrite {
		if err := s.ensureTranscriptCache(); err == nil && s.transcriptCache != nil && len(data) > 0 && segments > 0 {
			if _, err := s.transcriptCache.Store(key, language, segments, data); err != nil && s.logger != nil {
				s.logger.Warn("whisperx snippet cache store failed", logging.Error(err))
			}
		}
	}

	if s.logger != nil {
		s.logger.Debug("whisperx snippet transcribed",
			logging.String("source", source),
			logging.Int("audio_index", req.AudioIndex),
			logging.Float64("start_seconds", req.StartSeconds),
			logging.Float64("duration_seconds", req.DurationSeconds),
			logging.Int("segments", segments),
			logging.Duration("elapsed", time.Since(started)),
		)
	}
	return SnippetResult{
		PlainText:    plain,
		SegmentCount: segments,
		Language:     language,
		Source:       "whisperx",
	}, nil
}

func buildSnippetTranscriptKey(source string, audioIndex int, startSeconds, durationSeconds float64, language string) string {
	info, err := os.Stat(source)
	size := int64(0)
	mod := int64(0)
	if err == nil {
		size = info.Size()
		mod = info.ModTime().UnixNano()
	}
	raw := strings.Join([]string{
		"commentary_snippet_v1",
		source,
		strconv.FormatInt(size, 10),
		strconv.FormatInt(mod, 10),
		strconv.Itoa(audioIndex),
		strconv.FormatInt(int64(startSeconds*1000), 10),
		strconv.FormatInt(int64(durationSeconds*1000), 10),
		strings.ToLower(strings.TrimSpace(language)),
	}, "|")
	sum := sha256.Sum256([]byte(raw))
	return "commentary_snippet:" + hex.EncodeToString(sum[:])
}

func extractAudioSnippet(ctx context.Context, run commandRunner, source string, audioIndex int, startSeconds, durationSeconds float64, destination string) error {
	if run == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "extract snippet", "Command runner unavailable", nil)
	}
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-ss", fmt.Sprintf("%.3f", startSeconds),
		"-t", fmt.Sprintf("%.3f", durationSeconds),
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
	if err := run(ctx, ffmpegCommand, args...); err != nil {
		return services.Wrap(services.ErrExternalTool, "subtitles", "extract snippet", "Failed to extract audio snippet with ffmpeg", err)
	}
	return nil
}

func plainTextFromSRTBytes(data []byte) (string, int, error) {
	cleaned, _ := CleanSRT(data)
	plain := strings.TrimSpace(PlainTextFromSRT(cleaned))
	segments, err := countSRTCuesFromBytes(cleaned)
	if err != nil {
		return plain, 0, nil
	}
	return plain, segments, nil
}

func countSRTCuesFromBytes(data []byte) (int, error) {
	tmp, err := os.CreateTemp("", "spindle-snippet-*.srt")
	if err != nil {
		return 0, err
	}
	path := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(path)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return 0, err
	}
	return countSRTCues(path)
}
