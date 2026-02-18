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

func (s *Service) downloadAndAlignCandidate(ctx context.Context, plan *generationPlan, req GenerateRequest, candidate opensubtitles.Subtitle) (GenerateResult, error) {
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

	// Early duration pre-check: reject obviously wrong candidates before
	// expensive alignment (saves ~30-40s per rejected candidate).
	// Skip for forced subtitles - they're sparse and end when last foreign dialogue occurs.
	if !candidate.ForeignPartsOnly {
		if delta, reject := earlyDurationPreCheck(cleanedPath, plan.totalSeconds); reject {
			if s.logger != nil {
				s.logger.Debug("opensubtitles candidate rejected early (duration pre-check)",
					logging.Float64("delta_seconds", delta),
					logging.String("release", candidate.Release),
				)
			}
			return GenerateResult{}, earlyDurationRejectError{
				deltaSeconds: delta,
				release:      candidate.Release,
			}
		}
	}

	inputPath := cleanedPath

	// Forced subtitles (indicated by referenceSubtitlePath being set) skip audio-based alignment
	// because they're sparse (few cues) and alignment algorithms produce garbage with insufficient data.
	// Instead, we align forced subtitles to the reference (already-aligned regular) subtitle by
	// finding matching cues and calculating a time transformation.
	isForcedSubtitle := strings.TrimSpace(plan.referenceSubtitlePath) != ""

	if isForcedSubtitle {
		matchCount, transform, err := alignForcedToReference(plan.referenceSubtitlePath, inputPath, plan.outputFile)
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
	} else {
		if syncedPath, err := s.applyFFSubsync(ctx, plan, cleanedPath); err != nil {
			if s.logger != nil {
				s.logger.Warn("ffsubsync alignment failed",
					logging.Error(err),
					logging.String("source_file", cleanedPath),
					logging.String(logging.FieldEventType, "ffsubsync_failed"),
					logging.String(logging.FieldErrorHint, "verify with: uvx --from ffsubsync ffsubsync --version"),
				)
			}
			return GenerateResult{}, err
		} else if syncedPath != "" {
			// Validate ffsubsync alignment quality before proceeding.
			// FFSubsync preserves cue structure 1:1, so before/after comparison is reliable.
			if qualityErr := checkAlignmentQuality(cleanedPath, syncedPath, candidate.Release); qualityErr != nil {
				if s.logger != nil {
					s.logger.Info("ffsubsync alignment quality check failed",
						logging.String(logging.FieldEventType, "alignment_quality_failed"),
						logging.String(logging.FieldDecisionType, "alignment_quality"),
						logging.String("decision_result", "rejected"),
						logging.String("decision_reason", qualityErr.reason),
						logging.String("release", candidate.Release),
						logging.Float64("shift_mean", qualityErr.metrics.shiftMean),
						logging.Float64("shift_median", qualityErr.metrics.shiftMedian),
						logging.Float64("shift_stddev", qualityErr.metrics.shiftStdDev),
						logging.Float64("shift_max", qualityErr.metrics.shiftMax),
						logging.Int("cue_count", qualityErr.metrics.cueCount),
						logging.Int("negative_timestamps", qualityErr.metrics.negativeTimestamps),
						logging.Int("zero_duration_cues", qualityErr.metrics.zeroDurationCues),
						logging.Int("new_overlaps", qualityErr.metrics.newOverlaps),
					)
				}
				return GenerateResult{}, qualityErr
			}
			inputPath = syncedPath
			if s.logger != nil {
				s.logger.Debug("ffsubsync alignment complete",
					logging.String("subtitle_file", syncedPath),
				)
			}
		}

		alignLanguage := req.Context.Language
		if alignLanguage == "" {
			alignLanguage = plan.language
		}
		if s.logger != nil {
			s.logger.Debug("opensubtitles aligning subtitles",
				logging.String("language", alignLanguage),
				logging.Bool("cuda_enabled", plan.cudaEnabled),
			)
		}
		if err := s.alignDownloadedSubtitles(ctx, plan, inputPath, plan.outputFile, alignLanguage); err != nil {
			return GenerateResult{}, err
		}
		if s.logger != nil {
			s.logger.Debug("opensubtitles alignment complete",
				logging.String("subtitle_file", plan.outputFile),
			)
		}

		// Validate WhisperX output integrity (no negative timestamps, no zero-duration cues).
		// WhisperX can change cue structure, so only output-only checks are reliable here.
		if qualityErr := checkOutputIntegrity(plan.outputFile, candidate.Release); qualityErr != nil {
			if s.logger != nil {
				s.logger.Info("whisperx output integrity check failed",
					logging.String(logging.FieldEventType, "output_integrity_failed"),
					logging.String(logging.FieldDecisionType, "output_integrity"),
					logging.String("decision_result", "rejected"),
					logging.String("decision_reason", qualityErr.reason),
					logging.String("release", candidate.Release),
					logging.Int("negative_timestamps", qualityErr.metrics.negativeTimestamps),
					logging.Int("zero_duration_cues", qualityErr.metrics.zeroDurationCues),
				)
			}
			return GenerateResult{}, qualityErr
		}
	}

	segmentCount, err := countSRTCues(plan.outputFile)
	if err != nil {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "analyze srt", "Failed to inspect downloaded subtitles", err)
	}
	if segmentCount == 0 {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "opensubtitles", "Fetched subtitles contained no cues", nil)
	}

	// Validate subtitle density: catch obviously sparse/incomplete subtitles
	// (e.g., 143 cues for a 126-minute movie is only 1.1 cues/min, expected 6-12).
	// Skip for forced subtitles - they're intentionally sparse.
	if !candidate.ForeignPartsOnly {
		if sparseResult := checkSubtitleDensity(plan.outputFile, plan.totalSeconds, segmentCount); sparseResult != nil {
			if s.logger != nil {
				s.logger.Debug("opensubtitles candidate rejected (sparse subtitles)",
					logging.Int("cue_count", sparseResult.cueCount),
					logging.Float64("video_minutes", sparseResult.videoMinutes),
					logging.Float64("cues_per_minute", sparseResult.cuesPerMinute),
					logging.Float64("coverage_ratio", sparseResult.coverageRatio),
					logging.String("reason", sparseResult.reason),
					logging.String("release", candidate.Release),
				)
			}
			return GenerateResult{}, sparseResult
		}
	}

	// Skip duration check for forced subtitles - they end when last foreign dialogue occurs.
	if !candidate.ForeignPartsOnly {
		delta, mismatch, err := checkSubtitleDuration(plan.outputFile, plan.totalSeconds)
		if err != nil {
			return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "duration inspect", "Failed to compare subtitle duration", err)
		}
		if mismatch && delta > 0 {
			if start, last, boundsErr := subtitleBounds(plan.outputFile); boundsErr == nil {
				introGap := start
				if introGap < 0 {
					introGap = 0
				}
				tailDelta := plan.totalSeconds - last

				// Accept with intro gap exception if:
				// - Intro gap is between 5s and 5 minutes (reasonable opening credits/sequence)
				// - Tail gap is 0-45s (reasonable credits)
				// Larger intro gaps are suspicious (wrong cut, missing content).
				introGapValid := introGap >= subtitleIntroMinimumSeconds && introGap <= subtitleIntroMaximumSeconds
				tailGapValid := tailDelta > 0 && tailDelta <= subtitleIntroAllowanceSeconds

				if introGapValid && tailGapValid {
					if s.logger != nil {
						s.logger.Debug("opensubtitles accepted with intro gap",
							logging.Float64("intro_gap_seconds", introGap),
							logging.Float64("tail_delta_seconds", tailDelta),
							logging.String("release", candidate.Release),
						)
					}
					mismatch = false
				}
			}
		}
		if mismatch {
			if s.logger != nil {
				s.logger.Debug("opensubtitles candidate soft-rejected (duration mismatch)",
					logging.Float64("delta_seconds", delta),
					logging.String("release", candidate.Release),
				)
			}
			return GenerateResult{}, durationMismatchError{
				deltaSeconds: delta,
				videoSeconds: plan.totalSeconds,
				release:      candidate.Release,
			}
		}
	}

	if s.logger != nil {
		s.logger.Debug("open subtitles download complete",
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

func (s *Service) alignDownloadedSubtitles(ctx context.Context, plan *generationPlan, inputPath, outputPath, language string) error {
	if plan == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "align", "Subtitle generation plan not initialized", nil)
	}
	device := whisperXCPUDevice
	if plan.cudaEnabled {
		device = whisperXCUDADevice
	}
	lang := normalizeWhisperLanguage(language)
	if lang == "" {
		lang = "en"
	}
	args := []string{
		"--from", whisperXPackage,
		"python", "-c", whisperXAlignerScript,
		plan.audioPath,
		inputPath,
		outputPath,
		lang,
		device,
	}
	if err := s.run(ctx, whisperXCommand, args...); err != nil {
		return services.Wrap(services.ErrExternalTool, "subtitles", "align", "Failed to align downloaded subtitles", err)
	}
	return nil
}

func (s *Service) applyFFSubsync(ctx context.Context, plan *generationPlan, inputPath string) (string, error) {
	if plan == nil {
		return "", errors.New("generation plan not initialized")
	}
	audioPath := strings.TrimSpace(plan.audioPath)
	if audioPath == "" {
		return "", errors.New("primary audio reference unavailable for ffsubsync")
	}
	if _, err := os.Stat(audioPath); err != nil {
		return "", fmt.Errorf("ffsubsync audio reference missing: %w", err)
	}
	if _, err := os.Stat(inputPath); err != nil {
		return "", fmt.Errorf("ffsubsync input missing: %w", err)
	}
	outputPath := filepath.Join(plan.runDir, "opensubtitles-ffsubsync.srt")
	args := []string{
		"--from", ffsubsyncPackage,
		"ffsubsync",
		audioPath,
		"-i", inputPath,
		"-o", outputPath,
	}
	if err := s.run(ctx, ffsubsyncCommand, args...); err != nil {
		return "", services.Wrap(services.ErrExternalTool, "subtitles", "ffsubsync", "Failed to synchronize downloaded subtitles", err)
	}
	return outputPath, nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
