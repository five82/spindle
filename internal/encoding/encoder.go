package encoding

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
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

const (
	minEncodedFileSizeBytes = 5 * 1024 * 1024
)

var encodeProbe = ffprobe.Inspect

// NewEncoder constructs the encoding handler.
func NewEncoder(cfg *config.Config, store *queue.Store, logger *slog.Logger) *Encoder {
	client := drapto.NewCLI(
		drapto.WithBinary(cfg.DraptoBinary()),
		drapto.WithLogDir(draptoLogDirFromConfig(cfg)),
	)
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
	startedAt := time.Now()
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

	stagingRoot := item.StagingRoot(e.cfg.StagingDir)
	if stagingRoot == "" {
		stagingRoot = filepath.Join(strings.TrimSpace(e.cfg.StagingDir), fmt.Sprintf("queue-%d", item.ID))
	}
	encodedDir := filepath.Join(stagingRoot, "encoded")
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

	if draptoLogDir := e.draptoLogDir(); draptoLogDir != "" {
		if err := os.MkdirAll(draptoLogDir, 0o755); err != nil {
			return services.Wrap(
				services.ErrConfiguration,
				"encoding",
				"ensure drapto log dir",
				"Failed to create Drapto log directory; set log_dir to a writable path",
				err,
			)
		}
		logger.Info("prepared drapto log directory", logging.String("drapto_log_dir", draptoLogDir))
	}

	var encodedPath string
	if e.client != nil {
		base := filepath.Base(item.RippedFile)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if stem == "" {
			stem = base
		}
		expectedOutput := filepath.Join(encodedDir, stem+".mkv")
		logger.Info(
			"launching drapto encode",
			logging.String("command", e.draptoCommand(item.RippedFile, encodedDir)),
			logging.String("input", item.RippedFile),
			logging.String("expected_output", expectedOutput),
		)

		progress := func(update drapto.ProgressUpdate) {
			copy := *item
			if update.Stage != "" {
				copy.ProgressStage = update.Stage
			}
			if update.Percent >= 0 {
				copy.ProgressPercent = update.Percent
			}
			if message := progressMessageText(update); message != "" {
				copy.ProgressMessage = message
			}
			if err := e.store.Update(ctx, &copy); err != nil {
				logger.Warn("failed to persist encoding progress", logging.Error(err))
			}
			*item = copy
		}

		var (
			lastLoggedStage  string
			lastLoggedRawMsg string
			lastLoggedBucket = -1
		)
		progressLogger := func(update drapto.ProgressUpdate) {
			stage := strings.TrimSpace(update.Stage)
			rawMsg := strings.TrimSpace(update.Message)
			summary := progressMessageText(update)
			shouldLog := false
			if stage != "" && stage != lastLoggedStage {
				lastLoggedStage = stage
				shouldLog = true
				lastLoggedBucket = -1
			}
			if rawMsg != "" && rawMsg != lastLoggedRawMsg {
				lastLoggedRawMsg = rawMsg
				shouldLog = true
			}
			if update.Percent >= 1 && lastLoggedBucket < 0 {
				lastLoggedBucket = 0
				shouldLog = true
			}
			if update.Percent >= 5 {
				bucket := int(update.Percent / 5)
				if bucket > lastLoggedBucket {
					lastLoggedBucket = bucket
					shouldLog = true
				}
			}
			if update.Percent >= 100 && lastLoggedBucket < 20 {
				lastLoggedBucket = 20
				shouldLog = true
			}
			if shouldLog {
				attrs := []logging.Attr{}
				if update.Percent >= 0 {
					attrs = append(attrs, logging.Float64("progress_percent", update.Percent))
				}
				if stage != "" {
					attrs = append(attrs, logging.String("progress_stage", stage))
				}
				if summary != "" {
					attrs = append(attrs, logging.String("progress_message", summary))
				}
				if update.ETA > 0 {
					attrs = append(attrs, logging.Duration("progress_eta", update.ETA))
				}
				if update.Speed > 0 {
					attrs = append(attrs, logging.Float64("speed_x", update.Speed))
				}
				if update.FPS > 0 {
					attrs = append(attrs, logging.Float64("fps", update.FPS))
				}
				if update.Bitrate != "" {
					attrs = append(attrs, logging.String("bitrate", update.Bitrate))
				}
				logger.Info("drapto progress", logging.Args(attrs...)...)
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
	if err := e.validateEncodedArtifact(ctx, encodedPath, startedAt); err != nil {
		return err
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

func progressMessageText(update drapto.ProgressUpdate) string {
	message := strings.TrimSpace(update.Message)
	if message != "" {
		return message
	}
	if update.Percent < 0 {
		return ""
	}
	label := formatStageLabel(update.Stage)
	base := fmt.Sprintf("%s %.1f%%", label, update.Percent)
	extras := make([]string, 0, 2)
	if update.ETA > 0 {
		if formatted := formatETA(update.ETA); formatted != "" {
			extras = append(extras, fmt.Sprintf("ETA %s", formatted))
		}
	}
	if update.Speed > 0 {
		extras = append(extras, fmt.Sprintf("@ %.1fx", update.Speed))
	}
	if len(extras) == 0 {
		return base
	}
	return fmt.Sprintf("%s (%s)", base, strings.Join(extras, ", "))
}

func formatStageLabel(stage string) string {
	stage = strings.TrimSpace(stage)
	if stage == "" {
		return "Progress"
	}
	parts := strings.FieldsFunc(stage, func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})
	if len(parts) == 0 {
		return capitalizeASCII(stage)
	}
	for i, part := range parts {
		parts[i] = capitalizeASCII(part)
	}
	return strings.Join(parts, " ")
}

func formatETA(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	d = d.Round(time.Second)
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute
	d -= minutes * time.Minute
	seconds := d / time.Second
	parts := make([]string, 0, 3)
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 || hours > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if seconds > 0 || (hours == 0 && minutes == 0) {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	return strings.Join(parts, "")
}

func capitalizeASCII(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	return strings.ToUpper(lower[:1]) + lower[1:]
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

func (e *Encoder) validateEncodedArtifact(ctx context.Context, path string, startedAt time.Time) error {
	logger := logging.WithContext(ctx, e.logger)
	clean := strings.TrimSpace(path)
	if clean == "" {
		logger.Error("encoding validation failed", logging.String("reason", "empty path"))
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate output",
			"Encoding produced an empty file path",
			nil,
		)
	}
	info, err := os.Stat(clean)
	if err != nil {
		logger.Error("encoding validation failed", logging.String("reason", "stat failure"), logging.Error(err))
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate output",
			"Failed to stat encoded file",
			err,
		)
	}
	if info.IsDir() {
		logger.Error("encoding validation failed", logging.String("reason", "path is directory"), logging.String("encoded_path", clean))
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate output",
			"Encoded artifact points to a directory",
			nil,
		)
	}
	if info.Size() < minEncodedFileSizeBytes {
		logger.Error(
			"encoding validation failed",
			logging.String("reason", "file too small"),
			logging.Int64("size_bytes", info.Size()),
		)
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate output",
			fmt.Sprintf("Encoded file %q is unexpectedly small (%d bytes)", clean, info.Size()),
			nil,
		)
	}

	binary := "ffprobe"
	if e.cfg != nil {
		binary = e.cfg.FFprobeBinary()
	}
	probe, err := encodeProbe(ctx, binary, clean)
	if err != nil {
		logger.Error("encoding validation failed", logging.String("reason", "ffprobe"), logging.Error(err))
		return services.Wrap(
			services.ErrExternalTool,
			"encoding",
			"ffprobe validation",
			"Failed to inspect encoded file with ffprobe",
			err,
		)
	}
	if probe.VideoStreamCount() == 0 {
		logger.Error("encoding validation failed", logging.String("reason", "no video stream"))
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate video stream",
			"Encoded file does not contain a video stream",
			nil,
		)
	}
	if probe.AudioStreamCount() == 0 {
		logger.Error("encoding validation failed", logging.String("reason", "no audio stream"))
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate audio stream",
			"Encoded file does not contain an audio stream",
			nil,
		)
	}
	duration := probe.DurationSeconds()
	if duration <= 0 {
		logger.Error("encoding validation failed", logging.String("reason", "invalid duration"))
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate duration",
			"Encoded file duration could not be determined",
			nil,
		)
	}

	logger.Info(
		"encoding validation succeeded",
		logging.String("encoded_file", clean),
		logging.Duration("elapsed", time.Since(startedAt)),
		logging.String("ffprobe_binary", binary),
		logging.Group("ffprobe",
			logging.Float64("duration_seconds", duration),
			logging.Int("video_streams", probe.VideoStreamCount()),
			logging.Int("audio_streams", probe.AudioStreamCount()),
			logging.Int64("size_bytes", info.Size()),
			logging.Int64("bitrate_bps", probe.BitRate()),
		),
	)
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

func (e *Encoder) draptoCommand(inputPath, outputDir string) string {
	binary := e.draptoBinaryName()
	logDir := strings.TrimSpace(e.draptoLogDir())
	if logDir != "" {
		return fmt.Sprintf(
			"%s encode --input %q --output %q --log-dir %q --progress-json",
			binary,
			strings.TrimSpace(inputPath),
			strings.TrimSpace(outputDir),
			logDir,
		)
	}
	return fmt.Sprintf(
		"%s encode --input %q --output %q --progress-json",
		binary,
		strings.TrimSpace(inputPath),
		strings.TrimSpace(outputDir),
	)
}

func (e *Encoder) draptoLogDir() string {
	return draptoLogDirFromConfig(e.cfg)
}

func draptoLogDirFromConfig(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	trimmed := strings.TrimSpace(cfg.LogDir)
	if trimmed == "" {
		return ""
	}
	return filepath.Join(trimmed, "drapto")
}
