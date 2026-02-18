package subtitles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"spindle/internal/logging"
	"spindle/internal/services"
	"spindle/internal/subtitles/opensubtitles"
)

func (s *Service) fetchOpenSubtitlesPayload(ctx context.Context, req GenerateRequest, candidate opensubtitles.Subtitle) (opensubtitles.DownloadResult, error) {
	if s.openSubs == nil {
		return opensubtitles.DownloadResult{}, errors.New("opensubtitles client unavailable")
	}
	if s.openSubsCache != nil && candidate.FileID > 0 {
		if cached, ok, err := s.openSubsCache.Load(candidate.FileID); err != nil {
			if s.logger != nil {
				s.logger.Warn("opensubtitles cache load failed; continuing with network fetch",
					logging.Error(err),
					logging.String(logging.FieldEventType, "opensubtitles_cache_load_failed"),
					logging.String(logging.FieldErrorHint, "check opensubtitles_cache_dir permissions"),
				)
			}
		} else if ok {
			if s.logger != nil {
				s.logger.Debug("opensubtitles cache hit",
					logging.Int64("file_id", candidate.FileID),
					logging.String("language", cached.Entry.Language),
				)
			}
			return cached.DownloadResult(), nil
		}
	}
	payload, err := s.openSubs.Download(ctx, candidate.FileID, opensubtitles.DownloadOptions{Format: "srt"})
	if err != nil {
		return opensubtitles.DownloadResult{}, err
	}
	s.storeOpenSubtitlesPayload(candidate, payload, req)
	return payload, nil
}

func (s *Service) storeOpenSubtitlesPayload(candidate opensubtitles.Subtitle, payload opensubtitles.DownloadResult, req GenerateRequest) {
	if s.openSubsCache == nil || candidate.FileID <= 0 || len(payload.Data) == 0 {
		return
	}
	entry := opensubtitles.CacheEntry{
		FileID:       candidate.FileID,
		Language:     payload.Language,
		FileName:     payload.FileName,
		DownloadURL:  payload.DownloadURL,
		FeatureTitle: candidate.FeatureTitle,
		FeatureYear:  candidate.FeatureYear,
		Season:       req.Context.Season,
		Episode:      req.Context.Episode,
	}
	if req.Context.IsMovie() {
		entry.TMDBID = req.Context.TMDBID
		entry.ParentTMDBID = req.Context.ParentID()
	} else {
		entry.ParentTMDBID = req.Context.ParentID()
		entry.TMDBID = req.Context.EpisodeID()
	}
	if _, err := s.openSubsCache.Store(entry, payload.Data); err != nil && s.logger != nil {
		s.logger.Warn("opensubtitles cache store failed; subtitles will re-download next time",
			logging.Error(err),
			logging.String(logging.FieldEventType, "opensubtitles_cache_store_failed"),
			logging.String(logging.FieldErrorHint, "check opensubtitles_cache_dir permissions"),
		)
	}
}

// downloadAndAlignForcedCandidate downloads a forced subtitle candidate from
// OpenSubtitles and aligns it to the reference (regular) subtitle using
// text-based matching. Only used for forced (foreign-parts-only) subtitles.
func (s *Service) downloadAndAlignForcedCandidate(ctx context.Context, plan *generationPlan, req GenerateRequest, candidate opensubtitles.Subtitle) (GenerateResult, error) {
	if plan == nil {
		return GenerateResult{}, errors.New("generation plan not initialized")
	}
	payload, err := s.fetchOpenSubtitlesPayload(ctx, req, candidate)
	if err != nil {
		return GenerateResult{}, err
	}

	cleaned, stats := CleanSRT(payload.Data)
	cleanedPath := filepath.Join(plan.runDir, "opensubtitles-clean.srt")
	if err := os.WriteFile(cleanedPath, cleaned, 0o644); err != nil {
		return GenerateResult{}, fmt.Errorf("write cleaned subtitles: %w", err)
	}
	if s.logger != nil {
		s.logger.Debug("opensubtitles subtitles cleaned",
			logging.String("subtitle_file", cleanedPath),
			logging.Int("removed_cues", stats.RemovedCues),
		)
	}

	// Align forced subtitles to the reference (already-aligned regular) subtitle by
	// finding matching cues and calculating a time transformation.
	matchCount, transform, err := alignForcedToReference(plan.referenceSubtitlePath, cleanedPath, plan.outputFile)
	if err != nil {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "align forced", "Failed to align forced subtitle", err)
	}
	if s.logger != nil {
		if matchCount >= 2 {
			s.logger.Debug("forced subtitle aligned to reference",
				logging.String("subtitle_file", plan.outputFile),
				logging.String("reference_file", plan.referenceSubtitlePath),
				logging.Int("matched_cues", matchCount),
				logging.Float64("scale_factor", transform.scale),
				logging.Float64("offset_seconds", transform.offset),
			)
		} else {
			s.logger.Debug("forced subtitle alignment skipped (insufficient matching cues)",
				logging.String("subtitle_file", plan.outputFile),
				logging.Int("matched_cues", matchCount),
				logging.String("reason", "using_original_timing"),
			)
		}
	}

	segmentCount, err := countSRTCues(plan.outputFile)
	if err != nil {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "analyze srt", "Failed to inspect downloaded subtitles", err)
	}
	if segmentCount == 0 {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "opensubtitles", "Fetched subtitles contained no cues", nil)
	}

	if s.logger != nil {
		s.logger.Debug("forced subtitle download complete",
			logging.String("release", strings.TrimSpace(candidate.Release)),
			logging.String("language", candidate.Language),
			logging.Int("segments", segmentCount),
			logging.Bool("ai_translated", candidate.AITranslated),
			logging.Int("removed_cues", stats.RemovedCues),
		)
	}

	finalDuration := plan.totalSeconds
	if finalDuration <= 0 {
		if last, err := lastSRTTimestamp(plan.outputFile); err == nil && last > 0 {
			finalDuration = last
		}
	}

	return GenerateResult{
		SubtitlePath: plan.outputFile,
		SegmentCount: segmentCount,
		Duration:     time.Duration(finalDuration * float64(time.Second)),
		Source:       "opensubtitles",
	}, nil
}
