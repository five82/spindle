package processing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/reel/internal/video"
)

func TestDetectCropSamplesSkipsUnreadableFrames(t *testing.T) {
	const width, height = 32, 32
	path := filepath.Join(t.TempDir(), "bars.y4m")
	data := []byte("YUV4MPEG2 W32 H32 F24:1 Ip A1:1 C420\n")
	for range 3 {
		frame := make([]byte, width*height*3/2)
		for y := 4; y < height-4; y++ {
			for x := 2; x < width-2; x++ {
				frame[y*width+x] = 100
			}
		}
		data = append(data, "FRAME\n"...)
		data = append(data, frame...)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := video.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	crops := detectCropSamples(path, info, []int{0, 1, 50, 2}, 2)
	if len(crops) != 3 {
		t.Fatalf("valid crops = %d, want 3: %+v", len(crops), crops)
	}
	for _, crop := range crops {
		if crop != (detectedCrop{Top: 4, Bottom: 4, Left: 2, Right: 2}) {
			t.Fatalf("detected crop = %+v", crop)
		}
	}
	if got := detectCropSamples(path, info, []int{0}, 0); got != nil {
		t.Fatalf("zero workers: %+v", got)
	}
}
