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

func recordSubtitleGeneration(env *ripspec.Envelope, episodeKey, language string, result GenerateResult, openSubsExpected bool, openSubsCount, whisperxCount int) {
	if env == nil {
		return
	}
	if env.Attributes == nil {
		env.Attributes = make(map[string]any)
	}
	key := normalizeEpisodeKey(episodeKey)

	entry := map[string]any{
		"episode_key": key,
		"source":      strings.ToLower(strings.TrimSpace(result.Source)),
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
	if dec := strings.TrimSpace(result.OpenSubtitlesDecision); dec != "" {
		entry["opensubtitles_decision"] = dec
	}
	if detail := strings.TrimSpace(result.OpenSubtitlesDetail); detail != "" {
		entry["opensubtitles_detail"] = detail
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
		"opensubtitles":          openSubsCount,
		"whisperx":               whisperxCount,
		"expected_opensubtitles": openSubsExpected,
		"fallback_used":          openSubsExpected && whisperxCount > 0,
	}
}

func subtitleStageMessage(episodeCount, openSubsCount, whisperxCount int) string {
	base := "Generated subtitles"
	if episodeCount > 1 {
		base = fmt.Sprintf("Generated subtitles for %d episode(s)", episodeCount)
	}
	parts := make([]string, 0, 2)
	if openSubsCount > 0 {
		parts = append(parts, fmt.Sprintf("OpenSubtitles: %d", openSubsCount))
	}
	if whisperxCount > 0 {
		parts = append(parts, fmt.Sprintf("WhisperX: %d", whisperxCount))
	}
	if len(parts) == 0 {
		return base
	}
	return fmt.Sprintf("%s (%s)", base, strings.Join(parts, ", "))
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
	encoded, err := env.Encode()
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to encode rip spec after subtitle; metadata may be stale",
				logging.Error(err),
				logging.String(logging.FieldEventType, "rip_spec_encode_failed"),
				logging.String(logging.FieldErrorHint, "rerun identification if rip spec data looks wrong"),
				logging.String(logging.FieldImpact, "subtitle metadata may not reflect latest state"),
			)
		}
		return
	}
	itemCopy := *item
	itemCopy.RipSpecData = encoded
	if err := s.store.Update(ctx, &itemCopy); err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to persist rip spec after subtitle; metadata may be stale",
				logging.Error(err),
				logging.String(logging.FieldEventType, "rip_spec_persist_failed"),
				logging.String(logging.FieldErrorHint, "check queue database access"),
				logging.String(logging.FieldImpact, "subtitle metadata may not reflect latest state"),
			)
		}
	} else {
		*item = itemCopy
	}
}

// processGenerationResult handles logging and RipSpec update after successful generation.
func (s *Stage) processGenerationResult(ctx context.Context, item *queue.Item, target subtitleTarget, result GenerateResult, env *ripspec.Envelope, hasRipSpec, openSubsExpected bool, openSubsCount, aiCount int, ctxMeta SubtitleContext) {
	episodeKey := normalizeEpisodeKey(target.EpisodeKey)

	// Log fallback warning if OpenSubtitles was expected but WhisperX was used
	if openSubsExpected && strings.EqualFold(result.Source, "whisperx") &&
		result.OpenSubtitlesDecision != "force_ai" && result.OpenSubtitlesDecision != "skipped" {
		if s.logger != nil {
			s.logger.Warn("whisperx subtitle fallback used",
				logging.Int64("item_id", item.ID),
				logging.String("episode_key", episodeKey),
				logging.String("source_file", target.SourcePath),
				logging.String("subtitle_file", result.SubtitlePath),
				logging.String("opensubtitles_decision", result.OpenSubtitlesDecision),
				logging.String("opensubtitles_detail", result.OpenSubtitlesDetail),
				logging.Alert("subtitle_fallback"),
				logging.String(logging.FieldEventType, "subtitle_fallback"),
				logging.String(logging.FieldErrorHint, "verify OpenSubtitles metadata or use --forceai"),
				logging.String(logging.FieldImpact, "AI-generated subtitles used instead of OpenSubtitles"),
			)
		}
	}

	// Log generation decision
	if s.logger != nil {
		s.logger.Info("subtitle generation decision",
			logging.String(logging.FieldDecisionType, "subtitle_generation"),
			logging.String("decision_result", strings.ToLower(strings.TrimSpace(result.Source))),
			logging.String("decision_reason", strings.TrimSpace(result.OpenSubtitlesDecision)),
			logging.String("decision_options", "opensubtitles, whisperx"),
			logging.String("episode_key", episodeKey),
			logging.String("source_file", target.SourcePath),
			logging.String("subtitle_file", result.SubtitlePath),
			logging.Int("segments", result.SegmentCount),
			logging.String("subtitle_source", result.Source),
			logging.String("opensubtitles_decision", result.OpenSubtitlesDecision),
			logging.String("opensubtitles_detail", result.OpenSubtitlesDetail),
		)
	}

	// Update RipSpec if available (asset already added in main loop with status)
	if !hasRipSpec {
		return
	}
	recordSubtitleGeneration(env, episodeKey, ctxMeta.Language, result, openSubsExpected, openSubsCount, aiCount)
	s.persistRipSpec(ctx, item, env)
}
