package reporter

import (
	"bytes"
	"log/slog"
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

func textLogger(out *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestLogReporterLifecycle(t *testing.T) {
	var out bytes.Buffer
	r := NewLogReporter(textLogger(&out))
	r.Hardware(HardwareSummary{Hostname: "host"})
	r.Initialization(InitializationSummary{InputFile: "in.mkv", OutputFile: "out.mkv", Duration: "1h", Resolution: "1920x1080", DynamicRange: "HDR", AudioDescription: "stereo"})
	r.StageProgress(StageProgress{Stage: "scanning", Message: "frames"})
	r.CropResult(CropSummary{Required: true, Crop: "1920:800", Message: "bars"})
	r.EncodingConfig(EncodingConfigSummary{Encoder: "SVT", EncoderVersion: "2.0", SVTAV1Params: "key=value"})
	r.EncodingStarted(200)
	r.EncodingProgress(ProgressSnapshot{Percent: 0, Speed: 2, MaxWorkers: 4, ActiveWorkers: 2, TargetWorkers: 3, ChunksComplete: 1, ChunksTotal: 9})
	r.EncodingProgress(ProgressSnapshot{Percent: 4}) // Same 5% bucket: no log entry.
	r.EncodingProgress(ProgressSnapshot{Percent: 10})
	r.EncodingProgress(ProgressSnapshot{Percent: 105}) // Beyond 100%: ignored.
	r.ValidationComplete(ValidationSummary{Steps: []ValidationStep{{Name: "audio", Details: "bad"}}})
	r.EncodingComplete(EncodingOutcome{OutputFile: "out.mkv", OriginalSize: 1000, EncodedSize: 500, VideoOriginalSize: 800, VideoEncodedSize: 400, TotalTime: time.Minute, OutputPath: "/out.mkv"})
	r.Error(ReporterError{Title: "failed", Message: "reason", Context: "file", Suggestion: "retry"})
	r.OperationComplete("done")
	r.BatchStarted(BatchStartInfo{TotalFiles: 2, OutputDir: "/out", FileList: []string{"a", "b"}})
	r.FileProgress(FileProgressContext{CurrentFile: 1, TotalFiles: 2})
	r.BatchComplete(BatchSummary{TotalFiles: 2, SuccessfulCount: 1, FileResults: []FileResult{{Filename: "a", Reduction: 50}}})

	log := out.String()
	requireLogContains(t, log, "hostname=host", "input=in.mkv", "stage=scanning", "crop=1920:800", "encoder_version=2.0",
		`svtav1_params="key=value"`, "total_frames=200", "workers_max=4", "chunks_total=9", "step=audio passed=false details=bad",
		"video_size_reduction_percent=50", "saved_to=/out.mkv", "level=ERROR msg=failed event_type=reel_error error_hint=retry error=reason context=file",
		"message=done", "file_list=\"[a b]\"", "current_file=1", "file=a size_reduction_percent=50", "succeeded=1")
	if got := strings.Count(log, `msg="encoding progress"`); got != 2 {
		t.Errorf("progress logged %d times, want 2", got)
	}
	r.EncodingStarted(50)
	r.EncodingProgress(ProgressSnapshot{Percent: 0})
	if got := strings.Count(out.String(), `msg="encoding progress"`); got != 3 {
		t.Errorf("progress bucket not reset: got %d entries", got)
	}
}

func TestCompositeReporterFansOut(t *testing.T) {
	var a, b bytes.Buffer
	r := NewCompositeReporter(NewLogReporter(textLogger(&a)), NewLogReporter(textLogger(&b)))
	r.Hardware(HardwareSummary{Hostname: "host"})
	r.Initialization(InitializationSummary{InputFile: "in"})
	r.StageProgress(StageProgress{Stage: "scan"})
	r.CropResult(CropSummary{Disabled: true})
	r.EncodingConfig(EncodingConfigSummary{Encoder: "SVT"})
	r.EncodingStarted(10)
	r.EncodingProgress(ProgressSnapshot{Percent: 5})
	r.ValidationComplete(ValidationSummary{Passed: true})
	r.EncodingComplete(EncodingOutcome{OutputFile: "out"})
	r.Error(ReporterError{Title: "error"})
	r.OperationComplete("done")
	r.BatchStarted(BatchStartInfo{TotalFiles: 1})
	r.FileProgress(FileProgressContext{CurrentFile: 1})
	r.BatchComplete(BatchSummary{TotalFiles: 1})
	for _, out := range []*bytes.Buffer{&a, &b} {
		requireLogContains(t, out.String(), "hostname=host", "input=in", "stage=scan", "disabled=true", "encoder=SVT", "total_frames=10",
			"percent=5", "validation result", "output=out", "msg=error", "operation complete", "batch started", "batch file", "batch complete")
	}
}
