package encode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/video"
	"github.com/five82/spindle/reel/internal/worker"
)

func TestEncodeAllUnavailableSource(t *testing.T) {
	dir := t.TempDir()
	chunks := []chunk.Chunk{{Idx: 0, Start: 0, End: 2}, {Idx: 1, Start: 2, End: 3}}
	info := &video.Info{Width: 32, Height: 32, FPSNum: 25, FPSDen: 1}
	var progress []worker.Progress
	workers, err := EncodeAll(context.Background(), chunks, filepath.Join(dir, "missing.y4m"), info, &EncodeConfig{CRF: 30}, dir, nil, func(p worker.Progress) { progress = append(progress, p) })
	if workers < 1 || err == nil || !strings.Contains(err.Error(), "video source") {
		t.Fatalf("workers=%d error=%v", workers, err)
	}
	for _, p := range progress {
		if p.FramesComplete > p.FramesTotal || p.ChunksComplete > p.ChunksTotal {
			t.Fatalf("invalid progress: %+v", p)
		}
	}
}

func TestEncodeAllRejectsBadCropBeforeOpeningSource(t *testing.T) {
	dir := t.TempDir()
	_, err := EncodeAll(context.Background(), []chunk.Chunk{{Idx: 0, End: 2}}, "missing.y4m", &video.Info{Width: 32, Height: 32}, &EncodeConfig{}, dir, &video.CropRect{X: 1, Width: 30, Height: 32}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid crop") {
		t.Fatalf("error = %v", err)
	}
}

func TestEncodeAllEmptyAndWorkDirError(t *testing.T) {
	dir := t.TempDir()
	workers, err := EncodeAll(context.Background(), nil, "missing", nil, nil, dir, nil, nil)
	if err != nil || workers < 1 {
		t.Fatalf("empty encode: %d, %v", workers, err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = EncodeAll(context.Background(), nil, "missing", nil, nil, file, nil, nil)
	if err == nil {
		t.Fatal("expected work directory error")
	}
}

func TestEncodeTargetQualityEarlyExits(t *testing.T) {
	dir := t.TempDir()
	workers, stats, err := EncodeTargetQuality(context.Background(), nil, "missing", nil, nil, dir, nil, nil, TargetQualityConfig{})
	if err != nil || stats != nil || workers < 1 {
		t.Fatalf("empty target run: %d, %+v, %v", workers, stats, err)
	}
	_, _, err = EncodeTargetQuality(context.Background(), []chunk.Chunk{{Idx: 0, End: 2}}, "missing", &video.Info{Width: 32, Height: 32}, nil, t.TempDir(), &video.CropRect{X: 1, Width: 30, Height: 32}, nil, TargetQualityConfig{})
	if err == nil || !strings.Contains(err.Error(), "invalid crop") {
		t.Fatalf("crop error = %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err = EncodeTargetQuality(context.Background(), nil, "missing", nil, nil, file, nil, nil, TargetQualityConfig{})
	if err == nil {
		t.Fatal("expected directory error")
	}
}
