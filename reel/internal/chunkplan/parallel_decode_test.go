package chunkplan

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/reel/internal/video"
)

func TestParallelShotScoresMatchSequentialAtSegmentBoundary(t *testing.T) {
	const frames = 3000 // two workers need at least 1500 frames each
	path := filepath.Join(t.TempDir(), "shots.y4m")
	var data bytes.Buffer
	data.WriteString("YUV4MPEG2 W16 H16 F25:1 Ip A1:1 C420\n")
	for i := range frames {
		data.WriteString("FRAME\n")
		value := byte(30)
		if i >= frames/2 {
			value = 210
		}
		data.Write(bytes.Repeat([]byte{value}, 16*16*3/2))
	}
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := video.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	var progress int
	workers, parallel, err := scoreVideo(context.Background(), path, info, Options{ShotDetectWorkers: 2, Progress: func(done, total int) {
		if done == total {
			progress++
		}
	}})
	if err != nil || workers != 2 || progress == 0 {
		t.Fatalf("parallel workers=%d final progress=%d err=%v", workers, progress, err)
	}
	_, sequential, err := scoreVideo(context.Background(), path, info, Options{ShotDetectWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(parallel) != frames || len(sequential) != frames {
		t.Fatalf("frame counts: %d vs %d", len(parallel), len(sequential))
	}
	for i, score := range parallel {
		if score != sequential[i] {
			t.Fatalf("score at frame %d: parallel %g, sequential %g", i, score, sequential[i])
		}
	}
	if parallel[frames/2] <= 0 {
		t.Fatal("boundary shot change not detected")
	}
}
