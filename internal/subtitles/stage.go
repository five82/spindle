package subtitles

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/stage"
)

// Stage integrates subtitle generation with the workflow manager.
type Stage struct {
	store   *queue.Store
	service *Service
	logger  *slog.Logger
}

// SetLogger allows the workflow manager to route stage logs into the item-scoped log.
func (s *Stage) SetLogger(logger *slog.Logger) {
	if s == nil {
		return
	}
	s.logger = logging.NewComponentLogger(logger, "subtitle-stage")
	if s.service != nil {
		s.service.SetLogger(logger)
	}
}

// NewStage constructs a workflow stage that generates subtitles for queue items.
func NewStage(store *queue.Store, service *Service, logger *slog.Logger) *Stage {
	return &Stage{store: store, service: service, logger: logging.NewComponentLogger(logger, "subtitle-stage")}
}

// Prepare primes queue progress fields before executing the stage.
func (s *Stage) Prepare(ctx context.Context, item *queue.Item) error {
	if s == nil || s.service == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "prepare", "Subtitle stage is not configured", nil)
	}
	if !s.service.config.Subtitles.Enabled {
		return nil
	}
	if s.store == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "prepare", "Queue store unavailable", nil)
	}
	item.InitProgress(progressStageGenerating, "Phase 1/2 - Preparing audio")
	return s.store.UpdateProgress(ctx, item)
}

// Execute performs subtitle generation for the queue item.
func (s *Stage) Execute(ctx context.Context, item *queue.Item) error {
	stageStart := time.Now()
	if s == nil || s.service == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "execute", "Subtitle stage is not configured", nil)
	}
	if item == nil {
		return services.Wrap(services.ErrValidation, "subtitles", "execute", "Queue item is nil", nil)
	}
	if s.store == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "execute", "Queue store unavailable", nil)
	}
	if !s.service.config.Subtitles.Enabled {
		return nil
	}

	var env ripspec.Envelope
	hasRipSpec := false
	if raw := strings.TrimSpace(item.RipSpecData); raw != "" {
		if parsed, err := ripspec.Parse(raw); err == nil {
			env = parsed
			hasRipSpec = true
		} else if s.logger != nil {
			s.logger.Warn("failed to parse rip spec for subtitle progress; continuing without progress details",
				logging.Error(err),
				logging.String(logging.FieldEventType, "rip_spec_parse_failed"),
				logging.String(logging.FieldErrorHint, "rerun identification if subtitle progress looks wrong"),
				logging.String(logging.FieldImpact, "progress display may be inaccurate"),
			)
		}
	}

	targets := s.buildSubtitleTargets(item)
	if len(targets) == 0 {
		return services.Wrap(services.ErrValidation, "subtitles", "execute", "No encoded assets available for subtitles", nil)
	}
	if err := s.updateProgress(ctx, item, fmt.Sprintf("Phase 1/2 - Preparing subtitles (%d episodes)", len(targets)), progressPercentAfterPrep); err != nil {
		return err
	}

	baseCtx := BuildSubtitleContext(item)
	openSubsExpected := s.service != nil && s.service.shouldUseOpenSubtitles()
	step := progressPercentForGen / float64(len(targets))
	var (
		openSubsCount int
		aiCount       int
		totalSegments int
	)
	for idx, target := range targets {
		item.ActiveEpisodeKey = strings.ToLower(strings.TrimSpace(target.EpisodeKey))
		label := episodeProgressLabel(target)
		message := fmt.Sprintf("Phase 2/2 - Generating subtitles (%d/%d – %s)", idx+1, len(targets), label)
		if err := s.updateProgress(ctx, item, message, progressPercentAfterPrep+step*float64(idx)); err != nil {
			return err
		}
		ctxMeta := buildEpisodeContext(baseCtx, target)
		cacheKey := BuildTranscriptCacheKey(item.ID, target.EpisodeKey)
		result, err := s.service.Generate(ctx, GenerateRequest{
			SourcePath:                target.SourcePath,
			WorkDir:                   target.WorkDir,
			OutputDir:                 target.OutputDir,
			BaseName:                  target.BaseName,
			Context:                   ctxMeta,
			Languages:                 append([]string(nil), s.service.languages...),
			TranscriptKey:             cacheKey,
			AllowTranscriptCacheRead:  cacheKey != "",
			AllowTranscriptCacheWrite: cacheKey != "",
		})
		if err != nil {
			var suspect suspectMisIdentificationError
			if errors.As(err, &suspect) {
				if handled, retryResult, handleErr := s.handleSuspectMisID(ctx, item, target, ctxMeta, suspect); handleErr == nil && handled {
					result = retryResult
				} else {
					if handleErr != nil && s.logger != nil {
						s.logger.Warn("misidentification handling failed; review required",
							logging.Error(handleErr),
							logging.String(logging.FieldEventType, "subtitle_misidentification_handle_failed"),
							logging.String(logging.FieldErrorHint, "review subtitle offsets and metadata"),
							logging.String(logging.FieldImpact, "item routed to review for manual inspection"),
						)
					}
					if s.logger != nil {
						s.logger.Warn("subtitle generation flagged for review",
							logging.Int64("item_id", item.ID),
							logging.String("source_file", target.SourcePath),
							logging.Float64("median_delta_seconds", suspect.medianAbsDelta()),
							logging.Alert("review"),
							logging.String(logging.FieldEventType, "subtitle_review_required"),
							logging.String(logging.FieldErrorHint, "review subtitle offsets and metadata"),
							logging.String(logging.FieldImpact, "item diverted to review queue"),
						)
					}
					item.NeedsReview = true
					item.ReviewReason = "suspect mis-identification from subtitle offsets"
					item.ProgressMessage = "Subtitles diverted to review (suspect mis-identification)"
					item.ProgressPercent = 100
					if err := s.store.Update(ctx, item); err != nil {
						return services.Wrap(services.ErrTransient, "subtitles", "persist review", "Failed to persist review flag", err)
					}
					return nil
				}
			} else {
				message := strings.TrimSpace(err.Error())
				if message == "" {
					message = "Subtitle generation failed"
				}
				if s.logger != nil {
					s.logger.Warn("subtitle generation skipped",
						logging.Int64("item_id", item.ID),
						logging.String("source_file", target.SourcePath),
						logging.Error(err),
						logging.String(logging.FieldEventType, "subtitle_generation_skipped"),
						logging.String(logging.FieldErrorHint, "check WhisperX/OpenSubtitles logs and retry"),
						logging.String(logging.FieldImpact, "subtitles will be missing for this episode"),
					)
				}
				item.ProgressMessage = fmt.Sprintf("Subtitle generation skipped: %s", message)
				item.ProgressPercent = 100
				item.ErrorMessage = message
				if err := s.store.UpdateProgress(ctx, item); err != nil {
					return services.Wrap(services.ErrTransient, "subtitles", "persist skip", "Failed to persist subtitle skip status", err)
				}
				return nil
			}
		}
		if strings.EqualFold(result.Source, "opensubtitles") {
			openSubsCount++
		} else {
			aiCount++
		}
		totalSegments += result.SegmentCount
		s.processGenerationResult(ctx, item, target, result, &env, hasRipSpec, openSubsExpected, openSubsCount, aiCount, ctxMeta)
	}
	item.ProgressMessage = subtitleStageMessage(len(targets), openSubsCount, aiCount)
	item.ProgressPercent = 100
	item.ErrorMessage = ""
	item.ActiveEpisodeKey = ""
	if err := s.store.UpdateProgress(ctx, item); err != nil {
		return services.Wrap(services.ErrTransient, "subtitles", "persist progress", "Failed to persist subtitle progress", err)
	}
	fallbackEpisodes := len(targets) - openSubsCount
	alertValue := ""
	if item.NeedsReview {
		alertValue = "review"
	} else if fallbackEpisodes > 0 && openSubsExpected {
		alertValue = "subtitle_fallback"
	}
	if s.logger != nil {
		summaryAttrs := []logging.Attr{
			logging.String(logging.FieldEventType, "stage_complete"),
			logging.Duration("stage_duration", time.Since(stageStart)),
			logging.Int("episodes", len(targets)),
			logging.Int("opensubtitles", openSubsCount),
			logging.Int("whisperx_fallback", aiCount),
			logging.Int("segments", totalSegments),
			logging.Bool("needs_review", item.NeedsReview),
			logging.Int("opensubtitles_missing", fallbackEpisodes),
			logging.Bool("opensubtitles_expected", openSubsExpected),
		}
		if alertValue != "" {
			summaryAttrs = append(summaryAttrs, logging.Alert(alertValue))
			summaryAttrs = append(summaryAttrs,
				logging.String(logging.FieldImpact, "subtitle stage completed with review or fallback alerts"),
				logging.String(logging.FieldErrorHint, "Review subtitle results or rerun spindle gensubtitle for affected episodes"),
			)
			s.logger.Warn("subtitle stage summary", logging.Args(summaryAttrs...)...)
		} else {
			s.logger.Info("subtitle stage summary", logging.Args(summaryAttrs...)...)
		}
	}
	return nil
}

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
	key := strings.ToLower(strings.TrimSpace(episodeKey))
	if key == "" {
		key = "primary"
	}

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

type subtitleTarget struct {
	SourcePath   string
	WorkDir      string
	OutputDir    string
	BaseName     string
	EpisodeKey   string
	EpisodeTitle string
	TitleID      int
	Season       int
	Episode      int
}

func (s *Stage) updateProgress(ctx context.Context, item *queue.Item, message string, percent float64) error {
	item.ProgressStage = progressStageGenerating
	if strings.TrimSpace(message) != "" {
		item.ProgressMessage = message
	}
	if percent >= 0 {
		item.ProgressPercent = percent
	}
	if err := s.store.UpdateProgress(ctx, item); err != nil {
		return services.Wrap(services.ErrTransient, "subtitles", "persist progress", "Failed to persist subtitle progress", err)
	}
	return nil
}

func (s *Stage) handleSuspectMisID(ctx context.Context, item *queue.Item, target subtitleTarget, ctxMeta SubtitleContext, suspect suspectMisIdentificationError) (bool, GenerateResult, error) {
	// Best-effort auto-fix: fall back to local WhisperX generation (no OpenSubtitles)
	result, err := s.service.Generate(ctx, GenerateRequest{
		SourcePath:                target.SourcePath,
		WorkDir:                   target.WorkDir,
		OutputDir:                 target.OutputDir,
		BaseName:                  target.BaseName,
		Context:                   ctxMeta,
		Languages:                 append([]string(nil), s.service.languages...),
		ForceAI:                   true,
		TranscriptKey:             BuildTranscriptCacheKey(item.ID, target.EpisodeKey),
		AllowTranscriptCacheRead:  true,
		AllowTranscriptCacheWrite: true,
	})
	if err != nil {
		return false, GenerateResult{}, err
	}
	return true, result, nil
}

// buildEpisodeContext creates episode-specific subtitle context from base context.
func buildEpisodeContext(baseCtx SubtitleContext, target subtitleTarget) SubtitleContext {
	ctx := baseCtx
	if target.Season > 0 {
		ctx.Season = target.Season
	}
	if target.Episode > 0 {
		ctx.Episode = target.Episode
	}
	if strings.TrimSpace(target.EpisodeTitle) != "" {
		baseTitle := strings.TrimSpace(baseCtx.Title)
		episodeTitle := strings.TrimSpace(target.EpisodeTitle)
		if baseTitle != "" {
			ctx.Title = fmt.Sprintf("%s – %s", baseTitle, episodeTitle)
		} else {
			ctx.Title = episodeTitle
		}
	}
	if ctx.ContentKey == "" {
		ctx.ContentKey = target.EpisodeKey
	}
	return ctx
}

// episodeProgressLabel builds a display label for the current episode.
func episodeProgressLabel(target subtitleTarget) string {
	if target.Season > 0 && target.Episode > 0 {
		return fmt.Sprintf("S%02dE%02d", target.Season, target.Episode)
	}
	if key := strings.TrimSpace(target.EpisodeKey); key != "" {
		return strings.ToUpper(key)
	}
	return filepath.Base(target.SourcePath)
}

// processGenerationResult handles logging and RipSpec update after successful generation.
func (s *Stage) processGenerationResult(ctx context.Context, item *queue.Item, target subtitleTarget, result GenerateResult, env *ripspec.Envelope, hasRipSpec, openSubsExpected bool, openSubsCount, aiCount int, ctxMeta SubtitleContext) {
	episodeKey := strings.ToLower(strings.TrimSpace(target.EpisodeKey))
	if episodeKey == "" {
		episodeKey = "primary"
	}

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

	// Update RipSpec if available
	if !hasRipSpec {
		return
	}
	if strings.TrimSpace(target.EpisodeKey) != "" {
		env.Assets.AddAsset("subtitled", ripspec.Asset{EpisodeKey: target.EpisodeKey, TitleID: target.TitleID, Path: result.SubtitlePath})
	}
	recordSubtitleGeneration(env, episodeKey, ctxMeta.Language, result, openSubsExpected, openSubsCount, aiCount)
	encoded, err := env.Encode()
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to encode rip spec after subtitles; metadata may be stale",
				logging.Error(err),
				logging.String(logging.FieldEventType, "rip_spec_encode_failed"),
				logging.String(logging.FieldErrorHint, "rerun identification if rip spec data looks wrong"),
				logging.String(logging.FieldImpact, "subtitle metadata may not reflect latest state"),
			)
		}
		return
	}
	copy := *item
	copy.RipSpecData = encoded
	if err := s.store.Update(ctx, &copy); err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to persist rip spec after subtitles; metadata may be stale",
				logging.Error(err),
				logging.String(logging.FieldEventType, "rip_spec_persist_failed"),
				logging.String(logging.FieldErrorHint, "check queue database access"),
				logging.String(logging.FieldImpact, "subtitle metadata may not reflect latest state"),
			)
		}
	} else {
		*item = copy
	}
}

func (s *Stage) buildSubtitleTargets(item *queue.Item) []subtitleTarget {
	if item == nil || s == nil || s.service == nil {
		return nil
	}
	stagingRoot := strings.TrimSpace(item.StagingRoot(s.service.config.Paths.StagingDir))
	if stagingRoot == "" {
		stagingRoot = filepath.Dir(strings.TrimSpace(item.EncodedFile))
	}
	if stagingRoot == "" {
		stagingRoot = "."
	}
	baseWorkDir := filepath.Join(stagingRoot, "subtitles")
	var targets []subtitleTarget

	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil && s.logger != nil {
		s.logger.Warn("failed to parse rip spec for subtitle targets; continuing with encoded file fallback",
			logging.Error(err),
			logging.String(logging.FieldEventType, "rip_spec_parse_failed"),
			logging.String(logging.FieldErrorHint, "rerun identification if subtitle targets look wrong"),
			logging.String(logging.FieldImpact, "subtitle targets determined from encoded file instead of rip spec"),
		)
	}
	if len(env.Assets.Encoded) > 0 {
		for idx, asset := range env.Assets.Encoded {
			source := strings.TrimSpace(asset.Path)
			if source == "" {
				continue
			}
			episodeKey := strings.TrimSpace(asset.EpisodeKey)
			season, episode := parseEpisodeKey(episodeKey)
			episodeTitle := ""
			if ep := env.EpisodeByKey(episodeKey); ep != nil {
				if ep.Season > 0 {
					season = ep.Season
				}
				if ep.Episode > 0 {
					episode = ep.Episode
				}
				if strings.TrimSpace(ep.Key) != "" && episodeKey == "" {
					episodeKey = strings.TrimSpace(ep.Key)
				}
				episodeTitle = strings.TrimSpace(ep.EpisodeTitle)
			}
			targets = append(targets, subtitleTarget{
				SourcePath:   source,
				WorkDir:      filepath.Join(baseWorkDir, sanitizeEpisodeToken(episodeKey, idx)),
				OutputDir:    filepath.Dir(source),
				BaseName:     baseNameWithoutExt(source),
				EpisodeKey:   episodeKey,
				EpisodeTitle: episodeTitle,
				TitleID:      asset.TitleID,
				Season:       season,
				Episode:      episode,
			})
		}
	}
	if len(targets) == 0 {
		source := strings.TrimSpace(item.EncodedFile)
		if source == "" {
			return nil
		}
		targets = append(targets, subtitleTarget{
			SourcePath: source,
			WorkDir:    filepath.Join(baseWorkDir, "primary"),
			OutputDir:  filepath.Dir(source),
			BaseName:   baseNameWithoutExt(source),
		})
	}
	return targets
}

var episodeKeyPattern = regexp.MustCompile(`s?(\d+)[ex](\d+)`)

func parseEpisodeKey(key string) (int, int) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return 0, 0
	}
	matches := episodeKeyPattern.FindStringSubmatch(key)
	if len(matches) != 3 {
		return 0, 0
	}
	season, _ := strconv.Atoi(matches[1])
	episode, _ := strconv.Atoi(matches[2])
	return season, episode
}

func sanitizeEpisodeToken(key string, idx int) string {
	token := strings.TrimSpace(key)
	if token == "" {
		token = fmt.Sprintf("episode-%d", idx+1)
	}
	token = strings.ToLower(token)
	replacer := strings.NewReplacer(
		" ", "_",
		"/", "_",
		"\\", "_",
		":", "_",
		"..", "_",
	)
	return replacer.Replace(token)
}

// HealthCheck reports readiness for the subtitle stage.
func (s *Stage) HealthCheck(ctx context.Context) stage.Health {
	if s == nil || s.service == nil {
		return stage.Unhealthy("subtitles", "stage not configured")
	}
	if !s.service.config.Subtitles.Enabled {
		return stage.Healthy("subtitles")
	}
	return stage.Healthy("subtitles")
}
