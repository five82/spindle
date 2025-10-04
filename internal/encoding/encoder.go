package encoding

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/services"
	"spindle/internal/services/drapto"
	"spindle/internal/stage"
)

// Encoder manages Drapto encoding of ripped files.
type Encoder struct {
	store    *queue.Store
	cfg      *config.Config
	logger   *slog.Logger
	client   drapto.Client
	notifier notifications.Service
}

// NewEncoder constructs the encoding handler.
func NewEncoder(cfg *config.Config, store *queue.Store, logger *slog.Logger) *Encoder {
	client := drapto.NewCLI(drapto.WithBinary(cfg.DraptoBinary()))
	return NewEncoderWithDependencies(cfg, store, logger, client, notifications.NewService(cfg))
}

// NewEncoderWithDependencies allows injecting custom dependencies (used for tests).
func NewEncoderWithDependencies(cfg *config.Config, store *queue.Store, logger *slog.Logger, client drapto.Client, notifier notifications.Service) *Encoder {
	stageLogger := logger
	if stageLogger != nil {
		stageLogger = stageLogger.With(logging.String("component", "encoder"))
	}
	return &Encoder{store: store, cfg: cfg, logger: stageLogger, client: client, notifier: notifier}
}

func (e *Encoder) Prepare(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, e.logger)
	if item.ProgressStage == "" {
		item.ProgressStage = "Encoding"
	}
	item.ProgressMessage = "Starting Drapto encoding"
	item.ProgressPercent = 0
	item.ErrorMessage = ""
	logger.Info(
		"starting encoding preparation",
		logging.String("disc_title", strings.TrimSpace(item.DiscTitle)),
		logging.String("ripped_file", strings.TrimSpace(item.RippedFile)),
	)
	return nil
}

func (e *Encoder) Execute(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, e.logger)
	logger.Info("starting encoding", logging.String("ripped_file", strings.TrimSpace(item.RippedFile)))
	if item.RippedFile == "" {
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate inputs",
			"No ripped file available for encoding; ensure the ripping stage completed successfully",
			nil,
		)
	}

	encodedDir := filepath.Join(e.cfg.StagingDir, "encoded")
	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		return services.Wrap(
			services.ErrConfiguration,
			"encoding",
			"ensure encoded dir",
			"Failed to create encoded directory; set staging_dir to a writable path",
			err,
		)
	}
	logger.Info("prepared encoding directory", logging.String("encoded_dir", encodedDir))

	var encodedPath string
	if e.client != nil {
		expectedOutput := filepath.Join(encodedDir, filepath.Base(item.RippedFile)+".av1.mkv")
		logger.Info(
			"launching drapto encode",
			logging.String("command", e.draptoCommand(item.RippedFile, expectedOutput)),
			logging.String("input", item.RippedFile),
			logging.String("output", expectedOutput),
		)

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
				logger.Warn("failed to persist encoding progress", logging.Error(err))
			}
			*item = copy
		}

		var (
			lastLoggedPercent float64 = -1
			lastLoggedStage   string
			lastLoggedMsg     string
		)
		progressLogger := func(update drapto.ProgressUpdate) {
			shouldLog := false
			stage := strings.TrimSpace(update.Stage)
			msg := strings.TrimSpace(update.Message)
			if stage != "" && stage != lastLoggedStage {
				lastLoggedStage = stage
				shouldLog = true
			}
			if msg != "" && msg != lastLoggedMsg {
				lastLoggedMsg = msg
				shouldLog = true
			}
			if update.Percent >= 0 && (lastLoggedPercent < 0 || update.Percent-lastLoggedPercent >= 5 || update.Percent >= 100) {
				lastLoggedPercent = update.Percent
				shouldLog = true
			}
			if shouldLog {
				logger.Info(
					"drapto progress",
					logging.Float64("percent", update.Percent),
					logging.String("stage", stage),
					logging.String("message", msg),
				)
			}
			progress(update)
		}
		path, err := e.client.Encode(ctx, item.RippedFile, encodedDir, progressLogger)
		if err != nil {
			return services.Wrap(
				services.ErrExternalTool,
				"encoding",
				"drapto encode",
				"Drapto encoding failed; inspect Drapto logs and confirm the binary path in config",
				err,
			)
		}
		encodedPath = path
		if err := validateEncodedArtifact(encodedPath); err != nil {
			return services.Wrap(
				services.ErrExternalTool,
				"encoding",
				"validate output",
				"Drapto reported success but produced an invalid encoded file",
				err,
			)
		}
		logger.Info("drapto encode completed", logging.String("encoded_file", encodedPath))
	}

	if encodedPath == "" {
		base := filepath.Base(item.RippedFile)
		encodedPath = filepath.Join(encodedDir, strings.TrimSuffix(base, filepath.Ext(base))+".encoded.mkv")
		if err := copyFile(item.RippedFile, encodedPath); err != nil {
			return services.Wrap(services.ErrTransient, "encoding", "copy ripped file", "Failed to stage encoded artifact", err)
		}
		logger.Info("created placeholder encoded copy", logging.String("encoded_file", encodedPath))
	}

	if err := validateEncodedArtifact(encodedPath); err != nil {
		return services.Wrap(
			services.ErrExternalTool,
			"encoding",
			"validate output",
			"Encoded artifact missing or empty after encoding",
			err,
		)
	}

	item.EncodedFile = encodedPath
	item.ProgressStage = "Encoded"
	item.ProgressPercent = 100
	item.ProgressMessage = "Encoded placeholder artifact"
	if e.client != nil {
		item.ProgressMessage = "Encoding completed"
		if e.notifier != nil {
			if err := e.notifier.Publish(ctx, notifications.EventEncodingCompleted, notifications.Payload{"discTitle": item.DiscTitle}); err != nil {
				logger.Warn("encoding notification failed", logging.Error(err))
			}
		}
	}
	logger.Info(
		"encoding stage completed",
		logging.String("encoded_file", encodedPath),
		logging.String("progress_message", strings.TrimSpace(item.ProgressMessage)),
	)

	return nil
}

// HealthCheck verifies encoding dependencies for Drapto.
func (e *Encoder) HealthCheck(ctx context.Context) stage.Health {
	const name = "encoder"
	if e.cfg == nil {
		return stage.Unhealthy(name, "configuration unavailable")
	}
	if strings.TrimSpace(e.cfg.StagingDir) == "" {
		return stage.Unhealthy(name, "staging directory not configured")
	}
	if e.client == nil {
		return stage.Unhealthy(name, "drapto client unavailable")
	}
	binary := strings.TrimSpace(e.cfg.DraptoBinary())
	if binary == "" {
		return stage.Unhealthy(name, "drapto binary not configured")
	}
	if _, err := exec.LookPath(binary); err != nil {
		return stage.Unhealthy(name, fmt.Sprintf("drapto binary %q not found", binary))
	}
	return stage.Healthy(name)
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

func validateEncodedArtifact(path string) error {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return fmt.Errorf("encoded artifact path is empty")
	}
	info, err := os.Stat(clean)
	if err != nil {
		return fmt.Errorf("stat encoded artifact: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("encoded artifact %q is a directory", clean)
	}
	if info.Size() == 0 {
		return fmt.Errorf("encoded artifact %q is empty", clean)
	}
	return nil
}

func (e *Encoder) draptoBinaryName() string {
	if e == nil || e.cfg == nil {
		return "drapto"
	}
	binary := strings.TrimSpace(e.cfg.DraptoBinary())
	if binary == "" {
		return "drapto"
	}
	return binary
}

func (e *Encoder) draptoCommand(inputPath, outputPath string) string {
	binary := e.draptoBinaryName()
	return fmt.Sprintf("%s encode --input %q --output %q --progress-json", binary, strings.TrimSpace(inputPath), strings.TrimSpace(outputPath))
}
