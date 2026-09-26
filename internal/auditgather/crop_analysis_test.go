package auditgather

import (
	"testing"

	"github.com/five82/spindle/internal/encodingstate"
)

func TestAnalyzeCropFromEncodingSnapshot(t *testing.T) {
	for _, tc := range []struct {
		filter   string
		required bool
		width    int
		ratio    float64
	}{
		{"crop=1920:800:0:140", true, 1920, 2.4},
		{"not-a-crop", false, 0, 0},
	} {
		got := analyzeCrop(&encodingstate.Snapshot{CropFilter: tc.filter, CropRequired: tc.required})
		if got.Filter != tc.filter || got.Required != tc.required || got.OutputWidth != tc.width || got.AspectRatio != tc.ratio {
			t.Fatalf("analyzeCrop(%q) = %+v", tc.filter, got)
		}
	}
}
