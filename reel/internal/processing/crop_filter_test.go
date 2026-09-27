package processing

import (
	"testing"

	"github.com/five82/reel/internal/media"
	"github.com/five82/reel/internal/video"
)

func TestParseCropFilterExactRectangle(t *testing.T) {
	crop, err := parseCropFilter("crop=1900:1060:4:8", 1920, 1080)
	if err != nil {
		t.Fatalf("parseCropFilter returned error: %v", err)
	}
	if crop.X != 4 || crop.Y != 8 || crop.Width != 1900 || crop.Height != 1060 {
		t.Fatalf("unexpected crop rectangle: %+v", crop)
	}
}

func TestParseCropFilterRejectsOutOfBounds(t *testing.T) {
	if _, err := parseCropFilter("crop=1900:1060:40:8", 1920, 1080); err == nil {
		t.Fatal("expected out-of-bounds crop to be rejected")
	}
}

func TestParseCropFilterRejectsOddYUV420Geometry(t *testing.T) {
	if _, err := parseCropFilter("crop=1901:1060:4:8", 1920, 1080); err == nil {
		t.Fatal("expected odd crop width to be rejected")
	}
}

func TestDisplayAspectAfterCrop(t *testing.T) {
	props := &media.VideoProperties{
		Width:                720,
		Height:               480,
		SampleAspectRatioNum: 32,
		SampleAspectRatioDen: 27,
	}
	crop := &video.CropRect{X: 0, Y: 60, Width: 720, Height: 360}

	got := displayAspectAfterCrop(props, nil, crop)
	if got != "64:27" {
		t.Fatalf("displayAspectAfterCrop() = %q, want %q", got, "64:27")
	}
}

func TestDisplayAspectAfterCropIgnoresSquarePixels(t *testing.T) {
	props := &media.VideoProperties{
		Width:                1920,
		Height:               1080,
		SampleAspectRatioNum: 1,
		SampleAspectRatioDen: 1,
	}
	if got := displayAspectAfterCrop(props, nil, nil); got != "" {
		t.Fatalf("displayAspectAfterCrop() = %q, want empty string", got)
	}
}

func TestDisplayAspectAfterCropFallsBackToVideoInfo(t *testing.T) {
	inf := &video.Info{
		Width:                720,
		Height:               480,
		SampleAspectRatioNum: 32,
		SampleAspectRatioDen: 27,
	}
	crop := &video.CropRect{X: 0, Y: 60, Width: 720, Height: 360}

	got := displayAspectAfterCrop(nil, inf, crop)
	if got != "64:27" {
		t.Fatalf("displayAspectAfterCrop() = %q, want %q", got, "64:27")
	}
}

func TestExpectedDisplayAspectAfterCrop(t *testing.T) {
	props := &media.VideoProperties{
		SampleAspectRatioNum: 32,
		SampleAspectRatioDen: 27,
	}

	got := expectedDisplayAspectAfterCrop(props, 720, 360)
	if got == nil || *got != [2]uint32{64, 27} {
		t.Fatalf("expectedDisplayAspectAfterCrop() = %v, want 64:27", got)
	}
}
