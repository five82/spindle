package encoding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/services"
	"spindle/internal/services/drapto"
)

type draptoRunner struct {
	cfg    *config.Config
	client drapto.Client
	store  *queue.Store
}

func newDraptoRunner(cfg *config.Config, client drapto.Client, store *queue.Store) *draptoRunner {
	return &draptoRunner{cfg: cfg, client: client, store: store}
}

func (r *draptoRunner) Encode(ctx context.Context, item *queue.Item, sourcePath, encodedDir, label, episodeKey string, episodeIndex, episodeCount int, presetProfile string, logger *slog.Logger) (string, error) {
	if r == nil || r.client == nil {
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
		logging.String("command", r.draptoCommand(sourcePath, encodedDir, presetProfile)),
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
		if err := r.store.UpdateProgress(ctx, &copy); err != nil {
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
		if err := r.store.UpdateProgress(ctx, &copy); err != nil {
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

	path, err := r.client.Encode(ctx, sourcePath, encodedDir, drapto.EncodeOptions{
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

func (r *draptoRunner) draptoBinaryName() string {
	if r == nil || r.cfg == nil {
		return "drapto"
	}
	binary := strings.TrimSpace(r.cfg.DraptoBinary())
	if binary == "" {
		return "drapto"
	}
	return binary
}

func (r *draptoRunner) draptoCommand(inputPath, outputDir, presetProfile string) string {
	binary := r.draptoBinaryName()
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
