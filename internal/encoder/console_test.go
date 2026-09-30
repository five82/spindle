package encoder

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/reel"
)

func TestConsoleReporterProgressAndDetails(t *testing.T) {
	var out bytes.Buffer
	r := &consoleReporter{out: &out}
	r.Initialization(reel.InitializationSummary{
		InputFile: "movie.mkv", Duration: "1h", Resolution: "1080p",
		DynamicRange: "SDR", AudioDescription: "stereo",
	})
	r.EncodingProgress(reel.ProgressSnapshot{
		Percent: 5, FPS: 25, ETA: 2 * time.Minute, ChunksComplete: 1, ChunksTotal: 4,
	})
	// Frequent sub-5% updates are throttled, while a 5% jump prints again.
	r.EncodingProgress(reel.ProgressSnapshot{Percent: 8})
	r.EncodingProgress(reel.ProgressSnapshot{Percent: 10})
	r.ValidationComplete(reel.ValidationSummary{Passed: true})
	got := out.String()
	for _, want := range []string{"movie.mkv", "Audio:      stereo", "5.0%", "25 fps", "ETA 2m0s", "chunks 1/4", "10.0%", "Validation: passed"} {
		if !strings.Contains(got, want) {
			t.Errorf("console output missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "8.0%") {
		t.Errorf("unthrottled progress: %q", got)
	}
}

func TestQuietConsoleReporterSuppressesRoutineOutput(t *testing.T) {
	var out bytes.Buffer
	reporter := &consoleReporter{out: &out, quiet: true}

	reporter.Initialization(reel.InitializationSummary{InputFile: "input.mkv"})
	reporter.CropResult(reel.CropSummary{Message: "no crop"})
	reporter.EncodingConfig(reel.EncodingConfigSummary{Encoder: "svt-av1"})
	reporter.EncodingProgress(reel.ProgressSnapshot{Percent: 50})
	reporter.ValidationComplete(reel.ValidationSummary{Passed: true})

	if out.Len() != 0 {
		t.Fatalf("quiet routine output = %q, want empty", out.String())
	}
}

func TestQuietConsoleReporterPreservesDiagnostics(t *testing.T) {
	var out bytes.Buffer
	reporter := &consoleReporter{out: &out, quiet: true}

	reporter.Error(reel.ReporterError{Title: "encode", Message: "failed", Suggestion: "retry"})
	reporter.ValidationComplete(reel.ValidationSummary{
		Steps: []reel.ReporterValidationStep{{Name: "duration", Details: "mismatch"}},
	})

	got := out.String()
	for _, want := range []string{"encode: failed", "suggestion: retry", "duration (mismatch)"} {
		if !strings.Contains(got, want) {
			t.Errorf("quiet diagnostic output does not contain %q: %q", want, got)
		}
	}
}
