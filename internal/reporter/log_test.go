package reporter

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func requireLogContains(t *testing.T, log string, want ...string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(log, s) {
			t.Errorf("log missing %q:\n%s", s, log)
		}
	}
}

func TestLogReporterLifecycle(t *testing.T) {
	var out bytes.Buffer
	r := NewLogReporter(&out)
	r.Hardware(HardwareSummary{Hostname: "host"})
	r.Initialization(InitializationSummary{InputFile: "in.mkv", OutputFile: "out.mkv", Duration: "1h", Resolution: "1920x1080", DynamicRange: "HDR", AudioDescription: "stereo"})
	r.StageProgress(StageProgress{Stage: "scanning", Message: "frames"})
	for _, crop := range []CropSummary{{Disabled: true}, {Required: true, Crop: "1920:800", Message: "bars"}, {Message: "clear"}} {
		r.CropResult(crop)
	}
	r.EncodingConfig(EncodingConfigSummary{Encoder: "SVT", EncoderVersion: "2.0", Preset: "6", Tune: "0", Quality: "30", PixelFormat: "yuv420p10le", MatrixCoefficients: "bt709", AudioCodec: "opus", AudioDescription: "stereo", SVTAV1Params: "key=value"})
	r.EncodingConfig(EncodingConfigSummary{Encoder: "SVT"})
	r.EncodingStarted(200)
	r.EncodingProgress(ProgressSnapshot{Percent: 0, Speed: 2, RecentSpeed: 3, FPS: 48, ETA: time.Minute, MaxWorkers: 4, ActiveWorkers: 2, TargetWorkers: 3})
	r.EncodingProgress(ProgressSnapshot{Percent: 4}) // Same 5% bucket: no log entry.
	r.EncodingProgress(ProgressSnapshot{Percent: 10})
	r.EncodingProgress(ProgressSnapshot{Percent: 105}) // Beyond 100%: ignored.
	r.ValidationComplete(ValidationSummary{Passed: true, Steps: []ValidationStep{{Name: "sync", Passed: true, Details: "ok"}, {Name: "video", Details: "bad"}}})
	r.ValidationComplete(ValidationSummary{Steps: []ValidationStep{{Name: "audio", Details: "bad"}}})
	r.EncodingComplete(EncodingOutcome{OutputFile: "out.mkv", OriginalSize: 1000, EncodedSize: 500, VideoOriginalSize: 800, VideoEncodedSize: 400, VideoStream: "AV1", AudioStream: "Opus", TotalTime: time.Minute, AverageSpeed: 2, OutputPath: "/out.mkv"})
	r.EncodingComplete(EncodingOutcome{OriginalSize: 100, EncodedSize: 50})
	r.Warning("careful")
	r.Error(ReporterError{Title: "failed", Message: "reason", Context: "file", Suggestion: "retry"})
	r.Error(ReporterError{Title: "oops", Message: "plain"})
	r.OperationComplete("done")
	r.BatchStarted(BatchStartInfo{TotalFiles: 2, OutputDir: "/out", FileList: []string{"a", "b"}})
	r.FileProgress(FileProgressContext{CurrentFile: 1, TotalFiles: 2})
	r.BatchComplete(BatchSummary{TotalFiles: 2, SuccessfulCount: 1, ValidationPassedCount: 1, ValidationFailedCount: 1, TotalOriginalSize: 100, TotalEncodedSize: 50, TotalDuration: time.Minute, AverageSpeed: 2, FileResults: []FileResult{{Filename: "a", Reduction: 50}}})
	r.Verbose("details")

	log := out.String()
	requireLogContains(t, log, "Hostname: host", "Input: in.mkv", "[SCANNING] frames", "Crop detection: disabled", "bars (1920:800)", "clear (no crop needed)", "SVT version: 2.0", "SVT params: key=value", "total frames: 200", "workers 2/3 active/target, max 4", "Result: PASSED", "Result: FAILED", "sync: ok (ok)", "video: FAILED (bad)", "Video size:", "Saved to: /out.mkv", "[WARN] careful", "[ERROR] failed: reason", "Context: file", "Suggestion: retry", "=== COMPLETE === done", "Processing 2 files -> /out", "1. a", "2. b", "File 1 of 2", "1 of 2 succeeded", "a (50.0% reduction)", "[DEBUG] details")
	if got := strings.Count(log, "Progress:"); got != 2 {
		t.Errorf("progress logged %d times, want 2", got)
	}
	if got := strings.Count(log, "Video size:"); got != 1 {
		t.Errorf("video size logged %d times, want 1", got)
	}
	r.EncodingStarted(50)
	r.EncodingProgress(ProgressSnapshot{Percent: 0})
	if got := strings.Count(out.String(), "Progress:"); got != 3 {
		t.Errorf("progress bucket not reset: got %d entries", got)
	}
}

func TestCompositeReporterFansOut(t *testing.T) {
	var a, b bytes.Buffer
	r := NewCompositeReporter(NewLogReporter(&a), NewLogReporter(&b))
	r.Hardware(HardwareSummary{Hostname: "host"})
	r.Initialization(InitializationSummary{InputFile: "in"})
	r.StageProgress(StageProgress{Stage: "scan"})
	r.CropResult(CropSummary{Disabled: true})
	r.EncodingConfig(EncodingConfigSummary{Encoder: "SVT"})
	r.EncodingStarted(10)
	r.EncodingProgress(ProgressSnapshot{Percent: 5})
	r.ValidationComplete(ValidationSummary{Passed: true})
	r.EncodingComplete(EncodingOutcome{OutputFile: "out"})
	r.Warning("warn")
	r.Error(ReporterError{Title: "error"})
	r.OperationComplete("done")
	r.BatchStarted(BatchStartInfo{TotalFiles: 1})
	r.FileProgress(FileProgressContext{CurrentFile: 1})
	r.BatchComplete(BatchSummary{TotalFiles: 1})
	r.Verbose("trace")
	// Timestamps may differ across a second boundary; compare the event bodies.
	for _, out := range []*bytes.Buffer{&a, &b} {
		requireLogContains(t, out.String(), "Hostname: host", "Input: in", "[SCAN]", "Crop detection: disabled", "Encoder: SVT", "total frames: 10", "Progress: 5%", "Result: PASSED", "Output: out", "[WARN] warn", "[ERROR] error", "COMPLETE", "BATCH STARTED", "File 1", "BATCH COMPLETE", "[DEBUG] trace")
	}
}
