package reporter

import (
	"log/slog"
	"sync"
	"time"

	"github.com/five82/spindle/reel/internal/util"
)

// LogReporter records reporter events as structured log records, so the
// CLI's log file carries the same lifecycle facts (inputs, configuration,
// progress, validation, results) as the diagnostics Reel logs directly.
type LogReporter struct {
	log                *slog.Logger
	mu                 sync.Mutex
	lastProgressBucket int // Track progress in 5% buckets
}

// NewLogReporter creates a reporter that logs events to logger.
func NewLogReporter(logger *slog.Logger) *LogReporter {
	return &LogReporter{log: logger, lastProgressBucket: -1}
}

func (r *LogReporter) Hardware(summary HardwareSummary) {
	r.log.Info("hardware", "hostname", summary.Hostname)
}

func (r *LogReporter) Initialization(summary InitializationSummary) {
	r.log.Info("video input",
		"input", summary.InputFile,
		"output", summary.OutputFile,
		"duration", summary.Duration,
		"resolution", summary.Resolution,
		"dynamic_range", summary.DynamicRange,
		"audio", summary.AudioDescription,
	)
}

func (r *LogReporter) StageProgress(update StageProgress) {
	r.log.Info("stage progress", "stage", update.Stage, "state", update.State, "message", update.Message)
}

func (r *LogReporter) CropResult(summary CropSummary) {
	r.log.Info("crop detection",
		"disabled", summary.Disabled,
		"required", summary.Required,
		"crop", summary.Crop,
		"message", summary.Message,
	)
}

func (r *LogReporter) EncodingConfig(summary EncodingConfigSummary) {
	r.log.Info("encoding config",
		"encoder", summary.Encoder,
		"encoder_version", summary.EncoderVersion,
		"preset", summary.Preset,
		"tune", summary.Tune,
		"quality", summary.Quality,
		"pixel_format", summary.PixelFormat,
		"matrix_coefficients", summary.MatrixCoefficients,
		"audio_codec", summary.AudioCodec,
		"audio", summary.AudioDescription,
		"svtav1_params", summary.SVTAV1Params,
	)
}

func (r *LogReporter) EncodingStarted(totalFrames uint64) {
	r.mu.Lock()
	r.lastProgressBucket = -1
	r.mu.Unlock()
	r.log.Info("encoding started", "total_frames", totalFrames)
}

func (r *LogReporter) EncodingProgress(progress ProgressSnapshot) {
	// Log progress at 5% intervals
	bucket := int(progress.Percent / 5)
	r.mu.Lock()
	if bucket <= r.lastProgressBucket || bucket > 20 {
		r.mu.Unlock()
		return
	}
	r.lastProgressBucket = bucket
	r.mu.Unlock()
	attrs := []any{
		"percent", progress.Percent,
		"speed", progress.Speed,
		"recent_speed", progress.RecentSpeed,
		"fps", progress.FPS,
		"eta", util.FormatDurationFromSecs(int64(progress.ETA.Seconds())),
		"chunks_complete", progress.ChunksComplete,
		"chunks_total", progress.ChunksTotal,
	}
	if progress.MaxWorkers > 0 {
		attrs = append(attrs, "workers_active", progress.ActiveWorkers, "workers_target", progress.TargetWorkers, "workers_max", progress.MaxWorkers)
	}
	if stats, ok := util.ReadMemoryStats(); ok && stats.MemTotal > 0 {
		attrs = append(attrs, "mem_used", util.FormatBytes(stats.MemTotal-stats.MemAvailable),
			"mem_total", util.FormatBytes(stats.MemTotal), "mem_available", util.FormatBytes(stats.MemAvailable))
		if stats.SwapTotal > 0 {
			attrs = append(attrs, "swap_used", util.FormatBytes(stats.SwapUsed()), "swap_total", util.FormatBytes(stats.SwapTotal))
		}
	}
	r.log.Info("encoding progress", attrs...)
}

func (r *LogReporter) ValidationComplete(summary ValidationSummary) {
	for _, step := range summary.Steps {
		r.log.Info("validation step", "step", step.Name, "passed", step.Passed, "details", step.Details)
	}
	r.log.Info("validation result", "passed", summary.Passed, "steps", len(summary.Steps))
}

func (r *LogReporter) EncodingComplete(summary EncodingOutcome) {
	attrs := []any{
		"output", summary.OutputFile,
		"original_size", util.FormatBytesReadable(summary.OriginalSize),
		"encoded_size", util.FormatBytesReadable(summary.EncodedSize),
		"size_reduction_percent", util.CalculateSizeReduction(summary.OriginalSize, summary.EncodedSize),
	}
	if summary.VideoOriginalSize > 0 && summary.VideoEncodedSize > 0 {
		attrs = append(attrs, "video_original_size", util.FormatBytesReadable(summary.VideoOriginalSize),
			"video_encoded_size", util.FormatBytesReadable(summary.VideoEncodedSize),
			"video_size_reduction_percent", util.CalculateSizeReduction(summary.VideoOriginalSize, summary.VideoEncodedSize))
	}
	attrs = append(attrs,
		"video", summary.VideoStream,
		"audio", summary.AudioStream,
		"wall_time", summary.TotalTime.Round(time.Second).String(),
		"average_speed", summary.AverageSpeed,
		"saved_to", summary.OutputPath,
	)
	r.log.Info("encoding complete", attrs...)
}

func (r *LogReporter) Error(err ReporterError) {
	r.log.Error(err.Title,
		"event_type", "reel_error",
		"error_hint", err.Suggestion,
		"error", err.Message,
		"context", err.Context,
	)
}

func (r *LogReporter) OperationComplete(message string) {
	r.log.Info("operation complete", "message", message)
}

func (r *LogReporter) BatchStarted(info BatchStartInfo) {
	r.log.Info("batch started", "files", info.TotalFiles, "output_dir", info.OutputDir, "file_list", info.FileList)
}

func (r *LogReporter) FileProgress(context FileProgressContext) {
	r.log.Info("batch file", "current_file", context.CurrentFile, "total_files", context.TotalFiles)
}

func (r *LogReporter) BatchComplete(summary BatchSummary) {
	for _, result := range summary.FileResults {
		r.log.Info("batch file result", "file", result.Filename, "size_reduction_percent", result.Reduction)
	}
	r.log.Info("batch complete",
		"succeeded", summary.SuccessfulCount,
		"files", summary.TotalFiles,
		"validation_passed", summary.ValidationPassedCount,
		"validation_failed", summary.ValidationFailedCount,
		"original_size", util.FormatBytesReadable(summary.TotalOriginalSize),
		"encoded_size", util.FormatBytesReadable(summary.TotalEncodedSize),
		"size_reduction_percent", util.CalculateSizeReduction(summary.TotalOriginalSize, summary.TotalEncodedSize),
		"wall_time", util.FormatDurationFromSecs(int64(summary.TotalDuration.Seconds())),
		"average_speed", summary.AverageSpeed,
	)
}
