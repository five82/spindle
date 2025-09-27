package encoding

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/services"
	"spindle/internal/services/drapto"
	"spindle/internal/workflow"
)

// Encoder manages Drapto encoding of ripped files.
type Encoder struct {
	store    *queue.Store
	cfg      *config.Config
	logger   *zap.Logger
	client   drapto.Client
	notifier notifications.Service
}

// NewEncoder constructs the encoding handler.
func NewEncoder(cfg *config.Config, store *queue.Store, logger *zap.Logger) *Encoder {
	client := drapto.NewCLI(drapto.WithBinary(cfg.DraptoBinary()))
	return NewEncoderWithDependencies(cfg, store, logger, client, notifications.NewService(cfg))
}

// NewEncoderWithDependencies allows injecting custom dependencies (used for tests).
func NewEncoderWithDependencies(cfg *config.Config, store *queue.Store, logger *zap.Logger, client drapto.Client, notifier notifications.Service) *Encoder {
	stageLogger := logger
	if stageLogger != nil {
		stageLogger = stageLogger.With(zap.String("component", "encoder"))
	}
	return &Encoder{store: store, cfg: cfg, logger: stageLogger, client: client, notifier: notifier}
}

func (e *Encoder) Name() string { return "encoder" }

func (e *Encoder) TriggerStatus() queue.Status { return queue.StatusRipped }

func (e *Encoder) ProcessingStatus() queue.Status { return queue.StatusEncoding }

func (e *Encoder) NextStatus() queue.Status { return queue.StatusEncoded }

func (e *Encoder) Prepare(ctx context.Context, item *queue.Item) error {
	if item.ProgressStage == "" {
		item.ProgressStage = "Encoding"
	}
	item.ProgressMessage = "Starting Drapto encoding"
	item.ProgressPercent = 0
	item.ErrorMessage = ""
	return nil
}

func (e *Encoder) Execute(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, e.logger)
	if item.RippedFile == "" {
		return services.WithHint(
			services.Wrap(services.ErrorValidation, "encoding", "validate inputs", "No ripped file available for encoding", nil),
			"Ensure the ripping stage completed successfully before encoding",
		)
	}

	encodedDir := filepath.Join(e.cfg.StagingDir, "encoded")
	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		return services.WithHint(
			services.Wrap(services.ErrorConfiguration, "encoding", "ensure encoded dir", "Failed to create encoded directory", err),
			"Set staging_dir to a writable path",
		)
	}

	var encodedPath string
	if e.client != nil {
		progress := func(update drapto.ProgressUpdate) {
			copy := *item
			if update.Stage != "" {
				copy.ProgressStage = update.Stage
			}
			if update.Message != "" {
				copy.ProgressMessage = update.Message
			}
			if update.Percent >= 0 {
				copy.ProgressPercent = update.Percent
			}
			if err := e.store.Update(ctx, &copy); err != nil {
				logger.Warn("failed to persist encoding progress", zap.Error(err))
			}
			*item = copy
		}

		path, err := e.client.Encode(ctx, item.RippedFile, encodedDir, progress)
		if err != nil {
			return services.WithHint(
				services.Wrap(services.ErrorExternalTool, "encoding", "drapto encode", "Drapto encoding failed", err),
				"Inspect Drapto logs and confirm the binary path in config",
			)
		}
		encodedPath = path
	}

	if encodedPath == "" {
		base := filepath.Base(item.RippedFile)
		encodedPath = filepath.Join(encodedDir, strings.TrimSuffix(base, filepath.Ext(base))+".encoded.mkv")
		if err := copyFile(item.RippedFile, encodedPath); err != nil {
			return services.Wrap(services.ErrorTransient, "encoding", "copy ripped file", "Failed to stage encoded artifact", err)
		}
	}

	item.EncodedFile = encodedPath
	item.ProgressStage = "Encoded"
	item.ProgressPercent = 100
	item.ProgressMessage = "Encoded placeholder artifact"
	if e.client != nil {
		item.ProgressMessage = "Encoding completed"
		if e.notifier != nil {
			if err := e.notifier.NotifyEncodingCompleted(ctx, item.DiscTitle); err != nil {
				logger.Warn("encoding notification failed", zap.Error(err))
			}
		}
	}

	return nil
}

func (e *Encoder) Rollback(ctx context.Context, item *queue.Item, stageErr error) error {
	return nil
}

var _ workflow.Stage = (*Encoder)(nil)

// HealthCheck verifies encoding dependencies for Drapto.
func (e *Encoder) HealthCheck(ctx context.Context) workflow.StageHealth {
	name := e.Name()
	if e.cfg == nil {
		return workflow.UnhealthyStage(name, "configuration unavailable")
	}
	if strings.TrimSpace(e.cfg.StagingDir) == "" {
		return workflow.UnhealthyStage(name, "staging directory not configured")
	}
	if e.client == nil {
		return workflow.UnhealthyStage(name, "drapto client unavailable")
	}
	return workflow.HealthyStage(name)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
