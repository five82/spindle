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

	"spindle/internal/commentaryid"
	"spindle/internal/config"
	"spindle/internal/encodingstate"
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
	store            *queue.Store
	cfg              *config.Config
	logger           *slog.Logger
	client           drapto.Client
	notifier         notifications.Service
	cache            *ripcache.Manager
	presetClassifier presetClassifier
	commentary       *commentaryid.Detector
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

func (e *Encoder) encodeSource(ctx context.Context, item *queue.Item, sourcePath, encodedDir, label, episodeKey string, episodeIndex, episodeCount int, presetProfile string, logger *slog.Logger) (string, error) {
	if e.client == nil {
		return "", nil
	}
	jobLogger := logger
	episodeKey = strings.ToLower(strings.TrimSpace(episodeKey))
	if strings.TrimSpace(label) != "" || episodeKey != "" {
		jobLogger = jobLogger.With(
			logging.String(logging.FieldEpisodeKey, episodeKey),
			logging.String(logging.FieldEpisodeLabel, strings.TrimSpace(label)),
			logging.Int(logging.FieldEpisodeIndex, episodeIndex),
			logging.Int(logging.FieldEpisodeCount, episodeCount),
		)
	}
	jobLogger.Info(
		"launching drapto encode",
		logging.String("command", e.draptoCommand(sourcePath, encodedDir, presetProfile)),
		logging.String("input", sourcePath),
		logging.String("job", strings.TrimSpace(label)),
	)
	snapshot := loadEncodingSnapshot(jobLogger, item.EncodingDetailsJSON)
	snapshot.JobLabel = strings.TrimSpace(label)
	snapshot.EpisodeKey = episodeKey
	snapshot.EpisodeIndex = episodeIndex
	snapshot.EpisodeCount = episodeCount
	if raw, err := snapshot.Marshal(); err != nil {
		jobLogger.Warn("failed to marshal encoding snapshot", logging.Error(err))
	} else if raw != "" {
		copy := *item
		copy.EncodingDetailsJSON = raw
		copy.ActiveEpisodeKey = episodeKey
		if err := e.store.UpdateProgress(ctx, &copy); err != nil {
			jobLogger.Warn("failed to persist encoding job context", logging.Error(err))
		} else {
			*item = copy
		}
	}
	const progressPersistInterval = 2 * time.Second
	var lastPersisted time.Time
	progress := func(update drapto.ProgressUpdate) {
		copy := *item
		changed := false
		message := progressMessageText(update)
		if message != "" && strings.TrimSpace(label) != "" && episodeIndex > 0 && episodeCount > 0 {
			message = fmt.Sprintf("%s (%d/%d) — %s", strings.TrimSpace(label), episodeIndex, episodeCount, message)
		} else if message != "" && strings.TrimSpace(label) != "" {
			message = fmt.Sprintf("%s — %s", strings.TrimSpace(label), message)
		}
		if applyDraptoUpdate(&snapshot, update, message) {
			if raw, err := snapshot.Marshal(); err != nil {
				jobLogger.Warn("failed to marshal encoding snapshot", logging.Error(err))
			} else {
				copy.EncodingDetailsJSON = raw
			}
			changed = true
		}
		if stage := strings.TrimSpace(update.Stage); stage != "" && stage != copy.ProgressStage {
			copy.ProgressStage = stage
			changed = true
		}
		if update.Percent >= 0 && update.Percent != copy.ProgressPercent {
			copy.ProgressPercent = update.Percent
			changed = true
		}
		if message != "" && message != strings.TrimSpace(copy.ProgressMessage) {
			copy.ProgressMessage = message
			changed = true
		}
		if !changed {
			return
		}
		if update.Type == drapto.EventTypeEncodingProgress {
			now := time.Now()
			if !lastPersisted.IsZero() && now.Sub(lastPersisted) < progressPersistInterval {
				*item = copy
				return
			}
			lastPersisted = now
		}
		if err := e.store.UpdateProgress(ctx, &copy); err != nil {
			jobLogger.Warn("failed to persist encoding progress", logging.Error(err))
		}
		*item = copy
	}
	progressSampler := logging.NewProgressSampler(5)
	logProgressEvent := func(update drapto.ProgressUpdate) {
		stage := strings.TrimSpace(update.Stage)
		raw := strings.TrimSpace(update.Message)
		summary := progressMessageText(update)
		if !progressSampler.ShouldLog(update.Percent, stage, raw) {
			return
		}
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
		if update.ETA > 0 {
			attrs = append(attrs, logging.Duration("progress_eta", update.ETA))
		}
		if strings.TrimSpace(update.Bitrate) != "" {
			attrs = append(attrs, logging.String("progress_bitrate", strings.TrimSpace(update.Bitrate)))
		}
		jobLogger.Info("drapto progress", logging.Args(attrs...)...)
	}

	progressLogger := func(update drapto.ProgressUpdate) {
		persist := false
		switch update.Type {
		case drapto.EventTypeHardware:
			logDraptoHardware(jobLogger, label, update.Hardware)
			persist = true
		case drapto.EventTypeInitialization:
			logDraptoVideo(jobLogger, label, update.Video)
			persist = true
		case drapto.EventTypeCropResult:
			logDraptoCrop(jobLogger, label, update.Crop)
			persist = true
		case drapto.EventTypeEncodingConfig:
			logDraptoEncodingConfig(jobLogger, label, update.EncodingConfig)
			persist = true
		case drapto.EventTypeEncodingStarted:
			logDraptoEncodingStart(jobLogger, label, update.TotalFrames)
			persist = true
		case drapto.EventTypeValidation:
			logDraptoValidation(jobLogger, label, update.Validation)
			persist = true
		case drapto.EventTypeEncodingComplete:
			logDraptoEncodingResult(jobLogger, label, update.Result)
			persist = true
		case drapto.EventTypeOperationComplete:
			logDraptoOperation(jobLogger, label, update.OperationComplete)
		case drapto.EventTypeWarning:
			logDraptoWarning(jobLogger, label, update.Warning)
			persist = true
		case drapto.EventTypeError:
			logDraptoError(jobLogger, label, update.Error)
			persist = true
		case drapto.EventTypeBatchStarted:
			logDraptoBatchStart(jobLogger, label, update.BatchStart)
		case drapto.EventTypeFileProgress:
			logDraptoFileProgress(jobLogger, label, update.FileProgress)
		case drapto.EventTypeBatchComplete:
			logDraptoBatchSummary(jobLogger, label, update.BatchSummary)
		case drapto.EventTypeStageProgress, drapto.EventTypeEncodingProgress, drapto.EventTypeUnknown:
			logProgressEvent(update)
			persist = true
		default:
			if strings.TrimSpace(update.Message) != "" {
				attrs := []logging.Attr{
					logging.String("job", label),
					logging.String("drapto_event_type", string(update.Type)),
					logging.String("message", strings.TrimSpace(update.Message)),
				}
				jobLogger.Info("drapto event", logging.Args(attrs...)...)
			}
		}
		if persist {
			progress(update)
		}
	}

	path, err := e.client.Encode(ctx, sourcePath, encodedDir, drapto.EncodeOptions{
		Progress:      progressLogger,
		PresetProfile: presetProfile,
	})
	if err != nil {
		return "", services.Wrap(
			services.ErrExternalTool,
			"encoding",
			"drapto encode",
			"Drapto encoding failed; inspect the encoding log output and confirm the binary path in config",
			err,
		)
	}
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
	if cfg != nil && cfg.PresetDeciderEnabled {
		enc.presetClassifier = newPresetLLMClassifier(cfg)
	}
	if cfg != nil && cfg.CommentaryDetectionEnabled {
		enc.commentary = commentaryid.New(cfg, logger)
	}
	enc.SetLogger(logger)
	return enc
}

// SetLogger updates the encoder's logging destination while preserving component labeling.
func (e *Encoder) SetLogger(logger *slog.Logger) {
	e.logger = logging.NewComponentLogger(logger, "encoder")
	if e.cache != nil {
		e.cache.SetLogger(logger)
	}
	if e.commentary != nil {
		e.commentary.SetLogger(logger)
	}
}

func (e *Encoder) Prepare(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, e.logger)
	item.InitProgress("Encoding", "Starting Drapto encoding")
	item.DraptoPresetProfile = ""
	logger.Debug("starting encoding preparation")
	return nil
}

func (e *Encoder) Execute(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, e.logger)
	stageStart := time.Now()

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

	logger.Debug("starting encoding")
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
	if err := e.cleanupEncodedDir(logger, encodedDir); err != nil {
		return err
	}
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

	sampleSource := strings.TrimSpace(item.RippedFile)
	if len(jobs) > 0 {
		sampleSource = strings.TrimSpace(jobs[0].Source)
	}
	decision := e.selectPreset(ctx, item, sampleSource, logger)
	if profile := strings.TrimSpace(decision.Profile); profile != "" {
		item.DraptoPresetProfile = profile
	} else {
		item.DraptoPresetProfile = "default"
	}

	encodedPaths := make([]string, 0, maxInt(1, len(jobs)))
	if len(jobs) > 0 {
		for idx, job := range jobs {
			label := fmt.Sprintf("S%02dE%02d", job.Episode.Season, job.Episode.Episode)
			item.ActiveEpisodeKey = strings.ToLower(strings.TrimSpace(job.Episode.Key))
			if item.ActiveEpisodeKey != "" {
				item.ProgressMessage = fmt.Sprintf("Starting encode %s (%d/%d)", label, idx+1, len(jobs))
				item.ProgressPercent = 0
				if err := e.store.UpdateProgress(ctx, item); err != nil {
					logger.Warn("failed to persist encoding job start", logging.Error(err))
				}
			}
			if err := e.refineCommentaryTracks(ctx, item, job.Source, stagingRoot, label, idx+1, len(jobs), logger); err != nil {
				logger.Warn("commentary detection failed; encoding with existing audio streams", logging.Error(err))
			}
			path, err := e.encodeSource(ctx, item, job.Source, encodedDir, label, job.Episode.Key, idx+1, len(jobs), decision.Profile, logger)
			if err != nil {
				return err
			}
			finalPath, err := ensureEncodedOutput(path, job.Output, job.Source)
			if err != nil {
				return err
			}
			env.Assets.AddAsset("encoded", ripspec.Asset{EpisodeKey: job.Episode.Key, TitleID: job.Episode.TitleID, Path: finalPath})
			encodedPaths = append(encodedPaths, finalPath)

			// Persist rip spec after each episode so API consumers can surface
			// per-episode progress while the encoding stage is still running.
			if encoded, err := env.Encode(); err == nil {
				copy := *item
				copy.RipSpecData = encoded
				if err := e.store.Update(ctx, &copy); err != nil {
					logger.Warn("failed to persist rip spec after episode encode", logging.Error(err))
				} else {
					*item = copy
				}
			} else {
				logger.Warn("failed to encode rip spec after episode encode", logging.Error(err))
			}
		}
	} else {
		label := strings.TrimSpace(item.DiscTitle)
		if label == "" {
			label = "Disc"
		}
		item.ActiveEpisodeKey = ""
		if err := e.refineCommentaryTracks(ctx, item, item.RippedFile, stagingRoot, label, 0, 0, logger); err != nil {
			logger.Warn("commentary detection failed; encoding with existing audio streams", logging.Error(err))
		}
		path, err := e.encodeSource(ctx, item, item.RippedFile, encodedDir, label, "", 0, 0, decision.Profile, logger)
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
		if err := e.validateEncodedArtifact(ctx, path, stageStart); err != nil {
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
	item.ActiveEpisodeKey = ""
	if len(encodedPaths) > 1 {
		item.ProgressMessage = fmt.Sprintf("Encoding completed (%d episodes)", len(encodedPaths))
	} else if e.client != nil {
		item.ProgressMessage = "Encoding completed"
	} else {
		item.ProgressMessage = "Encoded placeholder artifact"
	}
	if suffix := presetSummary(decision); suffix != "" {
		item.ProgressMessage = fmt.Sprintf("%s – %s", item.ProgressMessage, suffix)
	}
	// Calculate resource consumption metrics
	var totalInputBytes, totalOutputBytes int64
	for _, path := range encodedPaths {
		if info, err := os.Stat(path); err == nil {
			totalOutputBytes += info.Size()
		}
	}
	if info, err := os.Stat(strings.TrimSpace(item.RippedFile)); err == nil {
		totalInputBytes = info.Size()
	}
	var compressionRatio float64
	if totalInputBytes > 0 {
		compressionRatio = float64(totalOutputBytes) / float64(totalInputBytes) * 100
	}

	if e.notifier != nil {
		if err := e.notifier.Publish(ctx, notifications.EventEncodingCompleted, notifications.Payload{
			"discTitle":   item.DiscTitle,
			"placeholder": e.client == nil,
			"ratio":       compressionRatio,
			"inputBytes":  totalInputBytes,
			"outputBytes": totalOutputBytes,
			"files":       len(encodedPaths),
			"preset":      strings.TrimSpace(item.DraptoPresetProfile),
		}); err != nil {
			logger.Debug("encoding notification failed", logging.Error(err))
		}
	}

	// Log stage summary with timing and resource metrics
	summaryAttrs := []logging.Attr{
		logging.String("encoded_file", item.EncodedFile),
		logging.Duration("stage_duration", time.Since(stageStart)),
		logging.Int64("input_bytes", totalInputBytes),
		logging.Int64("output_bytes", totalOutputBytes),
		logging.Float64("compression_ratio_percent", compressionRatio),
		logging.Int("files_encoded", len(encodedPaths)),
		logging.String("preset_profile", strings.TrimSpace(item.DraptoPresetProfile)),
	}
	logger.Info("encoding stage summary", logging.Args(summaryAttrs...)...)

	return nil
}

func (e *Encoder) cleanupEncodedDir(logger *slog.Logger, encodedDir string) error {
	encodedDir = strings.TrimSpace(encodedDir)
	if encodedDir == "" {
		return nil
	}
	info, err := os.Stat(encodedDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return services.Wrap(
			services.ErrConfiguration,
			"encoding",
			"inspect encoded dir",
			"Failed to inspect previous encoded artifacts",
			err,
		)
	}
	if !info.IsDir() {
		return services.Wrap(
			services.ErrConfiguration,
			"encoding",
			"inspect encoded dir",
			fmt.Sprintf("Expected encoded path %q to be a directory", encodedDir),
			nil,
		)
	}
	if err := os.RemoveAll(encodedDir); err != nil {
		return services.Wrap(
			services.ErrConfiguration,
			"encoding",
			"remove stale artifacts",
			"Failed to remove previous encoded outputs", err,
		)
	}
	if logger != nil {
		logger.Info("removed stale encoded artifacts", logging.String("encoded_dir", encodedDir))
	}
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

func loadEncodingSnapshot(logger *slog.Logger, raw string) encodingstate.Snapshot {
	snapshot, err := encodingstate.Unmarshal(raw)
	if err != nil {
		if logger != nil {
			logger.Warn("failed to parse encoding snapshot", logging.Error(err))
		}
		return encodingstate.Snapshot{}
	}
	return snapshot
}

func applyDraptoUpdate(snapshot *encodingstate.Snapshot, update drapto.ProgressUpdate, summary string) bool {
	if snapshot == nil {
		return false
	}
	changed := false
	switch update.Type {
	case drapto.EventTypeStageProgress, drapto.EventTypeEncodingProgress, drapto.EventTypeEncodingStarted, drapto.EventTypeUnknown:
		if mergeProgressSnapshot(snapshot, update, summary) {
			changed = true
		}
	}
	switch update.Type {
	case drapto.EventTypeHardware:
		if mergeHardwareSnapshot(snapshot, update.Hardware) {
			changed = true
		}
	case drapto.EventTypeInitialization:
		if mergeVideoSnapshot(snapshot, update.Video) {
			changed = true
		}
	case drapto.EventTypeCropResult:
		if mergeCropSnapshot(snapshot, update.Crop) {
			changed = true
		}
	case drapto.EventTypeEncodingConfig:
		if mergeConfigSnapshot(snapshot, update.EncodingConfig) {
			changed = true
		}
	case drapto.EventTypeValidation:
		if mergeValidationSnapshot(snapshot, update.Validation) {
			changed = true
		}
	case drapto.EventTypeEncodingComplete:
		if mergeResultSnapshot(snapshot, update.Result) {
			changed = true
		}
	case drapto.EventTypeWarning:
		if mergeWarningSnapshot(snapshot, update.Warning, update.Message) {
			changed = true
		}
	case drapto.EventTypeError:
		if mergeErrorSnapshot(snapshot, update.Error) {
			changed = true
		}
	}
	return changed
}

func mergeProgressSnapshot(snapshot *encodingstate.Snapshot, update drapto.ProgressUpdate, summary string) bool {
	changed := false
	if stage := strings.TrimSpace(update.Stage); stage != "" && stage != snapshot.Stage {
		snapshot.Stage = stage
		changed = true
	}
	if update.Percent >= 0 && update.Percent != snapshot.Percent {
		snapshot.Percent = update.Percent
		changed = true
	}
	if summary := strings.TrimSpace(summary); summary != "" && summary != snapshot.Message {
		snapshot.Message = summary
		changed = true
	}
	if update.ETA > 0 {
		eta := update.ETA.Seconds()
		if snapshot.ETASeconds != eta {
			snapshot.ETASeconds = eta
			changed = true
		}
	}
	if update.Speed > 0 && update.Speed != snapshot.Speed {
		snapshot.Speed = update.Speed
		changed = true
	}
	if update.FPS > 0 && update.FPS != snapshot.FPS {
		snapshot.FPS = update.FPS
		changed = true
	}
	if bitrate := strings.TrimSpace(update.Bitrate); bitrate != "" && bitrate != snapshot.Bitrate {
		snapshot.Bitrate = bitrate
		changed = true
	}
	if update.TotalFrames > 0 && update.TotalFrames != snapshot.TotalFrames {
		snapshot.TotalFrames = update.TotalFrames
		changed = true
	}
	if update.CurrentFrame > 0 && update.CurrentFrame != snapshot.CurrentFrame {
		snapshot.CurrentFrame = update.CurrentFrame
		changed = true
	}
	return changed
}

func mergeHardwareSnapshot(snapshot *encodingstate.Snapshot, info *drapto.HardwareInfo) bool {
	if info == nil {
		return false
	}
	host := strings.TrimSpace(info.Hostname)
	if host == "" {
		return false
	}
	if snapshot.Hardware == nil {
		snapshot.Hardware = &encodingstate.Hardware{}
	}
	if snapshot.Hardware.Hostname == host {
		return false
	}
	snapshot.Hardware.Hostname = host
	return true
}

func mergeVideoSnapshot(snapshot *encodingstate.Snapshot, info *drapto.VideoInfo) bool {
	if info == nil {
		return false
	}
	if snapshot.Video == nil {
		snapshot.Video = &encodingstate.Video{}
	}
	changed := false
	changed = setString(&snapshot.Video.InputFile, info.InputFile) || changed
	changed = setString(&snapshot.Video.OutputFile, info.OutputFile) || changed
	changed = setString(&snapshot.Video.Duration, info.Duration) || changed
	changed = setString(&snapshot.Video.Resolution, info.Resolution) || changed
	changed = setString(&snapshot.Video.Category, info.Category) || changed
	changed = setString(&snapshot.Video.DynamicRange, info.DynamicRange) || changed
	changed = setString(&snapshot.Video.AudioDescription, info.AudioDescription) || changed
	return changed
}

func mergeCropSnapshot(snapshot *encodingstate.Snapshot, summary *drapto.CropSummary) bool {
	if summary == nil {
		return false
	}
	if snapshot.Crop == nil {
		snapshot.Crop = &encodingstate.Crop{}
	}
	changed := false
	changed = setString(&snapshot.Crop.Message, summary.Message) || changed
	changed = setString(&snapshot.Crop.Crop, summary.Crop) || changed
	if snapshot.Crop.Required != summary.Required {
		snapshot.Crop.Required = summary.Required
		changed = true
	}
	if snapshot.Crop.Disabled != summary.Disabled {
		snapshot.Crop.Disabled = summary.Disabled
		changed = true
	}
	return changed
}

func mergeConfigSnapshot(snapshot *encodingstate.Snapshot, cfg *drapto.EncodingConfig) bool {
	if cfg == nil {
		return false
	}
	if snapshot.Config == nil {
		snapshot.Config = &encodingstate.Config{}
	}
	changed := false
	changed = setString(&snapshot.Config.Encoder, cfg.Encoder) || changed
	changed = setString(&snapshot.Config.Preset, cfg.Preset) || changed
	changed = setString(&snapshot.Config.Tune, cfg.Tune) || changed
	changed = setString(&snapshot.Config.Quality, cfg.Quality) || changed
	changed = setString(&snapshot.Config.PixelFormat, cfg.PixelFormat) || changed
	changed = setString(&snapshot.Config.MatrixCoefficients, cfg.MatrixCoefficients) || changed
	changed = setString(&snapshot.Config.AudioCodec, cfg.AudioCodec) || changed
	changed = setString(&snapshot.Config.AudioDescription, cfg.AudioDescription) || changed
	changed = setString(&snapshot.Config.DraptoPreset, cfg.DraptoPreset) || changed
	changed = setString(&snapshot.Config.SVTParams, cfg.SVTParams) || changed
	settings := make([]encodingstate.PresetSetting, 0, len(cfg.PresetSettings))
	for _, setting := range cfg.PresetSettings {
		settings = append(settings, encodingstate.PresetSetting{
			Key:   strings.TrimSpace(setting.Key),
			Value: strings.TrimSpace(setting.Value),
		})
	}
	if !presetSettingsEqual(snapshot.Config.PresetSettings, settings) {
		snapshot.Config.PresetSettings = settings
		changed = true
	}
	return changed
}

func mergeValidationSnapshot(snapshot *encodingstate.Snapshot, summary *drapto.ValidationSummary) bool {
	if summary == nil {
		return false
	}
	if snapshot.Validation == nil {
		snapshot.Validation = &encodingstate.Validation{}
	}
	changed := false
	if snapshot.Validation.Passed != summary.Passed {
		snapshot.Validation.Passed = summary.Passed
		changed = true
	}
	steps := make([]encodingstate.ValidationStep, 0, len(summary.Steps))
	for _, step := range summary.Steps {
		steps = append(steps, encodingstate.ValidationStep{
			Name:    strings.TrimSpace(step.Name),
			Passed:  step.Passed,
			Details: strings.TrimSpace(step.Details),
		})
	}
	if !validationStepsEqual(snapshot.Validation.Steps, steps) {
		snapshot.Validation.Steps = steps
		changed = true
	}
	return changed
}

func mergeResultSnapshot(snapshot *encodingstate.Snapshot, result *drapto.EncodingResult) bool {
	if result == nil {
		return false
	}
	if snapshot.Result == nil {
		snapshot.Result = &encodingstate.Result{}
	}
	changed := false
	changed = setString(&snapshot.Result.InputFile, result.InputFile) || changed
	changed = setString(&snapshot.Result.OutputFile, result.OutputFile) || changed
	changed = setString(&snapshot.Result.OutputPath, result.OutputPath) || changed
	changed = setString(&snapshot.Result.VideoStream, result.VideoStream) || changed
	changed = setString(&snapshot.Result.AudioStream, result.AudioStream) || changed
	if snapshot.Result.OriginalSize != result.OriginalSize {
		snapshot.Result.OriginalSize = result.OriginalSize
		changed = true
	}
	if snapshot.Result.EncodedSize != result.EncodedSize {
		snapshot.Result.EncodedSize = result.EncodedSize
		changed = true
	}
	if snapshot.Result.AverageSpeed != result.AverageSpeed {
		snapshot.Result.AverageSpeed = result.AverageSpeed
		changed = true
	}
	durationSeconds := result.Duration.Seconds()
	if snapshot.Result.DurationSeconds != durationSeconds {
		snapshot.Result.DurationSeconds = durationSeconds
		changed = true
	}
	if snapshot.Result.SizeReductionPercent != result.SizeReductionPercent {
		snapshot.Result.SizeReductionPercent = result.SizeReductionPercent
		changed = true
	}
	return changed
}

func mergeWarningSnapshot(snapshot *encodingstate.Snapshot, warning, fallback string) bool {
	message := strings.TrimSpace(warning)
	if message == "" {
		message = strings.TrimSpace(fallback)
	}
	if message == "" || snapshot.Warning == message {
		return false
	}
	snapshot.Warning = message
	return true
}

func mergeErrorSnapshot(snapshot *encodingstate.Snapshot, issue *drapto.ReporterIssue) bool {
	if issue == nil {
		return false
	}
	next := &encodingstate.Issue{
		Title:      strings.TrimSpace(issue.Title),
		Message:    strings.TrimSpace(issue.Message),
		Context:    strings.TrimSpace(issue.Context),
		Suggestion: strings.TrimSpace(issue.Suggestion),
	}
	if issuesEqual(snapshot.Error, next) {
		return false
	}
	snapshot.Error = next
	return true
}

func setString(target *string, value string) bool {
	trimmed := strings.TrimSpace(value)
	if *target == trimmed {
		return false
	}
	*target = trimmed
	return true
}

func presetSettingsEqual(a, b []encodingstate.PresetSetting) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Value != b[i].Value {
			return false
		}
	}
	return true
}

func validationStepsEqual(a, b []encodingstate.ValidationStep) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Passed != b[i].Passed || a[i].Details != b[i].Details {
			return false
		}
	}
	return true
}

func issuesEqual(a *encodingstate.Issue, b *encodingstate.Issue) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Title == b.Title && a.Message == b.Message && a.Context == b.Context && a.Suggestion == b.Suggestion
}

func logDraptoHardware(logger *slog.Logger, label string, info *drapto.HardwareInfo) {
	if logger == nil || info == nil || strings.TrimSpace(info.Hostname) == "" {
		return
	}
	infoWithJob(logger, label, "drapto hardware info", logging.String("hardware_hostname", strings.TrimSpace(info.Hostname)))
}

func logDraptoVideo(logger *slog.Logger, label string, info *drapto.VideoInfo) {
	if logger == nil || info == nil {
		return
	}
	attrs := []logging.Attr{
		logging.String("video_file", strings.TrimSpace(info.InputFile)),
		logging.String("video_output", strings.TrimSpace(info.OutputFile)),
		logging.String("video_duration", strings.TrimSpace(info.Duration)),
		logging.String("video_resolution", formatResolution(info.Resolution, info.Category)),
		logging.String("video_dynamic_range", strings.TrimSpace(info.DynamicRange)),
		logging.String("video_audio", strings.TrimSpace(info.AudioDescription)),
	}
	infoWithJob(logger, label, "drapto video info", attrs...)
}

func logDraptoCrop(logger *slog.Logger, label string, summary *drapto.CropSummary) {
	if logger == nil || summary == nil {
		return
	}
	status := "no crop required"
	if summary.Disabled {
		status = "auto-crop disabled"
	} else if summary.Required {
		status = "crop applied"
	}
	attrs := []logging.Attr{
		logging.String("crop_message", strings.TrimSpace(summary.Message)),
		logging.String("crop_status", status),
	}
	if strings.TrimSpace(summary.Crop) != "" {
		attrs = append(attrs, logging.String("crop_params", strings.TrimSpace(summary.Crop)))
	}
	infoWithJob(logger, label, "drapto crop detection", attrs...)
}

func logDraptoEncodingConfig(logger *slog.Logger, label string, cfg *drapto.EncodingConfig) {
	if logger == nil || cfg == nil {
		return
	}
	attrs := []logging.Attr{
		logging.String("encoding_encoder", strings.TrimSpace(cfg.Encoder)),
		logging.String("encoding_preset", strings.TrimSpace(cfg.Preset)),
		logging.String("encoding_tune", strings.TrimSpace(cfg.Tune)),
		logging.String("encoding_quality", strings.TrimSpace(cfg.Quality)),
		logging.String("encoding_pixel_format", strings.TrimSpace(cfg.PixelFormat)),
		logging.String("encoding_matrix", strings.TrimSpace(cfg.MatrixCoefficients)),
		logging.String("encoding_audio_codec", strings.TrimSpace(cfg.AudioCodec)),
		logging.String("encoding_audio", strings.TrimSpace(cfg.AudioDescription)),
		logging.String("encoding_drapto_preset", strings.TrimSpace(cfg.DraptoPreset)),
	}
	if len(cfg.PresetSettings) > 0 {
		pairs := make([]string, 0, len(cfg.PresetSettings))
		for _, setting := range cfg.PresetSettings {
			pairs = append(pairs, fmt.Sprintf("%s=%s", setting.Key, setting.Value))
		}
		attrs = append(attrs, logging.String("encoding_preset_values", strings.Join(pairs, ", ")))
	}
	if strings.TrimSpace(cfg.SVTParams) != "" {
		attrs = append(attrs, logging.String("encoding_svt_params", strings.TrimSpace(cfg.SVTParams)))
	}
	infoWithJob(logger, label, "drapto encoding config", attrs...)
}

func logDraptoEncodingStart(logger *slog.Logger, label string, totalFrames int64) {
	if logger == nil || totalFrames <= 0 {
		return
	}
	infoWithJob(logger, label, "drapto encoding started", logging.Int64("encoding_total_frames", totalFrames))
}

func logDraptoValidation(logger *slog.Logger, label string, summary *drapto.ValidationSummary) {
	if logger == nil || summary == nil {
		return
	}
	status := "failed"
	if summary.Passed {
		status = "passed"
	}
	infoWithJob(logger, label, "drapto validation", logging.String("validation_status", status))
	for _, step := range summary.Steps {
		infoWithJob(
			logger,
			label,
			"drapto validation step",
			logging.String("validation_step", strings.TrimSpace(step.Name)),
			logging.String("validation_status", formatValidationStatus(step.Passed)),
			logging.String("validation_details", strings.TrimSpace(step.Details)),
		)
	}
}

func logDraptoEncodingResult(logger *slog.Logger, label string, result *drapto.EncodingResult) {
	if logger == nil || result == nil {
		return
	}
	sizeSummary := fmt.Sprintf("%s -> %s", formatBytes(result.OriginalSize), formatBytes(result.EncodedSize))
	duration := formatETA(result.Duration)
	attrs := []logging.Attr{
		logging.String("encoding_result_input", strings.TrimSpace(result.InputFile)),
		logging.String("encoding_result_output", strings.TrimSpace(result.OutputFile)),
		logging.String("encoding_result_size", sizeSummary),
		logging.String("encoding_result_reduction", fmt.Sprintf("%.1f%%", result.SizeReductionPercent)),
		logging.String("encoding_result_video", strings.TrimSpace(result.VideoStream)),
		logging.String("encoding_result_audio", strings.TrimSpace(result.AudioStream)),
		logging.Float64("encoding_result_speed", result.AverageSpeed),
		logging.String("encoding_result_location", strings.TrimSpace(result.OutputPath)),
	}
	if duration != "" {
		attrs = append(attrs, logging.String("encoding_result_duration", duration))
	}
	infoWithJob(logger, label, "drapto results", attrs...)
}

func logDraptoOperation(logger *slog.Logger, label, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	infoWithJob(logger, label, "drapto encode complete", logging.String("result", strings.TrimSpace(message)))
}

func logDraptoWarning(logger *slog.Logger, label, warning string) {
	if strings.TrimSpace(warning) == "" {
		return
	}
	warnWithJob(logger, label, "drapto warning", logging.String("drapto_warning", strings.TrimSpace(warning)))
}

func logDraptoError(logger *slog.Logger, label string, issue *drapto.ReporterIssue) {
	if logger == nil || issue == nil {
		return
	}
	attrs := []logging.Attr{
		logging.String("drapto_error_title", strings.TrimSpace(issue.Title)),
		logging.String("drapto_error_message", strings.TrimSpace(issue.Message)),
	}
	if strings.TrimSpace(issue.Context) != "" {
		attrs = append(attrs, logging.String("drapto_error_context", strings.TrimSpace(issue.Context)))
	}
	if strings.TrimSpace(issue.Suggestion) != "" {
		attrs = append(attrs, logging.String("drapto_error_suggestion", strings.TrimSpace(issue.Suggestion)))
	}
	errorWithJob(logger, label, "drapto error", attrs...)
}

func logDraptoBatchStart(logger *slog.Logger, label string, info *drapto.BatchStartInfo) {
	if logger == nil || info == nil {
		return
	}
	attrs := []logging.Attr{
		logging.Int("batch_total_files", info.TotalFiles),
		logging.String("batch_output_dir", strings.TrimSpace(info.OutputDir)),
	}
	infoWithJob(logger, label, "drapto batch", attrs...)
}

func logDraptoFileProgress(logger *slog.Logger, label string, info *drapto.FileProgress) {
	if logger == nil || info == nil {
		return
	}
	attrs := []logging.Attr{
		logging.Int("batch_file_index", info.CurrentFile),
		logging.Int("batch_file_count", info.TotalFiles),
	}
	infoWithJob(logger, label, "drapto batch file", attrs...)
}

func logDraptoBatchSummary(logger *slog.Logger, label string, summary *drapto.BatchSummary) {
	if logger == nil || summary == nil {
		return
	}
	attrs := []logging.Attr{
		logging.Int("batch_successful", summary.SuccessfulCount),
		logging.Int("batch_total_files", summary.TotalFiles),
		logging.String("batch_reduction", fmt.Sprintf("%.1f%%", summary.TotalReductionPercent)),
	}
	if summary.TotalDuration > 0 {
		attrs = append(attrs, logging.String("batch_duration", formatETA(summary.TotalDuration)))
	}
	infoWithJob(logger, label, "drapto batch summary", attrs...)
}

func infoWithJob(logger *slog.Logger, label, message string, attrs ...logging.Attr) {
	if logger == nil {
		return
	}
	decorated := append([]logging.Attr{logging.String("job", label)}, attrs...)
	logger.Info(message, logging.Args(decorated...)...)
}

func warnWithJob(logger *slog.Logger, label, message string, attrs ...logging.Attr) {
	if logger == nil {
		return
	}
	decorated := append([]logging.Attr{logging.String("job", label)}, attrs...)
	logger.Warn(message, logging.Args(decorated...)...)
}

func errorWithJob(logger *slog.Logger, label, message string, attrs ...logging.Attr) {
	if logger == nil {
		return
	}
	decorated := append([]logging.Attr{logging.String("job", label)}, attrs...)
	logger.Error(message, logging.Args(decorated...)...)
}

func formatResolution(resolution, category string) string {
	res := strings.TrimSpace(resolution)
	cat := strings.TrimSpace(category)
	if res == "" {
		return cat
	}
	if cat == "" {
		return res
	}
	return fmt.Sprintf("%s (%s)", res, cat)
}

func formatValidationStatus(passed bool) string {
	if passed {
		return "ok"
	}
	return "failed"
}

func formatBytes(value int64) string {
	const (
		kiB = 1024
		miB = kiB * 1024
		giB = miB * 1024
	)
	switch {
	case value >= giB:
		return fmt.Sprintf("%.2f GiB", float64(value)/float64(giB))
	case value >= miB:
		return fmt.Sprintf("%.2f MiB", float64(value)/float64(miB))
	case value >= kiB:
		return fmt.Sprintf("%.2f KiB", float64(value)/float64(kiB))
	default:
		return fmt.Sprintf("%d B", value)
	}
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

	logger.Debug(
		"encoding validation succeeded",
		logging.String("encoded_file", clean),
		logging.Duration("elapsed", time.Since(startedAt)),
		logging.Group("ffprobe",
			logging.Float64("duration_seconds", duration),
			logging.Int("video_streams", probe.VideoStreamCount()),
			logging.Int("audio_streams", probe.AudioStreamCount()),
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

func (e *Encoder) draptoCommand(inputPath, outputDir, presetProfile string) string {
	binary := e.draptoBinaryName()
	parts := []string{
		fmt.Sprintf("%s encode", binary),
		fmt.Sprintf("--input %q", strings.TrimSpace(inputPath)),
		fmt.Sprintf("--output %q", strings.TrimSpace(outputDir)),
		"--responsive",
		"--no-log",
	}
	if profile := strings.TrimSpace(presetProfile); profile != "" && !strings.EqualFold(profile, "default") {
		parts = append(parts, fmt.Sprintf("--drapto-preset %s", profile))
	}
	parts = append(parts, "--progress-json")
	return strings.Join(parts, " ")
}
