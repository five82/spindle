package main

import (
	"github.com/five82/reel/internal/video"
	"testing"
)

func TestFrameSizeAndMissingSource(t *testing.T) {
	old := cropRect
	t.Cleanup(func() { cropRect = old })
	inf := &video.Info{Width: 20, Height: 10}
	cropRect = &video.CropRect{Width: 10, Height: 4}
	if got := frameSize(inf); got != 120 {
		t.Fatalf("crop frame size = %d", got)
	}
	cropRect = nil
	if got := frameSize(inf); got != 600 {
		t.Fatalf("full frame size = %d", got)
	}
	if _, err := groundTruth("/does/not/exist", inf, 1); err == nil {
		t.Fatal("expected source error")
	}
	if _, err := readFresh("/does/not/exist", inf, 0); err == nil {
		t.Fatal("expected source error")
	}
}
