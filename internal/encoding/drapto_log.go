package encoding

import (
	"fmt"
	"os"
	"strings"

	"log/slog"

	"spindle/internal/encodingstate"
	"spindle/internal/logging"
	"spindle/internal/services/drapto"
)

func logDraptoHardware(logger *slog.Logger, label string, info *drapto.HardwareInfo) {
	if logger == nil || info == nil || strings.TrimSpace(info.Hostname) == "" {
		return
	}
	debugWithJob(logger, label, "drapto hardware info", logging.String("hardware_hostname", strings.TrimSpace(info.Hostname)))
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
	debugWithJob(logger, label, "drapto video info", attrs...)
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

	// Log crop candidates at DEBUG level for diagnosing multiple aspect ratio issues
	if len(summary.Candidates) > 0 {
		debugWithJob(logger, label, "drapto crop candidates",
			logging.Int("crop_total_samples", summary.TotalSamples),
			logging.Int("crop_unique_values", len(summary.Candidates)),
		)
		for i, c := range summary.Candidates {
			if i >= 10 {
				debugWithJob(logger, label, "drapto crop candidates truncated",
					logging.Int("remaining_candidates", len(summary.Candidates)-10),
				)
				break
			}
			debugWithJob(logger, label, "drapto crop candidate",
				logging.Int("crop_candidate_rank", i+1),
				logging.String("crop_candidate_value", c.Crop),
				logging.Int("crop_candidate_count", c.Count),
				logging.Float64("crop_candidate_percent", c.Percent),
			)
		}
	}
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
	debugWithJob(logger, label, "drapto encoding started", logging.Int64("encoding_total_frames", totalFrames))
}

func logDraptoValidation(logger *slog.Logger, label string, summary *drapto.ValidationSummary) {
	if logger == nil || summary == nil {
		return
	}
	if summary.Passed {
		debugWithJob(logger, label, "drapto validation", logging.String("validation_status", "passed"))
		for _, step := range summary.Steps {
			debugWithJob(
				logger,
				label,
				"drapto validation step",
				logging.String("validation_step", strings.TrimSpace(step.Name)),
				logging.String("validation_status", "ok"),
				logging.String("validation_details", strings.TrimSpace(step.Details)),
			)
		}
		return
	}
	// Validation failed - log at WARN level
	failedSteps := countFailedValidationSteps(summary.Steps)
	warnWithJob(
		logger,
		label,
		"drapto validation failed",
		logging.String("validation_status", "failed"),
		logging.Int("failed_steps", failedSteps),
		logging.Int("total_steps", len(summary.Steps)),
		logging.String(logging.FieldEventType, "drapto_validation_failed"),
		logging.String(logging.FieldErrorHint, "Review validation step details; encoded output may not match source"),
		logging.String(logging.FieldImpact, "encoded file may have unexpected characteristics"),
	)
	for _, step := range summary.Steps {
		if step.Passed {
			debugWithJob(
				logger,
				label,
				"drapto validation step",
				logging.String("validation_step", strings.TrimSpace(step.Name)),
				logging.String("validation_status", "ok"),
				logging.String("validation_details", strings.TrimSpace(step.Details)),
			)
		} else {
			warnWithJob(
				logger,
				label,
				"drapto validation step failed",
				logging.String("validation_step", strings.TrimSpace(step.Name)),
				logging.String("validation_status", "failed"),
				logging.String("validation_details", strings.TrimSpace(step.Details)),
				logging.String(logging.FieldEventType, "drapto_validation_step_failed"),
				logging.String(logging.FieldErrorHint, "Check step details for mismatch cause"),
				logging.String(logging.FieldImpact, "this validation check did not pass"),
			)
		}
	}
}

func countFailedValidationSteps(steps []drapto.ValidationStep) int {
	count := 0
	for _, step := range steps {
		if !step.Passed {
			count++
		}
	}
	return count
}

func logDraptoEncodingResult(logger *slog.Logger, label string, result *drapto.EncodingResult) {
	if logger == nil || result == nil {
		return
	}
	sizeSummary := fmt.Sprintf("%s -> %s", logging.FormatBytes(result.OriginalSize), logging.FormatBytes(result.EncodedSize))
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
	debugWithJob(logger, label, "drapto batch", attrs...)
}

func logDraptoFileProgress(logger *slog.Logger, label string, info *drapto.FileProgress) {
	if logger == nil || info == nil {
		return
	}
	attrs := []logging.Attr{
		logging.Int("batch_file_index", info.CurrentFile),
		logging.Int("batch_file_count", info.TotalFiles),
	}
	debugWithJob(logger, label, "drapto batch file", attrs...)
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

func debugWithJob(logger *slog.Logger, label, message string, attrs ...logging.Attr) {
	if logger == nil {
		return
	}
	decorated := append([]logging.Attr{logging.String("job", label)}, attrs...)
	logger.Debug(message, logging.Args(decorated...)...)
}

func warnWithJob(logger *slog.Logger, label, message string, attrs ...logging.Attr) {
	if logger == nil {
		return
	}
	if !logging.HasAttrKey(attrs, logging.FieldEventType) {
		attrs = append(attrs, logging.String(logging.FieldEventType, "drapto_warning"))
	}
	if !logging.HasAttrKey(attrs, logging.FieldErrorHint) {
		attrs = append(attrs, logging.String(logging.FieldErrorHint, "Review Drapto warnings and encoding logs"))
	}
	if !logging.HasAttrKey(attrs, logging.FieldImpact) {
		attrs = append(attrs, logging.String(logging.FieldImpact, "encoding completed with warnings"))
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

// updateEstimatedSize reads the current output file size and calculates the
// estimated total size based on encoding progress. Only updates when percent >= 10
// to ensure the estimate has stabilized.
func updateEstimatedSize(snapshot *encodingstate.Snapshot, percent float64) bool {
	if snapshot == nil || snapshot.Video == nil {
		return false
	}
	if percent < 10 {
		return false
	}
	outputPath := strings.TrimSpace(snapshot.Video.OutputFile)
	if outputPath == "" {
		return false
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		return false
	}
	currentBytes := info.Size()
	if currentBytes <= 0 {
		return false
	}
	estimatedTotal := int64(float64(currentBytes) / (percent / 100))
	changed := false
	if snapshot.CurrentOutputBytes != currentBytes {
		snapshot.CurrentOutputBytes = currentBytes
		changed = true
	}
	if snapshot.EstimatedTotalBytes != estimatedTotal {
		snapshot.EstimatedTotalBytes = estimatedTotal
		changed = true
	}
	return changed
}
