package video

import (
	"strings"
	"testing"
)

func TestFrameIndexAndSeekRejectInvalidTiming(t *testing.T) {
	path := writeTestY4M(t, 16, 16, 3)
	src, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	frame, err := src.readRawFrame(0)
	if err != nil {
		t.Fatal(err)
	}
	savedMul, savedStart, savedDiv := src.tsMul, src.startTime, src.tsDiv
	src.nextFrame = 7
	src.tsMul = 0
	if got := src.frameIndex(frame); got != 7 {
		t.Fatalf("frame without timebase = %d, want sequential index 7", got)
	}
	src.tsMul = savedMul
	src.startTime = savedStart + 1000
	if got := src.frameIndex(frame); got != 7 {
		t.Fatalf("negative timestamp = %d, want sequential index 7", got)
	}
	src.startTime = savedStart
	src.tsDiv = 0
	if err := src.seekNear(1); err == nil || !strings.Contains(err.Error(), "invalid stream time base") {
		t.Fatalf("invalid timebase seek: %v", err)
	}
	src.tsDiv = savedDiv
}
