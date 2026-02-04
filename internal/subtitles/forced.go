package subtitles

import (
	"context"
	"path/filepath"
	"strings"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
)

// tryForcedSubtitlesForTarget checks if the disc has forced subtitle tracks and
// downloads foreign-parts-only subtitles from OpenSubtitles if available.
// referenceSubtitle is the path to the aligned regular subtitle used for alignment.
// Returns the path to the downloaded forced subtitle, or empty string if none.
func (s *Stage) tryForcedSubtitlesForTarget(ctx context.Context, item *queue.Item, target subtitleTarget, ctxMeta SubtitleContext, env *ripspec.Envelope, referenceSubtitle string) string {
	if s == nil || s.service == nil || env == nil {
		return ""
	}

	hasForcedTrack, _ := env.Attributes["has_forced_subtitle_track"].(bool)
	episodeKey := normalizeEpisodeKey(target.EpisodeKey)

	if !hasForcedTrack {
		if s.logger != nil {
			s.logger.Info("forced subtitle search decision",
				logging.String(logging.FieldDecisionType, "forced_subtitle_search"),
				logging.String("decision_result", "skipped"),
				logging.String("decision_reason", "no_forced_track_on_disc"),
				logging.String("episode_key", episodeKey),
			)
		}
		return ""
	}

	if !s.service.shouldUseOpenSubtitles() {
		if s.logger != nil {
			s.logger.Info("forced subtitle search decision",
				logging.String(logging.FieldDecisionType, "forced_subtitle_search"),
				logging.String("decision_result", "skipped"),
				logging.String("decision_reason", "opensubtitles_disabled"),
				logging.String("episode_key", episodeKey),
			)
		}
		return ""
	}

	if s.logger != nil {
		s.logger.Info("forced subtitle search decision",
			logging.String(logging.FieldDecisionType, "forced_subtitle_search"),
			logging.String("decision_result", "searching"),
			logging.String("decision_reason", "disc_has_forced_track"),
			logging.String("episode_key", episodeKey),
		)
	}

	req := GenerateRequest{
		SourcePath: target.SourcePath,
		WorkDir:    target.WorkDir,
		OutputDir:  target.OutputDir,
		BaseName:   target.BaseName,
		Context:    ctxMeta,
		Languages:  append([]string(nil), s.service.languages...),
	}

	plan, err := s.service.prepareGenerationPlan(ctx, req)
	if err != nil {
		if s.logger != nil {
			s.logger.Debug("forced subtitle plan preparation failed",
				logging.Error(err),
				logging.String("episode_key", episodeKey),
			)
		}
		return ""
	}
	if plan.cleanup != nil {
		defer plan.cleanup()
	}

	basePath := filepath.Join(target.OutputDir, target.BaseName)
	forcedPath, err := s.service.tryForcedSubtitles(ctx, plan, req, basePath, referenceSubtitle)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("forced subtitle search failed",
				logging.Error(err),
				logging.String("episode_key", episodeKey),
				logging.String(logging.FieldEventType, "forced_subtitle_search_failed"),
				logging.String(logging.FieldErrorHint, "forced subtitles may not be available on OpenSubtitles"),
			)
		}
		return ""
	}

	if forcedPath == "" {
		if s.logger != nil {
			s.logger.Info("forced subtitle download decision",
				logging.String(logging.FieldDecisionType, "forced_subtitle_download"),
				logging.String("decision_result", "not_found"),
				logging.String("decision_reason", "no_foreign_parts_subtitle_available"),
				logging.String("episode_key", episodeKey),
				logging.String("title", strings.TrimSpace(ctxMeta.Title)),
			)
		}
		return ""
	}

	if s.logger != nil {
		s.logger.Info("forced subtitle downloaded successfully",
			logging.String(logging.FieldEventType, "forced_subtitle_complete"),
			logging.String("episode_key", episodeKey),
			logging.String("forced_subtitle_path", forcedPath),
		)
	}
	return forcedPath
}
