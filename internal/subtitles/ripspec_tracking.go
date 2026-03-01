package subtitles

import (
	"context"
	"strings"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
)

func recordSubtitleGeneration(env *ripspec.Envelope, episodeKey, language string, result GenerateResult, cached bool) {
	if env == nil {
		return
	}
	key := normalizeEpisodeKey(episodeKey)

	entry := ripspec.SubtitleGenRecord{
		EpisodeKey: key,
		Source:     "whisperx",
		Cached:     cached,
	}
	if strings.TrimSpace(result.SubtitlePath) != "" {
		entry.SubtitlePath = strings.TrimSpace(result.SubtitlePath)
	}
	if result.SegmentCount > 0 {
		entry.Segments = result.SegmentCount
	}
	if lang := strings.ToLower(strings.TrimSpace(language)); lang != "" {
		entry.Language = lang
	}

	replaced := false
	for i := range env.Attributes.SubtitleGenerationResults {
		existingKey := strings.ToLower(strings.TrimSpace(env.Attributes.SubtitleGenerationResults[i].EpisodeKey))
		if existingKey != "" && strings.EqualFold(existingKey, key) {
			env.Attributes.SubtitleGenerationResults[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		env.Attributes.SubtitleGenerationResults = append(env.Attributes.SubtitleGenerationResults, entry)
	}

	env.Attributes.SubtitleGenerationSummary = &ripspec.SubtitleGenSummary{
		Source: "whisperx",
	}
}

func (g *Generator) persistRipSpec(ctx context.Context, item *queue.Item, env *ripspec.Envelope) {
	if err := queue.PersistRipSpec(ctx, g.store, item, env); err != nil {
		if g.logger != nil {
			g.logger.Warn("failed to persist rip spec after subtitle; metadata may be stale",
				logging.Error(err),
				logging.String(logging.FieldEventType, "rip_spec_persist_failed"),
				logging.String(logging.FieldErrorHint, "rerun identification or check queue database access"),
				logging.String(logging.FieldImpact, "subtitle metadata may not reflect latest state"),
			)
		}
	}
}

// processGenerationResult handles logging and RipSpec update after successful generation.
func (g *Generator) processGenerationResult(ctx context.Context, item *queue.Item, target subtitleTarget, result GenerateResult, env *ripspec.Envelope, hasRipSpec bool, ctxMeta SubtitleContext, cached bool) {
	episodeKey := normalizeEpisodeKey(target.EpisodeKey)

	if g.logger != nil {
		reason := "whisperx_only"
		if cached {
			reason = "contentid_cache_reused"
		}
		g.logger.Info("subtitle generation decision",
			logging.String(logging.FieldDecisionType, "subtitle_generation"),
			logging.String("decision_result", "whisperx"),
			logging.String("decision_reason", reason),
			logging.String("episode_key", episodeKey),
			logging.String("source_file", target.SourcePath),
			logging.String("subtitle_file", result.SubtitlePath),
			logging.Int("segments", result.SegmentCount),
		)
	}

	if !hasRipSpec {
		return
	}
	recordSubtitleGeneration(env, episodeKey, ctxMeta.Language, result, cached)
	g.persistRipSpec(ctx, item, env)
}
