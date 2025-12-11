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

// SetLogger allows the workflow manager to route stage logs into the item-scoped background log.
func (s *Stage) SetLogger(logger *slog.Logger) {
	if s == nil {
		return
	}
	stageLogger := logger
	if stageLogger != nil {
		stageLogger = stageLogger.With(logging.String("component", "subtitle-stage"))
	}
	s.logger = stageLogger
	if s.service != nil {
		s.service.SetLogger(logger)
	}
}

// NewStage constructs a workflow stage that generates subtitles for queue items.
func NewStage(store *queue.Store, service *Service, logger *slog.Logger) *Stage {
	stageLogger := logger
	if stageLogger != nil {
		stageLogger = stageLogger.With(logging.String("component", "subtitle-stage"))
	}
	return &Stage{store: store, service: service, logger: stageLogger}
}

// Prepare primes queue progress fields before executing the stage.
func (s *Stage) Prepare(ctx context.Context, item *queue.Item) error {
	if s == nil || s.service == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "prepare", "Subtitle stage is not configured", nil)
	}
	if !s.service.config.SubtitlesEnabled {
		return nil
	}
	if s.store == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "prepare", "Queue store unavailable", nil)
	}
	item.ProgressStage = progressStageGenerating
	item.ProgressMessage = "Preparing audio for transcription"
	item.ProgressPercent = 0
	item.ErrorMessage = ""
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
	if !s.service.config.SubtitlesEnabled {
		return nil
	}

	targets := s.buildSubtitleTargets(item)
	if len(targets) == 0 {
		return services.Wrap(services.ErrValidation, "subtitles", "execute", "No encoded assets available for subtitles", nil)
	}
	if err := s.updateProgress(ctx, item, fmt.Sprintf("Preparing subtitles for %d episode(s)", len(targets)), 5); err != nil {
		return err
	}

	baseCtx := BuildSubtitleContext(item)
	step := 90.0 / float64(len(targets))
	var (
		openSubsCount int
		aiCount       int
		totalSegments int
	)
	for idx, target := range targets {
		message := fmt.Sprintf("Generating subtitles %d/%d – %s", idx+1, len(targets), filepath.Base(target.SourcePath))
		if err := s.updateProgress(ctx, item, message, 5.0+step*float64(idx)); err != nil {
			return err
		}
		ctxMeta := baseCtx
		if target.Season > 0 {
			ctxMeta.Season = target.Season
		}
		if target.Episode > 0 {
			ctxMeta.Episode = target.Episode
		}
		if strings.TrimSpace(target.EpisodeTitle) != "" {
			baseTitle := strings.TrimSpace(baseCtx.Title)
			episodeTitle := strings.TrimSpace(target.EpisodeTitle)
			if baseTitle != "" {
				ctxMeta.Title = fmt.Sprintf("%s – %s", baseTitle, episodeTitle)
			} else {
				ctxMeta.Title = episodeTitle
			}
		}
		if ctxMeta.ContentKey == "" {
			ctxMeta.ContentKey = target.EpisodeKey
		}
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
						s.logger.Warn("misidentification handling failed", logging.Error(handleErr))
					}
					if s.logger != nil {
						s.logger.Warn("subtitle generation flagged for review",
							logging.Int64("item_id", item.ID),
							logging.String("source", target.SourcePath),
							logging.Float64("median_delta_seconds", suspect.medianAbsDelta()),
							logging.Alert("review"),
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
						logging.String("source", target.SourcePath),
						logging.Error(err),
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
		if s.logger != nil {
			s.logger.Debug("subtitle generation complete",
				logging.String("source", target.SourcePath),
				logging.String("subtitle", result.SubtitlePath),
				logging.Int("segments", result.SegmentCount),
				logging.String("subtitle_source", result.Source),
			)
		}
	}
	item.ProgressMessage = fmt.Sprintf("Generated subtitles for %d episode(s)", len(targets))
	item.ProgressPercent = 100
	item.ErrorMessage = ""
	if err := s.store.UpdateProgress(ctx, item); err != nil {
		return services.Wrap(services.ErrTransient, "subtitles", "persist progress", "Failed to persist subtitle progress", err)
	}
	fallbackEpisodes := len(targets) - openSubsCount
	openSubsExpected := s.service != nil && s.service.shouldUseOpenSubtitles()
	alertValue := ""
	if item.NeedsReview {
		alertValue = "review"
	} else if fallbackEpisodes > 0 && openSubsExpected {
		alertValue = "subtitle_fallback"
	}
	if s.logger != nil {
		summaryAttrs := []logging.Attr{
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
			s.logger.Warn("subtitle stage summary", logging.Args(summaryAttrs...)...)
		} else {
			s.logger.Info("subtitle stage summary", logging.Args(summaryAttrs...)...)
		}
	}
	return nil
}

type subtitleTarget struct {
	SourcePath   string
	WorkDir      string
	OutputDir    string
	BaseName     string
	EpisodeKey   string
	EpisodeTitle string
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

func (s *Stage) buildSubtitleTargets(item *queue.Item) []subtitleTarget {
	if item == nil || s == nil || s.service == nil {
		return nil
	}
	stagingRoot := strings.TrimSpace(item.StagingRoot(s.service.config.StagingDir))
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
		s.logger.Warn("failed to parse rip spec for subtitle targets", logging.Error(err))
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
	if !s.service.config.SubtitlesEnabled {
		return stage.Healthy("subtitles")
	}
	return stage.Healthy("subtitles")
}
