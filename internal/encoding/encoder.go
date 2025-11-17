package encoding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	"spindle/internal/ripcache"
	"spindle/internal/ripspec"
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
	cache    *ripcache.Manager
}

const (
	minEncodedFileSizeBytes = 5 * 1024 * 1024
)

var encodeProbe = ffprobe.Inspect

type encodeJob struct {
	Episode ripspec.Episode
	Source  string
	Output  string
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func buildEncodeJobs(env ripspec.Envelope, encodedDir string) ([]encodeJob, error) {
	if len(env.Episodes) == 0 {
		return nil, nil
	}
	jobs := make([]encodeJob, 0, len(env.Episodes))
	for _, episode := range env.Episodes {
		asset, ok := env.Assets.FindAsset("ripped", episode.Key)
		if !ok || strings.TrimSpace(asset.Path) == "" {
			return nil, fmt.Errorf("missing ripped asset for %s", episode.Key)
		}
		base := strings.TrimSpace(episode.OutputBasename)
		if base == "" {
			base = fmt.Sprintf("episode-%s", strings.ToLower(episode.Key))
		}
		output := filepath.Join(encodedDir, base+".mkv")
		jobs = append(jobs, encodeJob{Episode: episode, Source: asset.Path, Output: output})
	}
	return jobs, nil
}

func (e *Encoder) encodeSource(ctx context.Context, item *queue.Item, sourcePath, encodedDir, label string, logger *slog.Logger) (string, error) {
	if e.client == nil {
		return "", nil
	}
	logger.Info(
		"launching drapto encode",
		logging.String("command", e.draptoCommand(sourcePath, encodedDir)),
		logging.String("input", sourcePath),
		logging.String("job", strings.TrimSpace(label)),
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
		if err := e.store.UpdateProgress(ctx, &copy); err != nil {
			logger.Warn("failed to persist encoding progress", logging.Error(err))
		}
		*item = copy
	}
	var (
		lastStage  string
		lastMsg    string
		lastBucket = -1
	)
	progressLogger := func(update drapto.ProgressUpdate) {
		stage := strings.TrimSpace(update.Stage)
		raw := strings.TrimSpace(update.Message)
		summary := progressMessageText(update)
		log := false
		if stage != "" && stage != lastStage {
			lastStage = stage
			log = true
			lastBucket = -1
		}
		if raw != "" && raw != lastMsg {
			lastMsg = raw
			log = true
		}
		if update.Percent >= 5 {
			bucket := int(update.Percent / 5)
			if bucket > lastBucket {
				lastBucket = bucket
				log = true
			}
		}
		if update.Percent >= 100 && lastBucket < 20 {
			lastBucket = 20
			log = true
		}
		if log {
			attrs := []logging.Attr{logging.String("job", label)}
			if update.Percent >= 0 {
				attrs = append(attrs, logging.Float64("progress_percent", update.Percent))
			}
			if stage != "" {
				attrs = append(attrs, logging.String("progress_stage", stage))
			}
			if summary != "" {
				attrs = append(attrs, logging.String("progress_message", summary))
			}
			logger.Info("drapto progress", logging.Args(attrs...)...)
		}
		progress(update)
	}
	path, err := e.client.Encode(ctx, sourcePath, encodedDir, progressLogger)
	if err != nil {
		e.updateDraptoLogPointer(logger)
		return "", services.Wrap(
			services.ErrExternalTool,
			"encoding",
			"drapto encode",
			"Drapto encoding failed; inspect Drapto logs and confirm the binary path in config",
			err,
		)
	}
	e.updateDraptoLogPointer(logger)
	return path, nil
}

func ensureEncodedOutput(tempPath, desiredPath, sourcePath string) (string, error) {
	desiredPath = strings.TrimSpace(desiredPath)
	if desiredPath == "" {
		desiredPath = tempPath
	}
	if tempPath != "" {
		if strings.EqualFold(tempPath, desiredPath) {
			return tempPath, nil
		}
		if err := os.Rename(tempPath, desiredPath); err != nil {
			return "", services.Wrap(
				services.ErrTransient,
				"encoding",
				"finalize output",
				"Failed to move encoded artifact into destination",
				err,
			)
		}
		return desiredPath, nil
	}
	if err := copyFile(sourcePath, desiredPath); err != nil {
		return "", services.Wrap(
			services.ErrTransient,
			"encoding",
			"stage placeholder",
			"Failed to stage encoded artifact",
			err,
		)
	}
	return desiredPath, nil
}

func deriveEncodedFilename(rippedPath string) string {
	base := filepath.Base(rippedPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = "encoded"
	}
	return stem + ".mkv"
}

// NewEncoder constructs the encoding handler.
func NewEncoder(cfg *config.Config, store *queue.Store, logger *slog.Logger) *Encoder {
	client := drapto.NewCLI(
		drapto.WithBinary(cfg.DraptoBinary()),
		drapto.WithLogDir(draptoLogDirFromConfig(cfg)),
		drapto.WithPreset(cfg.DraptoPreset),
	)
	return NewEncoderWithDependencies(cfg, store, logger, client, notifications.NewService(cfg))
}

// NewEncoderWithDependencies allows injecting custom dependencies (used for tests).
func NewEncoderWithDependencies(cfg *config.Config, store *queue.Store, logger *slog.Logger, client drapto.Client, notifier notifications.Service) *Encoder {
	enc := &Encoder{
		store:    store,
		cfg:      cfg,
		client:   client,
		notifier: notifier,
		cache:    ripcache.NewManager(cfg, logger),
	}
	enc.SetLogger(logger)
	return enc
}

// SetLogger updates the encoder's logging destination while preserving component labeling.
func (e *Encoder) SetLogger(logger *slog.Logger) {
	stageLogger := logger
	if stageLogger == nil {
		stageLogger = logging.NewNop()
	}
	e.logger = stageLogger.With(logging.String("component", "encoder"))
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

	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"parse rip spec",
			"Rip specification missing or invalid; rerun identification",
			err,
		)
	}

	logger.Info("starting encoding", logging.String("ripped_file", strings.TrimSpace(item.RippedFile)))
	if strings.TrimSpace(item.RippedFile) == "" {
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate inputs",
			"No ripped file available for encoding; ensure the ripping stage completed successfully",
			nil,
		)
	}

	if e.cache != nil {
		ripDir := filepath.Dir(strings.TrimSpace(item.RippedFile))
		if !fileExists(item.RippedFile) {
			if restored, err := e.cache.Restore(ctx, item, ripDir); err != nil {
				return services.Wrap(
					services.ErrTransient,
					"encoding",
					"restore rip cache",
					"Failed to restore ripped files from cache; check cache path and permissions",
					err,
				)
			} else if restored {
				logger.Info("restored ripped files from cache", logging.String("rip_dir", ripDir))
			}
		}
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

	jobs, err := buildEncodeJobs(env, encodedDir)
	if err != nil {
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"plan encode jobs",
			"Unable to map ripped episodes to encoding jobs",
			err,
		)
	}

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

	encodedPaths := make([]string, 0, maxInt(1, len(jobs)))
	if len(jobs) > 0 {
		for _, job := range jobs {
			label := fmt.Sprintf("S%02dE%02d", job.Episode.Season, job.Episode.Episode)
			path, err := e.encodeSource(ctx, item, job.Source, encodedDir, label, logger)
			if err != nil {
				return err
			}
			finalPath, err := ensureEncodedOutput(path, job.Output, job.Source)
			if err != nil {
				return err
			}
			env.Assets.AddAsset("encoded", ripspec.Asset{EpisodeKey: job.Episode.Key, TitleID: job.Episode.TitleID, Path: finalPath})
			encodedPaths = append(encodedPaths, finalPath)
		}
	} else {
		label := strings.TrimSpace(item.DiscTitle)
		if label == "" {
			label = "Disc"
		}
		path, err := e.encodeSource(ctx, item, item.RippedFile, encodedDir, label, logger)
		if err != nil {
			return err
		}
		finalTarget := filepath.Join(encodedDir, deriveEncodedFilename(item.RippedFile))
		finalPath, err := ensureEncodedOutput(path, finalTarget, item.RippedFile)
		if err != nil {
			return err
		}
		encodedPaths = append(encodedPaths, finalPath)
	}

	if len(encodedPaths) == 0 {
		return services.Wrap(
			services.ErrValidation,
			"encoding",
			"locate encoded outputs",
			"No encoded artifacts were produced",
			nil,
		)
	}

	for _, path := range encodedPaths {
		if err := e.validateEncodedArtifact(ctx, path, startedAt); err != nil {
			return err
		}
	}

	if encoded, err := env.Encode(); err == nil {
		item.RipSpecData = encoded
	} else {
		logger.Warn("failed to encode rip spec after encoding", logging.Error(err))
	}

	item.EncodedFile = encodedPaths[0]
	item.ProgressStage = "Encoded"
	item.ProgressPercent = 100
	if len(encodedPaths) > 1 {
		item.ProgressMessage = fmt.Sprintf("Encoding completed (%d episodes)", len(encodedPaths))
	} else if e.client != nil {
		item.ProgressMessage = "Encoding completed"
	} else {
		item.ProgressMessage = "Encoded placeholder artifact"
	}
	if e.client != nil && e.notifier != nil {
		if err := e.notifier.Publish(ctx, notifications.EventEncodingCompleted, notifications.Payload{"discTitle": item.DiscTitle}); err != nil {
			logger.Warn("encoding notification failed", logging.Error(err))
		}
	}
	logger.Info(
		"encoding stage completed",
		logging.String("encoded_file", item.EncodedFile),
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

func fileExists(path string) bool {
	info, err := os.Stat(strings.TrimSpace(path))
	return err == nil && !info.IsDir()
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
	parts := []string{
		fmt.Sprintf("%s encode", binary),
		fmt.Sprintf("--input %q", strings.TrimSpace(inputPath)),
		fmt.Sprintf("--output %q", strings.TrimSpace(outputDir)),
		"--responsive",
		fmt.Sprintf("--preset %d", e.draptoPreset()),
	}
	if logDir != "" {
		parts = append(parts, fmt.Sprintf("--log-dir %q", logDir))
	}
	parts = append(parts, "--progress-json")
	return strings.Join(parts, " ")
}

func (e *Encoder) updateDraptoLogPointer(logger *slog.Logger) {
	if e == nil || e.cfg == nil {
		return
	}
	logDir := strings.TrimSpace(e.draptoLogDir())
	pointer := strings.TrimSpace(e.cfg.DraptoCurrentLogPath())
	if logDir == "" || pointer == "" {
		return
	}
	if err := updateDraptoLogPointer(logDir, pointer); err != nil {
		if logger != nil {
			logger.Warn(
				"failed to update drapto log pointer",
				logging.String("log_dir", logDir),
				logging.String("pointer", pointer),
				logging.Error(err),
			)
		}
		return
	}
	if logger != nil {
		logger.Debug(
			"updated drapto log pointer",
			logging.String("pointer", pointer),
		)
	}
}

func (e *Encoder) draptoLogDir() string {
	return draptoLogDirFromConfig(e.cfg)
}

func (e *Encoder) draptoPreset() int {
	if e == nil || e.cfg == nil {
		cfg := config.Default()
		return cfg.DraptoPreset
	}
	if e.cfg.DraptoPreset < 0 {
		cfg := config.Default()
		return cfg.DraptoPreset
	}
	return e.cfg.DraptoPreset
}

func draptoLogDirFromConfig(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.DraptoLogDir)
}

func updateDraptoLogPointer(logDir, pointer string) error {
	if strings.TrimSpace(logDir) == "" || strings.TrimSpace(pointer) == "" {
		return nil
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read drapto log directory: %w", err)
	}

	var (
		latestPath string
		latestMod  time.Time
		latestName string
	)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		mod := info.ModTime()
		switch {
		case latestPath == "":
		case mod.After(latestMod):
		case mod.Equal(latestMod) && name > latestName:
		default:
			continue
		}
		latestPath = filepath.Join(logDir, name)
		latestMod = mod
		latestName = name
	}
	if latestPath == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(pointer), 0o755); err != nil {
		return fmt.Errorf("ensure drapto pointer directory: %w", err)
	}
	if err := os.Remove(pointer); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove existing drapto log pointer: %w", err)
	}
	if latestPath == pointer {
		return nil
	}
	if err := os.Symlink(latestPath, pointer); err == nil {
		return nil
	}
	if err := os.Link(latestPath, pointer); err == nil {
		return nil
	}
	if err := copyFile(latestPath, pointer); err != nil {
		return fmt.Errorf("copy drapto log pointer: %w", err)
	}
	return nil
}
