package reporter

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/five82/reel/internal/util"
	"github.com/schollz/progressbar/v3"
)

// TerminalReporter outputs human-friendly text to the terminal.
type TerminalReporter struct {
	mu                  sync.Mutex
	progress            *progressbar.ProgressBar
	maxPercent          float32
	lastStage           string
	lastVerboseProgress time.Time
	verbose             bool
	cyan                *color.Color
	green               *color.Color
	yellow              *color.Color
	red                 *color.Color
	magenta             *color.Color
	bold                *color.Color
	dim                 *color.Color
}

// NewTerminalReporter creates a new terminal reporter with verbose mode disabled.
func NewTerminalReporter() *TerminalReporter {
	return NewTerminalReporterVerbose(false)
}

// NewTerminalReporterVerbose creates a new terminal reporter with configurable verbose mode.
func NewTerminalReporterVerbose(verbose bool) *TerminalReporter {
	return &TerminalReporter{
		verbose: verbose,
		cyan:    color.New(color.FgCyan, color.Bold),
		green:   color.New(color.FgGreen),
		yellow:  color.New(color.FgYellow, color.Bold),
		red:     color.New(color.FgRed, color.Bold),
		magenta: color.New(color.FgMagenta),
		bold:    color.New(color.Bold),
		dim:     color.New(color.Faint),
	}
}

func (r *TerminalReporter) finishProgress() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.progress != nil {
		_ = r.progress.Finish()
		r.progress = nil
	}
	r.maxPercent = 0
}

func (r *TerminalReporter) Hardware(summary HardwareSummary) {
	fmt.Println()
	_, _ = r.cyan.Println("HARDWARE")
	r.printLabel("Hostname:", summary.Hostname)
}

// labelWidth is the global width for all labels to ensure consistent alignment.
// Set to 18 to accommodate "Audio/video sync:" in validation output.
const labelWidth = 18

// printLabel prints a bold label with fixed width padding followed by a value.
func (r *TerminalReporter) printLabel(label, value string) {
	paddedLabel := fmt.Sprintf("%-*s", labelWidth, label)
	fmt.Printf("  %s %s\n", r.bold.Sprint(paddedLabel), value)
}

func (r *TerminalReporter) Initialization(summary InitializationSummary) {
	fmt.Println()
	_, _ = r.cyan.Println("VIDEO")
	r.printLabel("File:", summary.InputFile)
	r.printLabel("Output:", summary.OutputFile)
	r.printLabel("Duration:", summary.Duration)
	r.printLabel("Resolution:", summary.Resolution)
	r.printLabel("Dynamic:", summary.DynamicRange)
	r.printLabel("Audio:", summary.AudioDescription)
}

func (r *TerminalReporter) StageProgress(update StageProgress) {
	r.mu.Lock()
	if r.lastStage != update.Stage {
		r.mu.Unlock()
		fmt.Println()
		_, _ = r.cyan.Println(strings.ToUpper(update.Stage))
		r.mu.Lock()
		r.lastStage = update.Stage
	}
	r.mu.Unlock()
	fmt.Printf("  %s %s\n", r.magenta.Sprint("›"), update.Message)
}

func (r *TerminalReporter) CropResult(summary CropSummary) {
	var status string
	if summary.Disabled {
		status = color.New(color.Faint).Sprint("auto-crop disabled")
	} else if summary.Required {
		status = r.green.Sprint(summary.Crop)
	} else {
		status = color.New(color.Faint).Sprint("no crop needed")
	}
	r.printLabel("Crop detection:", fmt.Sprintf("%s (%s)", summary.Message, status))
}

func (r *TerminalReporter) EncodingConfig(summary EncodingConfigSummary) {
	fmt.Println()
	_, _ = r.cyan.Println("ENCODING")
	r.printLabel("Encoder:", summary.Encoder)
	if summary.EncoderVersion != "" {
		r.printLabel("SVT version:", summary.EncoderVersion)
	}
	r.printLabel("Preset:", summary.Preset)
	r.printLabel("Tune:", summary.Tune)
	r.printLabel("Quality:", summary.Quality)
	r.printLabel("Pixel format:", summary.PixelFormat)
	r.printLabel("Matrix:", summary.MatrixCoefficients)
	r.printLabel("Audio codec:", summary.AudioCodec)
	r.printLabel("Audio:", summary.AudioDescription)

	if summary.SVTAV1Params != "" {
		r.printLabel("SVT params:", summary.SVTAV1Params)
	}
}

func (r *TerminalReporter) EncodingStarted(totalFrames uint64) {
	r.finishProgress()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.verbose {
		r.lastVerboseProgress = time.Time{}
		fmt.Printf("  %s Encoding started (%s frames)\n", r.dim.Sprint("›"), compactCount(totalFrames))
		return
	}

	r.progress = progressbar.NewOptions64(
		100,
		progressbar.OptionSetDescription(""),
		progressbar.OptionSetWidth(40),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetPredictTime(false),
		progressbar.OptionThrottle(time.Second),
		progressbar.OptionShowDescriptionAtLineEnd(),
		progressbar.OptionSetElapsedTime(false),
		progressbar.OptionClearOnFinish(),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "Encoding [",
			BarEnd:        "]",
		}),
	)
}

func (r *TerminalReporter) EncodingProgress(progress ProgressSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.verbose {
		r.printVerboseEncodingProgress(progress)
		return
	}

	if r.progress == nil {
		return
	}

	clamped := progress.Percent
	if clamped > 100 {
		clamped = 100
	}
	if clamped < 0 {
		clamped = 0
	}

	if clamped >= r.maxPercent {
		r.maxPercent = clamped
		_ = r.progress.Set64(int64(clamped))
	}

	var desc string
	if progress.ChunksTotal > 0 {
		// Chunked encoding: show chunk progress with frame counts
		workerText := ""
		if progress.MaxWorkers > 0 {
			if progress.ActiveWorkers != progress.TargetWorkers {
				workerText = fmt.Sprintf(", workers %d→%d/%d", progress.ActiveWorkers, progress.TargetWorkers, progress.MaxWorkers)
			} else {
				workerText = fmt.Sprintf(", workers %d/%d", progress.ActiveWorkers, progress.MaxWorkers)
			}
		}
		desc = fmt.Sprintf("chunks %d/%d (%s/%s)%s, speed %.1fx avg / %.1fx recent, eta %s",
			progress.ChunksComplete, progress.ChunksTotal,
			compactCount(progress.CurrentFrame), compactCount(progress.TotalFrames), workerText,
			progress.Speed, progress.RecentSpeed, util.FormatDurationFromSecs(int64(progress.ETA.Seconds())))
	} else {
		// Traditional encoding: show fps
		desc = fmt.Sprintf("speed %.1fx, fps %.1f, eta %s",
			progress.Speed, progress.FPS, util.FormatDurationFromSecs(int64(progress.ETA.Seconds())))
	}
	r.progress.Describe(desc)
}

func (r *TerminalReporter) printVerboseEncodingProgress(progress ProgressSnapshot) {
	now := time.Now()
	if !r.lastVerboseProgress.IsZero() && now.Sub(r.lastVerboseProgress) < 5*time.Second {
		return
	}
	r.lastVerboseProgress = now

	workerText := ""
	if progress.MaxWorkers > 0 {
		if progress.ActiveWorkers != progress.TargetWorkers {
			workerText = fmt.Sprintf(", workers %d→%d/%d", progress.ActiveWorkers, progress.TargetWorkers, progress.MaxWorkers)
		} else {
			workerText = fmt.Sprintf(", workers %d/%d", progress.ActiveWorkers, progress.MaxWorkers)
		}
	}

	memoryText := ""
	if stats, ok := util.ReadMemoryStats(); ok && stats.MemTotal > 0 {
		used := stats.MemTotal - stats.MemAvailable
		memoryText = fmt.Sprintf(", mem %s/%s avail %s", util.FormatBytes(used), util.FormatBytes(stats.MemTotal), util.FormatBytes(stats.MemAvailable))
		if stats.SwapTotal > 0 {
			memoryText += fmt.Sprintf(", swap %s/%s", util.FormatBytes(stats.SwapUsed()), util.FormatBytes(stats.SwapTotal))
		}
	}

	fmt.Printf("  %s %.0f%% chunks %d/%d (%s/%s)%s, speed %.1fx avg / %.1fx recent, eta %s%s\n",
		r.dim.Sprint("›"), progress.Percent,
		progress.ChunksComplete, progress.ChunksTotal,
		compactCount(progress.CurrentFrame), compactCount(progress.TotalFrames), workerText,
		progress.Speed, progress.RecentSpeed, util.FormatDurationFromSecs(int64(progress.ETA.Seconds())), memoryText)
}

// compactCount formats a large number compactly (e.g., 45678 -> "45.7k").
func compactCount(n uint64) string {
	if n < 10_000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
}

func (r *TerminalReporter) ValidationComplete(summary ValidationSummary) {
	r.finishProgress()

	fmt.Println()
	_, _ = r.cyan.Println("VALIDATION")

	if summary.Passed {
		r.printLabel("Status:", fmt.Sprintf("%s %s", r.green.Sprint("✓"), r.green.Add(color.Bold).Sprint("All checks passed")))
	} else {
		r.printLabel("Status:", fmt.Sprintf("%s %s", r.red.Sprint("✗"), r.red.Sprint("Validation failed")))
	}

	for _, step := range summary.Steps {
		var status string
		if step.Passed {
			status = r.green.Sprint("✓")
		} else {
			status = r.red.Sprint("✗")
		}
		r.printLabel(step.Name+":", fmt.Sprintf("%s %s", status, step.Details))
	}
}

func (r *TerminalReporter) EncodingComplete(summary EncodingOutcome) {
	reduction := util.CalculateSizeReduction(summary.OriginalSize, summary.EncodedSize)

	fmt.Println()
	_, _ = r.cyan.Println("RESULTS")
	r.printLabel("Output:", summary.OutputFile)
	r.printLabel("Size:", fmt.Sprintf("%s -> %s",
		util.FormatBytesReadable(summary.OriginalSize),
		util.FormatBytesReadable(summary.EncodedSize)))
	r.printLabel("Reduction:", fmt.Sprintf("%.1f%%", reduction))
	if summary.VideoOriginalSize > 0 && summary.VideoEncodedSize > 0 {
		videoReduction := util.CalculateSizeReduction(summary.VideoOriginalSize, summary.VideoEncodedSize)
		r.printLabel("Video size:", fmt.Sprintf("%s -> %s",
			util.FormatBytesReadable(summary.VideoOriginalSize),
			util.FormatBytesReadable(summary.VideoEncodedSize)))
		r.printLabel("Video reduction:", fmt.Sprintf("%.1f%%", videoReduction))
	}
	r.printLabel("Video:", summary.VideoStream)
	r.printLabel("Audio:", summary.AudioStream)
	r.printLabel("Time:", fmt.Sprintf("%s (avg speed %.1fx)",
		util.FormatDurationFromSecs(int64(summary.TotalTime.Seconds())),
		summary.AverageSpeed))
	r.printLabel("Saved to:", r.green.Sprint(summary.OutputPath))
}

func (r *TerminalReporter) Warning(message string) {
	fmt.Println()
	_, _ = r.yellow.Printf("WARN: %s\n", message)
}

func (r *TerminalReporter) Error(err ReporterError) {
	_, _ = fmt.Fprintln(os.Stderr)
	_, _ = r.red.Fprintf(os.Stderr, "ERROR %s\n", err.Title)
	_, _ = fmt.Fprintf(os.Stderr, "  %s\n", err.Message)
	if err.Context != "" {
		_, _ = fmt.Fprintf(os.Stderr, "  Context: %s\n", err.Context)
	}
	if err.Suggestion != "" {
		_, _ = fmt.Fprintf(os.Stderr, "  Suggestion: %s\n", err.Suggestion)
	}
}

func (r *TerminalReporter) OperationComplete(message string) {
	fmt.Println()
	fmt.Printf("%s %s\n", r.green.Add(color.Bold).Sprint("✓"), r.bold.Sprint(message))
}

func (r *TerminalReporter) BatchStarted(info BatchStartInfo) {
	fmt.Println()
	_, _ = r.cyan.Println("BATCH")
	fmt.Printf("  Processing %d files -> %s\n", info.TotalFiles, r.bold.Sprint(info.OutputDir))
	for i, name := range info.FileList {
		fmt.Printf("  %d. %s\n", i+1, name)
	}
}

func (r *TerminalReporter) FileProgress(context FileProgressContext) {
	fmt.Printf("\nFile %s of %d\n",
		r.bold.Sprint(context.CurrentFile),
		context.TotalFiles)
}

func (r *TerminalReporter) BatchComplete(summary BatchSummary) {
	reduction := util.CalculateSizeReduction(summary.TotalOriginalSize, summary.TotalEncodedSize)

	fmt.Println()
	_, _ = r.cyan.Println("BATCH SUMMARY")
	fmt.Printf("  %s\n", r.bold.Sprintf("%d of %d succeeded", summary.SuccessfulCount, summary.TotalFiles))
	fmt.Printf("  Validation: %s passed, %s failed\n",
		r.green.Sprint(summary.ValidationPassedCount),
		r.red.Sprint(summary.ValidationFailedCount))
	fmt.Printf("  Size: %d -> %d bytes (%.1f%% reduction)\n",
		summary.TotalOriginalSize, summary.TotalEncodedSize, reduction)
	fmt.Printf("  Time: %s (avg speed %.1fx)\n",
		util.FormatDurationFromSecs(int64(summary.TotalDuration.Seconds())),
		summary.AverageSpeed)

	for _, result := range summary.FileResults {
		fmt.Printf("  - %s (%.1f%% reduction)\n", result.Filename, result.Reduction)
	}
}

func (r *TerminalReporter) Verbose(message string) {
	if !r.verbose {
		return
	}
	fmt.Printf("  %s %s\n", r.dim.Sprint("›"), r.dim.Sprint(message))
}
