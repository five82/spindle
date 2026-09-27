package validation

import (
	"strings"
	"testing"
)

func TestResultReportsFailedChecksAndBitDepth(t *testing.T) {
	r := &Result{IsAV1: true, Is10Bit: true, IsCropCorrect: true, IsDurationCorrect: true, IsHDRCorrect: true, IsAudioOpus: true, IsAudioTrackCountCorrect: true, IsSyncPreserved: true, IsAspectCorrect: true, CodecName: "av1", BitDepth: ptr(uint8(10)), PixelFormat: "yuv420p10le"}
	if !r.IsValid() || len(r.GetFailures()) != 0 {
		t.Fatalf("valid result: %+v", r)
	}
	if got := r.GetValidationSteps()[1].Details; got != "10-bit (yuv420p10le)" {
		t.Fatalf("bit depth: %q", got)
	}
	aspect := [2]uint32{16, 9}
	r.ExpectedDisplayAspect = &aspect
	r.IsAudioTrackCountCorrect = false
	r.IsAspectCorrect = false
	r.SourceTimelineMessage = "source overrun"
	r.SourceTimelineNormalized = false
	if r.IsValid() {
		t.Fatal("failed checks marked valid")
	}
	failures := strings.Join(r.GetFailures(), "; ")
	for _, want := range []string{"Display aspect:", "Audio tracks:", "Source timeline normalization: source overrun"} {
		if !strings.Contains(failures, want) {
			t.Fatalf("failures = %q, missing %q", failures, want)
		}
	}
	for _, tc := range []struct {
		depth uint8
		want  string
	}{{8, "8-bit"}, {10, "10-bit"}, {12, "12-bit"}, {16, ""}} {
		if got := formatDepth(tc.depth); got != tc.want {
			t.Errorf("depth %d: %q", tc.depth, got)
		}
	}
	if got := formatBitDepthDetails(nil, ""); got != "Unknown bit depth" {
		t.Fatal(got)
	}
	if got := formatCodecDetails("", false); got != "Unknown codec" {
		t.Fatal(got)
	}
}
