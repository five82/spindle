//go:build cgo && !no_vship

package quality

import (
	"testing"

	"github.com/five82/spindle/reel/internal/video"
)

func TestVshipColorspaceMappings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    int32
		want    int
		mapCode func(*video.Info) int
	}{
		{"matrix RGB", 0, 0, func(i *video.Info) int { return int(vshipMatrix(i)) }},
		{"matrix 470", 5, 5, func(i *video.Info) int { return int(vshipMatrix(i)) }},
		{"matrix 601", 6, 6, func(i *video.Info) int { return int(vshipMatrix(i)) }},
		{"matrix 2020 NCL", 9, 9, func(i *video.Info) int { return int(vshipMatrix(i)) }},
		{"matrix 2020 CL", 10, 10, func(i *video.Info) int { return int(vshipMatrix(i)) }},
		{"matrix ICTCP", 14, 14, func(i *video.Info) int { return int(vshipMatrix(i)) }},
		{"matrix unknown", 99, 1, func(i *video.Info) int { return int(vshipMatrix(i)) }},
		{"transfer 470M", 4, 4, func(i *video.Info) int { return int(vshipTransfer(i)) }},
		{"transfer 470BG", 5, 5, func(i *video.Info) int { return int(vshipTransfer(i)) }},
		{"transfer 601", 6, 6, func(i *video.Info) int { return int(vshipTransfer(i)) }},
		{"transfer linear", 8, 8, func(i *video.Info) int { return int(vshipTransfer(i)) }},
		{"transfer sRGB", 13, 13, func(i *video.Info) int { return int(vshipTransfer(i)) }},
		{"transfer PQ", 16, 16, func(i *video.Info) int { return int(vshipTransfer(i)) }},
		{"transfer ST428", 17, 17, func(i *video.Info) int { return int(vshipTransfer(i)) }},
		{"transfer HLG", 18, 18, func(i *video.Info) int { return int(vshipTransfer(i)) }},
		{"transfer unknown", 99, 1, func(i *video.Info) int { return int(vshipTransfer(i)) }},
		{"primaries internal", -1, -1, func(i *video.Info) int { return int(vshipPrimaries(i)) }},
		{"primaries 470M", 4, 4, func(i *video.Info) int { return int(vshipPrimaries(i)) }},
		{"primaries 470BG", 5, 5, func(i *video.Info) int { return int(vshipPrimaries(i)) }},
		{"primaries 2020", 9, 9, func(i *video.Info) int { return int(vshipPrimaries(i)) }},
		{"primaries unknown", 99, 1, func(i *video.Info) int { return int(vshipPrimaries(i)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := &video.Info{}
			switch {
			case len(tc.name) >= 6 && tc.name[:6] == "matrix":
				i.MatrixCoefficients = &tc.code
			case len(tc.name) >= 8 && tc.name[:8] == "transfer":
				i.TransferCharacteristics = &tc.code
			default:
				i.ColorPrimaries = &tc.code
			}
			if got := tc.mapCode(i); got != tc.want {
				t.Fatalf("mapping %d = %d, want %d", tc.code, got, tc.want)
			}
		})
	}
	if int(vshipMatrix(nil)) != 1 || int(vshipTransfer(nil)) != 1 || int(vshipPrimaries(nil)) != 1 {
		t.Fatal("nil colorspace must default to BT.709")
	}
	pos, full := int32(2), int32(2)
	info := &video.Info{ChromaSamplePosition: &pos, ColorRange: &full}
	if int(vshipChromaLocation(info)) != 2 || int(vshipRange(info)) != 1 {
		t.Fatal("top-left/full mapping")
	}
	if int(vshipChromaLocation(nil)) != 0 || int(vshipRange(nil)) != 0 {
		t.Fatal("left/limited defaults")
	}
	cs := createYUVColorspace(1920, 1080, info)
	if int(cs.width) != 1920 || int(cs.height) != 1080 || int(cs.sample) != 5 || int(cs.range_) != 1 {
		t.Fatal("unexpected YUV420P10 colorspace")
	}
	if !VshipBuildEnabled() {
		t.Fatal("VSHIP build disabled")
	}
}

func TestClosedVshipProcessor(t *testing.T) {
	var nilProcessor *VshipProcessor
	if err := nilProcessor.Close(); err != nil {
		t.Fatal(err)
	}
	if err := nilProcessor.ResetCVVDP(); err == nil {
		t.Fatal("expected closed handler error")
	}
	if _, err := nilProcessor.ComputeCVVDP(FramePlanes{}, FramePlanes{}); err == nil {
		t.Fatal("expected closed handler error")
	}
	closed := &VshipProcessor{closed: true}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := closed.ResetCVVDP(); err == nil {
		t.Fatal("expected closed handler error")
	}
	if _, err := closed.ComputeCVVDP(FramePlanes{}, FramePlanes{}); err == nil {
		t.Fatal("expected closed handler error")
	}
}
