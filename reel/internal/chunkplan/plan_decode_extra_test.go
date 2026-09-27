package chunkplan

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/five82/reel/internal/video"
)

func planTestClip(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clip.y4m")
	data := []byte("YUV4MPEG2 W32 H32 F25:1 Ip A1:1 C420\n")
	for i := 0; i < 12; i++ {
		data = append(data, []byte("FRAME\n")...)
		for j := 0; j < 32*32*3/2; j++ {
			data = append(data, byte(32+i*4))
		}
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPlanDecodesAndCachesShortClip(t *testing.T) {
	path := planTestClip(t)
	inf, err := video.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{MaxFrames: 5, MinFrames: 2, RetainScores: true, ShotDetectWorkers: 2}
	dir := t.TempDir()
	bounds, meta := filepath.Join(dir, "plan.txt"), filepath.Join(dir, "plan.json")
	result, err := PlanToFileIfNeeded(context.Background(), path, bounds, meta, inf, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Frames != 12 || result.ShotDetectWorkersUsed < 1 || len(result.FrameScores) != 12 || len(result.Boundaries) < 2 {
		t.Fatalf("plan: %+v", result)
	}
	cached, err := PlanToFileIfNeeded(context.Background(), path, bounds, meta, inf, opts)
	if err != nil || !reflect.DeepEqual(cached.Boundaries, result.Boundaries) {
		t.Fatalf("cache: %+v %v", cached, err)
	}
	if _, err := Plan(context.Background(), "/missing.y4m", inf, opts); err == nil {
		t.Fatal("missing video should fail")
	}
	if _, err := Plan(context.Background(), path, nil, opts); err == nil {
		t.Fatal("nil info should fail")
	}
}
