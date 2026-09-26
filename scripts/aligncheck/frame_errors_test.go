package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/video"
)

func TestGroundTruthAndFreshReadRejectTruncatedClip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "one-frame.y4m")
	frame := make([]byte, 4*4*3/2)
	data := append([]byte("YUV4MPEG2 W4 H4 F25:1 Ip A1:1 C420\nFRAME\n"), frame...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	oldCrop := cropRect
	cropRect = nil
	t.Cleanup(func() { cropRect = oldCrop })
	info, err := video.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if sums, err := groundTruth(path, info, 1); err != nil || len(sums) != 1 {
		t.Fatalf("one valid frame: %v, %v", sums, err)
	}
	if sums, err := groundTruth(path, info, 2); err == nil || len(sums) != 0 || !strings.Contains(err.Error(), "ground-truth frame 1") {
		t.Fatalf("past EOF ground truth: %v, %v", sums, err)
	}
	if _, err := readFresh(path, info, 1); err == nil {
		t.Fatal("past EOF fresh read must fail")
	}
}
