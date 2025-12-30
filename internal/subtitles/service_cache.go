package subtitles

import (
	"errors"
	"os"
	"strings"
	"time"

	"spindle/internal/logging"
)

func (s *Service) ensureTranscriptCache() error {
	if s == nil || s.config == nil {
		return errors.New("subtitle service unavailable")
	}
	s.transcriptCacheOnce.Do(func() {
		dir := strings.TrimSpace(s.config.Paths.WhisperXCacheDir)
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
			s.logger.Warn("whisperx transcript cache unavailable; caching disabled",
				logging.Error(err),
				logging.String(logging.FieldEventType, "transcript_cache_unavailable"),
				logging.String(logging.FieldErrorHint, "check whisperx_cache_dir permissions"),
			)
		}
		return
	}
	if s.transcriptCache == nil {
		return
	}
	data, err := os.ReadFile(plan.outputFile)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("whisperx transcript cache read failed; skipping cache write",
				logging.Error(err),
				logging.String(logging.FieldEventType, "transcript_cache_read_failed"),
				logging.String(logging.FieldErrorHint, "check whisperx_cache_dir permissions"),
			)
		}
		return
	}
	language := strings.TrimSpace(req.Context.Language)
	if language == "" {
		language = plan.language
	}
	if _, err := s.transcriptCache.Store(req.TranscriptKey, language, segmentCount, data); err != nil {
		if s.logger != nil {
			s.logger.Warn("whisperx transcript cache store failed; cache may be stale",
				logging.Error(err),
				logging.String(logging.FieldEventType, "transcript_cache_store_failed"),
				logging.String(logging.FieldErrorHint, "check whisperx_cache_dir permissions"),
			)
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
