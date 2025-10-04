package ripping

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/disc"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/services"
	"spindle/internal/services/makemkv"
	"spindle/internal/stage"
)

// Ripper manages the MakeMKV ripping workflow.
type Ripper struct {
	store    *queue.Store
	cfg      *config.Config
	logger   *slog.Logger
	client   makemkv.Ripper
	ejector  disc.Ejector
	notifier notifications.Service
}

// NewRipper constructs the ripping handler using default dependencies.
func NewRipper(cfg *config.Config, store *queue.Store, logger *slog.Logger) *Ripper {
	client, err := makemkv.New(cfg.MakemkvBinary(), cfg.MakeMKVRipTimeout)
	if err != nil {
		logger.Warn("makemkv client unavailable", logging.Error(err))
	}
	return NewRipperWithDependencies(cfg, store, logger, client, disc.NewEjector(), notifications.NewService(cfg))
}

// NewRipperWithClient keeps backwards compatibility for tests using only a client override.
func NewRipperWithClient(cfg *config.Config, store *queue.Store, logger *slog.Logger, client makemkv.Ripper) *Ripper {
	return NewRipperWithDependencies(cfg, store, logger, client, disc.NewEjector(), notifications.NewService(cfg))
}

// NewRipperWithDependencies allows injecting all collaborators (used in tests).
func NewRipperWithDependencies(cfg *config.Config, store *queue.Store, logger *slog.Logger, client makemkv.Ripper, ejector disc.Ejector, notifier notifications.Service) *Ripper {
	stageLogger := logger
	if stageLogger != nil {
		stageLogger = stageLogger.With(logging.String("component", "ripper"))
	}
	return &Ripper{store: store, cfg: cfg, logger: stageLogger, client: client, ejector: ejector, notifier: notifier}
}

func (r *Ripper) Prepare(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, r.logger)
	if item.ProgressStage == "" {
		item.ProgressStage = "Ripping"
	}
	item.ProgressMessage = "Starting rip"
	item.ProgressPercent = 0
	item.ErrorMessage = ""
	logger.Info(
		"starting rip preparation",
		logging.String("disc_title", strings.TrimSpace(item.DiscTitle)),
		logging.String("source_path", strings.TrimSpace(item.SourcePath)),
	)
	if r.notifier != nil {
		if err := r.notifier.Publish(ctx, notifications.EventRipStarted, notifications.Payload{"discTitle": item.DiscTitle}); err != nil {
			logger.Warn("failed to send rip start notification", logging.Error(err))
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
	destDir := filepath.Join(r.cfg.StagingDir, "rips")
	logger.Info(
		"starting rip execution",
		logging.String("disc_title", strings.TrimSpace(item.DiscTitle)),
		logging.String("destination_dir", destDir),
		logging.Bool("makemkv_enabled", r.client != nil),
	)

	if r.client != nil {
		logger.Info("launching makemkv rip", logging.String("destination_dir", destDir))
		path, err := r.client.Rip(ctx, item.DiscTitle, item.SourcePath, destDir, progressCB)
		if err != nil {
			return services.Wrap(
				services.ErrExternalTool,
				"ripping",
				"makemkv rip",
				"MakeMKV rip failed; check MakeMKV installation and disc readability",
				err,
			)
		}
		target = path
		logger.Info("makemkv rip finished", logging.String("ripped_file", target))
	}

	if target == "" {
		if err := os.MkdirAll(r.cfg.StagingDir, 0o755); err != nil {
			return services.Wrap(
				services.ErrConfiguration,
				"ripping",
				"ensure staging dir",
				"Failed to create staging directory; set staging_dir to a writable location",
				err,
			)
		}
		cleaned := sanitizeFileName(item.DiscTitle)
		if cleaned == "" {
			cleaned = "spindle-disc"
		}
		target = filepath.Join(r.cfg.StagingDir, cleaned+".mkv")
		if item.SourcePath != "" {
			if err := copyPlaceholder(item.SourcePath, target); err != nil {
				return services.Wrap(services.ErrTransient, "ripping", "prepare placeholder", "Failed to copy source into staging", err)
			}
		} else if err := os.WriteFile(target, []byte("placeholder rip"), 0o644); err != nil {
			return services.Wrap(services.ErrTransient, "ripping", "write placeholder", "Failed to write placeholder rip", err)
		}
		logger.Info("created placeholder rip output", logging.String("ripped_file", target))
	}

	item.RippedFile = target
	item.ProgressStage = "Ripped"
	item.ProgressPercent = 100
	item.ProgressMessage = "Disc content ripped"
	logger.Info("ripping completed", logging.String("ripped_file", target))

	if r.ejector != nil {
		logger.Info("ejecting disc", logging.String("device", strings.TrimSpace(r.cfg.OpticalDrive)))
		if err := r.ejector.Eject(ctx, r.cfg.OpticalDrive); err != nil {
			logger.Warn("failed to eject disc", logging.Error(err))
		}
	}
	if r.notifier != nil {
		if err := r.notifier.Publish(ctx, notifications.EventRipCompleted, notifications.Payload{"discTitle": item.DiscTitle}); err != nil {
			logger.Warn("rip completion notification failed", logging.Error(err))
		}
	}

	return nil
}

// HealthCheck verifies MakeMKV ripping dependencies.
func (r *Ripper) HealthCheck(ctx context.Context) stage.Health {
	const name = "ripper"
	if r.cfg == nil {
		return stage.Unhealthy(name, "configuration unavailable")
	}
	if strings.TrimSpace(r.cfg.StagingDir) == "" {
		return stage.Unhealthy(name, "staging directory not configured")
	}
	if strings.TrimSpace(r.cfg.OpticalDrive) == "" {
		return stage.Unhealthy(name, "optical drive not configured")
	}
	if r.client == nil {
		return stage.Unhealthy(name, "makemkv client unavailable")
	}
	binary := strings.TrimSpace(r.cfg.MakemkvBinary())
	if binary == "" {
		return stage.Unhealthy(name, "makemkv binary not configured")
	}
	if _, err := exec.LookPath(binary); err != nil {
		return stage.Unhealthy(name, fmt.Sprintf("makemkv binary %q not found", binary))
	}
	if r.ejector == nil {
		return stage.Unhealthy(name, "disc ejector unavailable")
	}
	return stage.Healthy(name)
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
		logger.Warn("failed to persist progress", logging.Error(err))
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
