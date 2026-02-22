package subtitles

import (
	"context"
	"fmt"
	"path/filepath"
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
	muxer   *Muxer
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
	if s.muxer != nil {
		s.muxer.SetLogger(logger)
	}
}

// NewStage constructs a workflow stage that generates subtitles for queue items.
func NewStage(store *queue.Store, service *Service, logger *slog.Logger) *Stage {
	stageLogger := logging.NewComponentLogger(logger, "subtitle-stage")
	return &Stage{
		store:   store,
		service: service,
		muxer:   NewMuxer(logger),
		logger:  stageLogger,
	}
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
	step := progressPercentForGen / float64(len(targets))
	var (
		successCount   int
		totalSegments  int
		skipped        int
		failedEpisodes int
		cachedCount    int
	)
	for idx, target := range targets {
		episodeKey := normalizeEpisodeKey(target.EpisodeKey)

		// Skip already-completed subtitled episodes (enables resume after partial failure)
		if asset, ok := env.Assets.FindAsset("subtitled", episodeKey); ok && asset.IsCompleted() {
			if s.logger != nil {
				s.logger.Debug("skipping already-subtitled episode",
					logging.String("episode_key", episodeKey),
					logging.String("subtitle_path", asset.Path),
				)
			}
			skipped++
			continue
		}

		item.ActiveEpisodeKey = episodeKey
		label := episodeProgressLabel(target)
		remaining := len(targets) - skipped
		current := idx + 1 - skipped
		message := fmt.Sprintf("Phase 2/2 - Generating subtitles (%d/%d – %s)", current, remaining, label)
		if err := s.updateProgress(ctx, item, message, progressPercentAfterPrep+step*float64(idx)); err != nil {
			return err
		}
		ctxMeta := buildEpisodeContext(baseCtx, target)

		var result GenerateResult
		cached := false
		if cachedResult, ok := s.tryReuseCachedTranscript(target, &env); ok {
			result = cachedResult
			cached = true
			cachedCount++
		} else {
			var err error
			result, err = s.service.Generate(ctx, GenerateRequest{
				SourcePath: target.SourcePath,
				WorkDir:    target.WorkDir,
				OutputDir:  target.OutputDir,
				BaseName:   target.BaseName,
				Context:    ctxMeta,
				Languages:  append([]string(nil), s.service.languages...),
			})
			if err != nil {
				errMessage := strings.TrimSpace(err.Error())
				if errMessage == "" {
					errMessage = "Subtitle generation failed"
				}
				if s.logger != nil {
					s.logger.Warn("subtitle generation failed for episode",
						logging.Int64("item_id", item.ID),
						logging.String("episode_key", episodeKey),
						logging.String("source_file", target.SourcePath),
						logging.Error(err),
						logging.String(logging.FieldEventType, "episode_subtitle_failed"),
						logging.String(logging.FieldErrorHint, "check WhisperX logs and retry"),
						logging.String(logging.FieldImpact, "subtitles will be missing for this episode, continuing with others"),
					)
				}
				// Record per-episode failure and continue to next episode
				env.Assets.AddAsset("subtitled", ripspec.Asset{
					EpisodeKey: target.EpisodeKey,
					TitleID:    target.TitleID,
					Path:       "",
					Status:     ripspec.AssetStatusFailed,
					ErrorMsg:   errMessage,
				})
				failedEpisodes++
				s.persistRipSpec(ctx, item, &env)
				continue
			}
		}
		// Validate generated SRT content
		if issues := ValidateSRTContent(result.SubtitlePath, result.Duration.Seconds()); len(issues) > 0 {
			if s.logger != nil {
				s.logger.Warn("SRT content validation issues",
					logging.String("episode_key", episodeKey),
					logging.String("subtitle_path", result.SubtitlePath),
					logging.String("issues", strings.Join(issues, "; ")),
					logging.String(logging.FieldEventType, "srt_validation_issues"),
					logging.String(logging.FieldErrorHint, "review subtitle file or regenerate"),
				)
			}
			// Continue with the subtitle but flag for review if there are issues
			item.NeedsReview = true
			if item.ReviewReason == "" {
				item.ReviewReason = fmt.Sprintf("SRT validation issues: %s", strings.Join(issues, "; "))
			}
		}

		successCount++
		totalSegments += result.SegmentCount

		// Check for forced subtitles if disc has forced subtitle tracks.
		// Pass the aligned regular subtitle as reference for subtitle-to-subtitle alignment.
		if forcedPath := s.tryForcedSubtitlesForTarget(ctx, item, target, ctxMeta, &env, result.SubtitlePath); forcedPath != "" {
			result.ForcedSubtitlePath = forcedPath
		}

		// Mux subtitles into MKV if configured
		subtitlesMuxed := false
		if s.shouldMuxSubtitles() {
			subtitlesMuxed = s.tryMuxSubtitles(ctx, target, result, &env, episodeKey)
		}

		// Mark as completed when adding successful asset
		env.Assets.AddAsset("subtitled", ripspec.Asset{
			EpisodeKey:     target.EpisodeKey,
			TitleID:        target.TitleID,
			Path:           result.SubtitlePath,
			Status:         ripspec.AssetStatusCompleted,
			SubtitlesMuxed: subtitlesMuxed,
		})
		s.processGenerationResult(ctx, item, target, result, &env, hasRipSpec, ctxMeta, cached)
	}

	// Determine final item status based on episode outcomes
	totalProcessed := len(targets) - skipped
	if failedEpisodes > 0 && successCount == 0 {
		return services.Wrap(services.ErrTransient, "subtitles", "execute",
			fmt.Sprintf("All %d episode(s) failed subtitle generation", totalProcessed), nil)
	}
	summaryParts := []string{fmt.Sprintf("%d episodes", successCount)}
	if cachedCount > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d cached", cachedCount))
	}
	summaryParts = append(summaryParts, fmt.Sprintf("%d segments", totalSegments))
	item.ProgressMessage = fmt.Sprintf("Subtitles generated (%s)", strings.Join(summaryParts, ", "))
	item.ProgressPercent = 100
	item.ErrorMessage = ""
	item.ActiveEpisodeKey = ""
	if err := s.store.UpdateProgress(ctx, item); err != nil {
		return services.Wrap(services.ErrTransient, "subtitles", "persist progress", "Failed to persist subtitle progress", err)
	}
	alertValue := ""
	if item.NeedsReview {
		alertValue = "review"
	}
	if s.logger != nil {
		summaryAttrs := []logging.Attr{
			logging.String(logging.FieldEventType, "stage_complete"),
			logging.Duration("stage_duration", time.Since(stageStart)),
			logging.Int("episodes", len(targets)),
			logging.Int("whisperx", successCount),
			logging.Int("cached", cachedCount),
			logging.Int("segments", totalSegments),
			logging.Bool("needs_review", item.NeedsReview),
		}
		if alertValue != "" {
			summaryAttrs = append(summaryAttrs, logging.Alert(alertValue))
			summaryAttrs = append(summaryAttrs,
				logging.String(logging.FieldImpact, "subtitle stage completed with review alerts"),
				logging.String(logging.FieldErrorHint, "Review subtitle results or rerun spindle gensubtitle for affected episodes"),
			)
			s.logger.Warn("subtitle stage summary", logging.Args(summaryAttrs...)...)
		} else {
			s.logger.Info("subtitle stage summary", logging.Args(summaryAttrs...)...)
		}
	}
	return nil
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

// HealthCheck reports readiness for the subtitle stage.
func (s *Stage) HealthCheck(ctx context.Context) stage.Health {
	const name = "subtitles"
	if s == nil || s.service == nil {
		return stage.Unhealthy(name, "stage not configured")
	}
	return stage.Healthy(name)
}
