package subtitles

import (
	"context"
	"fmt"
	"strings"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
)

const (
	subtitleGenerationResultsKey = "subtitle_generation_results"
	subtitleGenerationSummaryKey = "subtitle_generation_summary"
)

func recordSubtitleGeneration(env *ripspec.Envelope, episodeKey, language string, result GenerateResult, cached bool) {
	if env == nil {
		return
	}
	if env.Attributes == nil {
		env.Attributes = make(map[string]any)
	}
	key := normalizeEpisodeKey(episodeKey)

	entry := map[string]any{
		"episode_key": key,
		"source":      "whisperx",
	}
	if cached {
		entry["cached"] = true
	}
	if strings.TrimSpace(result.SubtitlePath) != "" {
		entry["subtitle_path"] = strings.TrimSpace(result.SubtitlePath)
	}
	if result.SegmentCount > 0 {
		entry["segments"] = result.SegmentCount
	}
	if lang := strings.ToLower(strings.TrimSpace(language)); lang != "" {
		entry["language"] = lang
	}

	var list []map[string]any
	switch v := env.Attributes[subtitleGenerationResultsKey].(type) {
	case []map[string]any:
		list = v
	case []any:
		list = make([]map[string]any, 0, len(v))
		for _, raw := range v {
			if m, ok := raw.(map[string]any); ok {
				list = append(list, m)
			}
		}
	}
	replaced := false
	for i := range list {
		existingKey := strings.ToLower(strings.TrimSpace(toString(list[i]["episode_key"])))
		if existingKey != "" && strings.EqualFold(existingKey, key) {
			list[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, entry)
	}
	env.Attributes[subtitleGenerationResultsKey] = list

	env.Attributes[subtitleGenerationSummaryKey] = map[string]any{
		"source": "whisperx",
	}
}

func toString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		return ""
	}
}

func (s *Stage) persistRipSpec(ctx context.Context, item *queue.Item, env *ripspec.Envelope) {
	if err := queue.PersistRipSpec(ctx, s.store, item, env); err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to persist rip spec after subtitle; metadata may be stale",
				logging.Error(err),
				logging.String(logging.FieldEventType, "rip_spec_persist_failed"),
				logging.String(logging.FieldErrorHint, "rerun identification or check queue database access"),
				logging.String(logging.FieldImpact, "subtitle metadata may not reflect latest state"),
			)
		}
	}
}

// processGenerationResult handles logging and RipSpec update after successful generation.
func (s *Stage) processGenerationResult(ctx context.Context, item *queue.Item, target subtitleTarget, result GenerateResult, env *ripspec.Envelope, hasRipSpec bool, ctxMeta SubtitleContext, cached bool) {
	episodeKey := normalizeEpisodeKey(target.EpisodeKey)

	if s.logger != nil {
		reason := "whisperx_only"
		if cached {
			reason = "contentid_cache_reused"
		}
		s.logger.Info("subtitle generation decision",
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
	s.persistRipSpec(ctx, item, env)
}
