package reporter

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"
)

func captureTerminal(t *testing.T, run func()) (string, string) {
	t.Helper()
	oldOut, oldErr, oldColor := os.Stdout, os.Stderr, color.Output
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr, color.Output = outW, errW, outW
	var stdout, stderr string
	doneOut, doneErr := make(chan struct{}), make(chan struct{})
	go func() { b, _ := io.ReadAll(outR); stdout = string(b); close(doneOut) }()
	go func() { b, _ := io.ReadAll(errR); stderr = string(b); close(doneErr) }()
	defer func() {
		os.Stdout, os.Stderr, color.Output = oldOut, oldErr, oldColor
		_ = outW.Close()
		_ = errW.Close()
		<-doneOut
		<-doneErr
		_ = outR.Close()
		_ = errR.Close()
	}()
	run()
	_ = outW.Close()
	_ = errW.Close()
	<-doneOut
	<-doneErr
	return stdout, stderr
}

func TestTerminalReporterLifecycle(t *testing.T) {
	// Capture global streams without parallel tests; the progress bar writes to stderr.
	var r *TerminalReporter
	out, errOut := captureTerminal(t, func() {
		r = NewTerminalReporter()
		r.Hardware(HardwareSummary{Hostname: "host"})
		r.Initialization(InitializationSummary{InputFile: "in.mkv", OutputFile: "out.mkv", Duration: "1h", Resolution: "1080p", DynamicRange: "HDR", AudioDescription: "stereo"})
		r.StageProgress(StageProgress{Stage: "scanning", Message: "first"})
		r.StageProgress(StageProgress{Stage: "scanning", Message: "second"})
		r.StageProgress(StageProgress{Stage: "sampling", Message: "third"})
		r.CropResult(CropSummary{Disabled: true, Message: "skipped"})
		r.CropResult(CropSummary{Required: true, Crop: "1920:800", Message: "bars"})
		r.CropResult(CropSummary{Message: "clear"})
		r.EncodingConfig(EncodingConfigSummary{Encoder: "SVT", EncoderVersion: "2.0", Preset: "6", Tune: "0", Quality: "30", PixelFormat: "10bit", MatrixCoefficients: "bt709", AudioCodec: "opus", AudioDescription: "stereo", SVTAV1Params: "key=value"})
		r.EncodingStarted(100)
		r.EncodingProgress(ProgressSnapshot{Percent: -10, Speed: 2, FPS: 24, ETA: time.Minute})
		r.EncodingProgress(ProgressSnapshot{Percent: 25, ChunksTotal: 4, ChunksComplete: 1, CurrentFrame: 10000, TotalFrames: 40000, MaxWorkers: 4, ActiveWorkers: 2, TargetWorkers: 3})
		r.EncodingProgress(ProgressSnapshot{Percent: 20}) // Progress must never regress.
		if r.maxPercent != 25 {
			t.Errorf("regressed progress to %v", r.maxPercent)
		}
		r.EncodingProgress(ProgressSnapshot{Percent: 120, ChunksTotal: 4, MaxWorkers: 4, ActiveWorkers: 3, TargetWorkers: 3})
		if r.maxPercent != 100 {
			t.Errorf("progress not clamped: %v", r.maxPercent)
		}
		r.ValidationComplete(ValidationSummary{Passed: false, Steps: []ValidationStep{{Name: "video", Passed: true, Details: "ok"}, {Name: "audio", Details: "bad"}}})
		if r.progress != nil || r.maxPercent != 0 {
			t.Error("validation did not finish progress")
		}
		r.ValidationComplete(ValidationSummary{Passed: true})
		r.EncodingComplete(EncodingOutcome{OutputFile: "out.mkv", OriginalSize: 1000, EncodedSize: 500, VideoOriginalSize: 800, VideoEncodedSize: 400, VideoStream: "AV1", AudioStream: "Opus", TotalTime: time.Minute, AverageSpeed: 2, OutputPath: "/out.mkv"})
		r.EncodingComplete(EncodingOutcome{OriginalSize: 100, EncodedSize: 50})
		r.Warning("careful")
		r.Error(ReporterError{Title: "failed", Message: "reason", Context: "file", Suggestion: "retry"})
		r.Error(ReporterError{Title: "oops", Message: "plain"})
		r.OperationComplete("done")
		r.BatchStarted(BatchStartInfo{TotalFiles: 2, OutputDir: "/out", FileList: []string{"a", "b"}})
		r.FileProgress(FileProgressContext{CurrentFile: 1, TotalFiles: 2})
		r.BatchComplete(BatchSummary{TotalFiles: 2, SuccessfulCount: 1, ValidationPassedCount: 1, ValidationFailedCount: 1, TotalOriginalSize: 100, TotalEncodedSize: 50, TotalDuration: time.Minute, AverageSpeed: 2, FileResults: []FileResult{{Filename: "a", Reduction: 50}}})
		r.Verbose("hidden")
	})
	requireLogContains(t, out, "HARDWARE", "Hostname:", "host", "VIDEO", "in.mkv", "SCANNING", "SAMPLING", "auto-crop disabled", "1920:800", "no crop needed", "ENCODING", "SVT version:", "SVT params:", "VALIDATION", "Validation failed", "All checks passed", "video:", "audio:", "RESULTS", "Video reduction:", "Saved to:", "careful", "done", "BATCH", "1. a", "2. b", "File 1 of 2", "BATCH SUMMARY", "1 of 2 succeeded", "a (50.0% reduction)")
	requireLogContains(t, errOut, "ERROR failed", "reason", "Context: file", "Suggestion: retry", "ERROR oops")
	if strings.Count(out, "SCANNING") != 1 || strings.Contains(out, "hidden") || strings.Count(out, "Video reduction:") != 1 {
		t.Errorf("unexpected terminal output:\n%s", out)
	}
}

func TestTerminalReporterVerboseProgress(t *testing.T) {
	out, _ := captureTerminal(t, func() {
		r := NewTerminalReporterVerbose(true)
		r.EncodingStarted(10000)
		r.EncodingProgress(ProgressSnapshot{Percent: 10, ChunksTotal: 4, ChunksComplete: 1, CurrentFrame: 10000, TotalFrames: 1000000, MaxWorkers: 4, ActiveWorkers: 2, TargetWorkers: 3, ETA: time.Minute})
		r.EncodingProgress(ProgressSnapshot{Percent: 20}) // Throttled.
		r.lastVerboseProgress = time.Now().Add(-6 * time.Second)
		r.EncodingProgress(ProgressSnapshot{Percent: 30, MaxWorkers: 4, ActiveWorkers: 3, TargetWorkers: 3})
		r.Verbose("details")
	})
	requireLogContains(t, out, "Encoding started (10.0k frames)", "10% chunks 1/4 (10.0k/1.0m)", "workers 2→3/4", "30% chunks", "workers 3/4", "details")
	if strings.Contains(out, "20% chunks") {
		t.Errorf("unthrottled progress: %s", out)
	}
}

func TestCompactCount(t *testing.T) {
	for _, tc := range []struct {
		n    uint64
		want string
	}{{9999, "9999"}, {10000, "10.0k"}, {45678, "45.7k"}, {1000000, "1.0m"}} {
		if got := compactCount(tc.n); got != tc.want {
			t.Errorf("compactCount(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
