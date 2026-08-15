package encoder

import (
	"bytes"
	"strings"
	"testing"

	"github.com/five82/reel"
)

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

	reporter.Warning("fallback used")
	reporter.Error(reel.ReporterError{Title: "encode", Message: "failed", Suggestion: "retry"})
	reporter.ValidationComplete(reel.ValidationSummary{
		Steps: []reel.ReporterValidationStep{{Name: "duration", Details: "mismatch"}},
	})

	got := out.String()
	for _, want := range []string{"fallback used", "encode: failed", "suggestion: retry", "duration (mismatch)"} {
		if !strings.Contains(got, want) {
			t.Errorf("quiet diagnostic output does not contain %q: %q", want, got)
		}
	}
}
