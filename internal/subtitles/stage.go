package subtitles

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"log/slog"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/services"
	"spindle/internal/stage"
)

// Stage integrates subtitle generation with the workflow manager.
type Stage struct {
	store   *queue.Store
	service *Service
	logger  *slog.Logger
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
	if s == nil || s.service == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "execute", "Subtitle stage is not configured", nil)
	}
	if item == nil {
		return services.Wrap(services.ErrValidation, "subtitles", "execute", "Queue item is nil", nil)
	}
	if s.store == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "execute", "Queue store unavailable", nil)
	}
	if strings.TrimSpace(item.EncodedFile) == "" {
		return services.Wrap(services.ErrValidation, "subtitles", "execute", "No encoded file available for subtitles", nil)
	}
	if !s.service.config.SubtitlesEnabled {
		return nil
	}

	if err := s.updateProgress(ctx, item, "Running WhisperX transcription", 5); err != nil {
		return err
	}
	workDir := item.StagingRoot(s.service.config.StagingDir)
	outputDir := filepath.Dir(strings.TrimSpace(item.EncodedFile))
	result, err := s.service.Generate(ctx, GenerateRequest{
		SourcePath: item.EncodedFile,
		WorkDir:    filepath.Join(workDir, "subtitles"),
		OutputDir:  outputDir,
		BaseName:   baseNameWithoutExt(item.EncodedFile),
	})
	if err != nil {
		message := strings.TrimSpace(err.Error())
		if message == "" {
			message = "Subtitle generation failed"
		}
		if s.logger != nil {
			s.logger.Warn("subtitle generation skipped", logging.Int64("item_id", item.ID), logging.Error(err))
		}
		item.ProgressMessage = fmt.Sprintf("Subtitle generation skipped: %s", message)
		item.ProgressPercent = 100
		item.ErrorMessage = message
		if err := s.store.UpdateProgress(ctx, item); err != nil {
			return services.Wrap(services.ErrTransient, "subtitles", "persist skip", "Failed to persist subtitle skip status", err)
		}
		return nil
	}
	item.ProgressMessage = fmt.Sprintf("Generated subtitles: %s", filepath.Base(result.SubtitlePath))
	item.ProgressPercent = 100
	if err := s.store.UpdateProgress(ctx, item); err != nil {
		return services.Wrap(services.ErrTransient, "subtitles", "persist progress", "Failed to persist subtitle progress", err)
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
