package reporter

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/five82/reel/internal/util"
)

// LogReporter writes encoding events to a log file.
type LogReporter struct {
	w                  io.Writer
	mu                 sync.Mutex
	lastProgressBucket int // Track progress in 5% buckets
}

// NewLogReporter creates a new log reporter that writes to the given writer.
func NewLogReporter(w io.Writer) *LogReporter {
	return &LogReporter{
		w:                  w,
		lastProgressBucket: -1,
	}
}

func (r *LogReporter) log(level, format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(r.w, "%s [%s] %s\n", timestamp, level, msg)
}

func (r *LogReporter) Hardware(summary HardwareSummary) {
	r.log("INFO", "=== HARDWARE ===")
	r.log("INFO", "Hostname: %s", summary.Hostname)
}

func (r *LogReporter) Initialization(summary InitializationSummary) {
	r.log("INFO", "=== VIDEO ===")
	r.log("INFO", "Input: %s", summary.InputFile)
	r.log("INFO", "Output: %s", summary.OutputFile)
	r.log("INFO", "Duration: %s", summary.Duration)
	r.log("INFO", "Resolution: %s", summary.Resolution)
	r.log("INFO", "Dynamic range: %s", summary.DynamicRange)
	r.log("INFO", "Audio: %s", summary.AudioDescription)
}

func (r *LogReporter) StageProgress(update StageProgress) {
	r.log("INFO", "[%s] %s", strings.ToUpper(update.Stage), update.Message)
}

func (r *LogReporter) CropResult(summary CropSummary) {
	if summary.Disabled {
		r.log("INFO", "Crop detection: disabled")
	} else if summary.Required {
		r.log("INFO", "Crop detection: %s (%s)", summary.Message, summary.Crop)
	} else {
		r.log("INFO", "Crop detection: %s (no crop needed)", summary.Message)
	}
}

func (r *LogReporter) EncodingConfig(summary EncodingConfigSummary) {
	r.log("INFO", "=== ENCODING CONFIG ===")
	r.log("INFO", "Encoder: %s", summary.Encoder)
	if summary.EncoderVersion != "" {
		r.log("INFO", "SVT version: %s", summary.EncoderVersion)
	}
	r.log("INFO", "Preset: %s", summary.Preset)
	r.log("INFO", "Tune: %s", summary.Tune)
	r.log("INFO", "Quality: %s", summary.Quality)
	r.log("INFO", "Pixel format: %s", summary.PixelFormat)
	r.log("INFO", "Matrix: %s", summary.MatrixCoefficients)
	r.log("INFO", "Audio codec: %s", summary.AudioCodec)
	r.log("INFO", "Audio: %s", summary.AudioDescription)

	if summary.SVTAV1Params != "" {
		r.log("INFO", "SVT params: %s", summary.SVTAV1Params)
	}
}

func (r *LogReporter) EncodingStarted(totalFrames uint64) {
	r.mu.Lock()
	r.lastProgressBucket = -1
	r.mu.Unlock()
	r.log("INFO", "=== ENCODING STARTED === (total frames: %d)", totalFrames)
}

func (r *LogReporter) EncodingProgress(progress ProgressSnapshot) {
	// Log progress at 5% intervals
	bucket := int(progress.Percent / 5)
	r.mu.Lock()
	if bucket > r.lastProgressBucket && bucket <= 20 {
		r.lastProgressBucket = bucket
		r.mu.Unlock()
		workerText := ""
		if progress.MaxWorkers > 0 {
			workerText = fmt.Sprintf(", workers %d/%d active/target, max %d", progress.ActiveWorkers, progress.TargetWorkers, progress.MaxWorkers)
		}
		memoryText := ""
		if stats, ok := util.ReadMemoryStats(); ok && stats.MemTotal > 0 {
			used := stats.MemTotal - stats.MemAvailable
			memoryText = fmt.Sprintf(", mem %s/%s avail %s", util.FormatBytes(used), util.FormatBytes(stats.MemTotal), util.FormatBytes(stats.MemAvailable))
			if stats.SwapTotal > 0 {
				memoryText += fmt.Sprintf(", swap %s/%s", util.FormatBytes(stats.SwapUsed()), util.FormatBytes(stats.SwapTotal))
			}
		}
		r.log("INFO", "Progress: %.0f%% (speed %.1fx avg / %.1fx recent, fps %.1f, eta %s%s%s)",
			progress.Percent, progress.Speed, progress.RecentSpeed, progress.FPS,
			util.FormatDurationFromSecs(int64(progress.ETA.Seconds())), workerText, memoryText)
	} else {
		r.mu.Unlock()
	}
}

func (r *LogReporter) ValidationComplete(summary ValidationSummary) {
	r.log("INFO", "=== VALIDATION ===")
	if summary.Passed {
		r.log("INFO", "Result: PASSED")
	} else {
		r.log("WARN", "Result: FAILED")
	}

	for _, step := range summary.Steps {
		status := "ok"
		if !step.Passed {
			status = "FAILED"
		}
		r.log("INFO", "  - %s: %s (%s)", step.Name, status, step.Details)
	}
}

func (r *LogReporter) EncodingComplete(summary EncodingOutcome) {
	reduction := util.CalculateSizeReduction(summary.OriginalSize, summary.EncodedSize)

	r.log("INFO", "=== RESULTS ===")
	r.log("INFO", "Output: %s", summary.OutputFile)
	r.log("INFO", "Size: %s -> %s (%.1f%% reduction)",
		util.FormatBytesReadable(summary.OriginalSize),
		util.FormatBytesReadable(summary.EncodedSize),
		reduction)
	if summary.VideoOriginalSize > 0 && summary.VideoEncodedSize > 0 {
		videoReduction := util.CalculateSizeReduction(summary.VideoOriginalSize, summary.VideoEncodedSize)
		r.log("INFO", "Video size: %s -> %s (%.1f%% reduction)",
			util.FormatBytesReadable(summary.VideoOriginalSize),
			util.FormatBytesReadable(summary.VideoEncodedSize),
			videoReduction)
	}
	r.log("INFO", "Video: %s", summary.VideoStream)
	r.log("INFO", "Audio: %s", summary.AudioStream)
	r.log("INFO", "Time: %s (avg speed %.1fx)",
		util.FormatDurationFromSecs(int64(summary.TotalTime.Seconds())),
		summary.AverageSpeed)
	r.log("INFO", "Saved to: %s", summary.OutputPath)
}

func (r *LogReporter) Warning(message string) {
	r.log("WARN", "%s", message)
}

func (r *LogReporter) Error(err ReporterError) {
	r.log("ERROR", "%s: %s", err.Title, err.Message)
	if err.Context != "" {
		r.log("ERROR", "  Context: %s", err.Context)
	}
	if err.Suggestion != "" {
		r.log("ERROR", "  Suggestion: %s", err.Suggestion)
	}
}

func (r *LogReporter) OperationComplete(message string) {
	r.log("INFO", "=== COMPLETE === %s", message)
}

func (r *LogReporter) BatchStarted(info BatchStartInfo) {
	r.log("INFO", "=== BATCH STARTED ===")
	r.log("INFO", "Processing %d files -> %s", info.TotalFiles, info.OutputDir)
	for i, name := range info.FileList {
		r.log("INFO", "  %d. %s", i+1, name)
	}
}

func (r *LogReporter) FileProgress(context FileProgressContext) {
	r.log("INFO", "--- File %d of %d ---", context.CurrentFile, context.TotalFiles)
}

func (r *LogReporter) BatchComplete(summary BatchSummary) {
	reduction := util.CalculateSizeReduction(summary.TotalOriginalSize, summary.TotalEncodedSize)

	r.log("INFO", "=== BATCH COMPLETE ===")
	r.log("INFO", "%d of %d succeeded", summary.SuccessfulCount, summary.TotalFiles)
	r.log("INFO", "Validation: %d passed, %d failed", summary.ValidationPassedCount, summary.ValidationFailedCount)
	r.log("INFO", "Size: %s -> %s (%.1f%% reduction)",
		util.FormatBytesReadable(summary.TotalOriginalSize),
		util.FormatBytesReadable(summary.TotalEncodedSize),
		reduction)
	r.log("INFO", "Time: %s (avg speed %.1fx)",
		util.FormatDurationFromSecs(int64(summary.TotalDuration.Seconds())),
		summary.AverageSpeed)

	for _, result := range summary.FileResults {
		r.log("INFO", "  - %s (%.1f%% reduction)", result.Filename, result.Reduction)
	}
}

func (r *LogReporter) Verbose(message string) {
	r.log("DEBUG", "%s", message)
}
