package encode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/quality"
	"github.com/five82/reel/internal/video"
)

func TestTargetQualityRejectsUnknownMetricBeforeLaunchingWorkers(t *testing.T) {
	info := &video.Info{Width: 32, Height: 32, FPSNum: 25, FPSDen: 1}
	cfg := &EncodeConfig{CRF: 30}
	chunks := []chunk.Chunk{{Idx: 0, Start: 0, End: 2}}
	dir := t.TempDir()
	_, _, err := EncodeTargetQuality(context.Background(), chunks, "missing.y4m", info, cfg, dir, nil, nil, TargetQualityConfig{Metric: "unknown", MetricWorkers: 2, CRFMin: 10, CRFMax: 50})
	if err == nil || !strings.Contains(err.Error(), "unknown probe metric") {
		t.Fatalf("scorer error = %v", err)
	}
	for _, sub := range []string{"encode", "probes", "tq"} {
		if _, err := os.Stat(filepath.Join(dir, sub)); err != nil {
			t.Fatalf("missing %s: %v", sub, err)
		}
	}
}

func TestGrainStage2SetupWithoutGPU(t *testing.T) {
	cfg := &EncodeConfig{}
	for _, tc := range []struct {
		name  string
		input GrainGateInput
		want  string
	}{
		{"no display", GrainGateInput{}, "no display model"},
		{"no target", GrainGateInput{DisplayPath: "display.json"}, "no target-quality band"},
		{"no tolerance", GrainGateInput{DisplayPath: "display.json", BandCenterJOD: 9, BandTopJOD: 9}, "no target-quality band"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			measure, closeFn, err := newGrainStage2Measure(cfg, tc.input, t.TempDir(), 16, 16)
			if err == nil || !strings.Contains(err.Error(), tc.want) || measure != nil || closeFn != nil {
				t.Fatalf("unexpected setup result: %v", err)
			}
		})
	}
	if _, err := measureChunkBPP(context.Background(), cfg, GrainGateInput{InputPath: "missing.y4m"}, chunk.Chunk{}, filepath.Join(t.TempDir(), "probe.ivf"), 16, 16); err == nil || !strings.Contains(err.Error(), "open source") {
		t.Fatalf("sample: %v", err)
	}
	if pool, err := newScorerPool(quality.MetricKind("unknown"), 2, 16, 16, &video.Info{}, ""); err == nil || pool != nil {
		t.Fatalf("pool: %v %v", pool, err)
	}
	closeScorerPool(nil)
}
