package auditgather

import (
	"strings"
	"testing"

	"github.com/five82/spindle/internal/encodingstate"
)

func TestDigestEncodingReportsFailureAndMeasurements(t *testing.T) {
	report := &Report{StageGate: StageGate{MediaType: "tv"}, Encoding: &EncodingReport{Snapshot: encodingstate.Snapshot{
		Encoder: "av1", Quality: "target", Resolution: "1920x1080", DynamicRange: "SDR",
		CropFilter: "crop=1920:800", CropRequired: true, CropMessage: "letterbox",
		OriginalSize: 1024 * 1024 * 1024, EncodedSize: 512 * 1024 * 1024, SizeReductionPercent: 50,
		EncodeDurationSeconds: 120, AverageSpeed: 2.5, Warning: "some warning",
		Error:      &encodingstate.Issue{Title: "encode", Message: "failed", Suggestion: "retry"},
		Validation: &encodingstate.Validation{Steps: []encodingstate.ValidationStep{{Name: "video", Passed: true}, {Name: "audio", Details: "missing"}}},
	}}}
	var b strings.Builder
	writeDigestEncoding(&b, report)
	for _, want := range []string{"last episode encoded", "Input: 1920x1080 SDR", "Crop: crop=1920:800 required=true", "letterbox", "Size:", "encode time", "avg speed 2.50x", "WARNING: some warning", "suggestion: retry", "Validation: FAILED", "video: pass", "audio: FAIL missing"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("digest missing %q: %s", want, b.String())
		}
	}
	report.Encoding.Snapshot.Validation = &encodingstate.Validation{Passed: true, Steps: []encodingstate.ValidationStep{{Name: "video", Passed: true}}}
	b.Reset()
	writeDigestEncoding(&b, report)
	if !strings.Contains(b.String(), "Validation: PASSED (video)") {
		t.Fatalf("passed digest: %s", b.String())
	}
}
