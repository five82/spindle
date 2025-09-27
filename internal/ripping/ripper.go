package ripping

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/disc"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/services"
	"spindle/internal/services/makemkv"
	"spindle/internal/workflow"
)

// Ripper manages the MakeMKV ripping workflow.
type Ripper struct {
	store    *queue.Store
	cfg      *config.Config
	logger   *zap.Logger
	client   makemkv.Ripper
	ejector  disc.Ejector
	notifier notifications.Service
}

// NewRipper constructs the ripping handler using default dependencies.
func NewRipper(cfg *config.Config, store *queue.Store, logger *zap.Logger) *Ripper {
	client, err := makemkv.New(cfg.MakemkvBinary(), cfg.MakeMKVRipTimeout)
	if err != nil {
		logger.Warn("makemkv client unavailable", zap.Error(err))
	}
	return NewRipperWithDependencies(cfg, store, logger, client, disc.NewEjector(), notifications.NewService(cfg))
}

// NewRipperWithClient keeps backwards compatibility for tests using only a client override.
func NewRipperWithClient(cfg *config.Config, store *queue.Store, logger *zap.Logger, client makemkv.Ripper) *Ripper {
	return NewRipperWithDependencies(cfg, store, logger, client, disc.NewEjector(), notifications.NewService(cfg))
}

// NewRipperWithDependencies allows injecting all collaborators (used in tests).
func NewRipperWithDependencies(cfg *config.Config, store *queue.Store, logger *zap.Logger, client makemkv.Ripper, ejector disc.Ejector, notifier notifications.Service) *Ripper {
	stageLogger := logger
	if stageLogger != nil {
		stageLogger = stageLogger.With(zap.String("component", "ripper"))
	}
	return &Ripper{store: store, cfg: cfg, logger: stageLogger, client: client, ejector: ejector, notifier: notifier}
}

func (r *Ripper) Name() string { return "ripper" }

func (r *Ripper) TriggerStatus() queue.Status { return queue.StatusIdentified }

func (r *Ripper) ProcessingStatus() queue.Status { return queue.StatusRipping }

func (r *Ripper) NextStatus() queue.Status { return queue.StatusRipped }

func (r *Ripper) Prepare(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, r.logger)
	if item.ProgressStage == "" {
		item.ProgressStage = "Ripping"
	}
	item.ProgressMessage = "Starting rip"
	item.ProgressPercent = 0
	item.ErrorMessage = ""
	if r.notifier != nil {
		if err := r.notifier.NotifyRipStarted(ctx, item.DiscTitle); err != nil {
			logger.Warn("failed to send rip start notification", zap.Error(err))
		}
	}
	return nil
}

func (r *Ripper) Execute(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, r.logger)
	var target string
	progressCB := func(update makemkv.ProgressUpdate) {
		r.applyProgress(ctx, item, update)
	}

	if r.client != nil {
		destDir := filepath.Join(r.cfg.StagingDir, "rips")
		path, err := r.client.Rip(ctx, item.DiscTitle, item.SourcePath, destDir, progressCB)
		if err != nil {
			return services.WithHint(
				services.Wrap(services.ErrorExternalTool, "ripping", "makemkv rip", "MakeMKV rip failed", err),
				"Check MakeMKV installation and disc readability",
			)
		}
		target = path
	}

	if target == "" {
		if err := os.MkdirAll(r.cfg.StagingDir, 0o755); err != nil {
			return services.WithHint(
				services.Wrap(services.ErrorConfiguration, "ripping", "ensure staging dir", "Failed to create staging directory", err),
				"Set staging_dir to a writable location",
			)
		}
		cleaned := sanitizeFileName(item.DiscTitle)
		if cleaned == "" {
			cleaned = "spindle-disc"
		}
		target = filepath.Join(r.cfg.StagingDir, cleaned+".mkv")
		if item.SourcePath != "" {
			if err := copyPlaceholder(item.SourcePath, target); err != nil {
				return services.Wrap(services.ErrorTransient, "ripping", "prepare placeholder", "Failed to copy source into staging", err)
			}
		} else if err := os.WriteFile(target, []byte("placeholder rip"), 0o644); err != nil {
			return services.Wrap(services.ErrorTransient, "ripping", "write placeholder", "Failed to write placeholder rip", err)
		}
	}

	item.RippedFile = target
	item.ProgressStage = "Ripped"
	item.ProgressPercent = 100
	item.ProgressMessage = "Disc content ripped"

	if r.ejector != nil {
		if err := r.ejector.Eject(ctx, r.cfg.OpticalDrive); err != nil {
			logger.Warn("failed to eject disc", zap.Error(err))
		}
	}
	if r.notifier != nil {
		if err := r.notifier.NotifyRipCompleted(ctx, item.DiscTitle); err != nil {
			logger.Warn("rip completion notification failed", zap.Error(err))
		}
	}

	return nil
}

func (r *Ripper) Rollback(ctx context.Context, item *queue.Item, stageErr error) error {
	return nil
}

var _ workflow.Stage = (*Ripper)(nil)

// HealthCheck verifies MakeMKV ripping dependencies.
func (r *Ripper) HealthCheck(ctx context.Context) workflow.StageHealth {
	name := r.Name()
	if r.cfg == nil {
		return workflow.UnhealthyStage(name, "configuration unavailable")
	}
	if strings.TrimSpace(r.cfg.StagingDir) == "" {
		return workflow.UnhealthyStage(name, "staging directory not configured")
	}
	if strings.TrimSpace(r.cfg.OpticalDrive) == "" {
		return workflow.UnhealthyStage(name, "optical drive not configured")
	}
	if r.client == nil {
		return workflow.UnhealthyStage(name, "makemkv client unavailable")
	}
	if r.ejector == nil {
		return workflow.UnhealthyStage(name, "disc ejector unavailable")
	}
	return workflow.HealthyStage(name)
}

func (r *Ripper) applyProgress(ctx context.Context, item *queue.Item, update makemkv.ProgressUpdate) {
	logger := logging.WithContext(ctx, r.logger)
	copy := *item
	if update.Stage != "" {
		copy.ProgressStage = update.Stage
	}
	if update.Percent >= 0 {
		copy.ProgressPercent = update.Percent
	}
	if update.Message != "" {
		copy.ProgressMessage = update.Message
	}
	if err := r.store.Update(ctx, &copy); err != nil {
		logger.Warn("failed to persist progress", zap.Error(err))
		return
	}
	*item = copy
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-", "?", "", "\"", "", "<", "", ">", "", "|", "")
	return strings.TrimSpace(replacer.Replace(name))
}

func copyPlaceholder(src, dst string) error {
	sourceData, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read source file: %w", err)
	}
	if err := os.WriteFile(dst, sourceData, 0o644); err != nil {
		return fmt.Errorf("write placeholder file: %w", err)
	}
	return nil
}
