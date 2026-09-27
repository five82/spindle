package encode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/config"
	"github.com/five82/spindle/reel/internal/perf"
	"github.com/five82/spindle/reel/internal/video"
)

func TestGrainGateWithoutSampleAndWithUnavailableSource(t *testing.T) {
	dir := t.TempDir()
	cfg := &EncodeConfig{}
	in := GrainGateInput{WorkDir: dir, Info: &video.Info{Width: config.HDWidthThreshold, Height: 16, Frames: 100, FPSNum: 25, FPSDen: 1}}
	stats, err := runGrainGate(context.Background(), cfg, in)
	if err != nil || len(stats.SampleChunks) != 0 || stats.Reason == "" {
		t.Fatalf("no sample = %+v, %v", stats, err)
	}
	in.Chunks = []chunk.Chunk{{Idx: 0, Start: 0, End: 100}}
	if _, err := runGrainGate(context.Background(), cfg, in); err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("missing source: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gate")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("gate measurements not cleaned up: %v", err)
	}
	in.Info.Width = 640
	stats, err = runGrainGate(context.Background(), cfg, in)
	if err != nil || stats.ResolutionClass != "sd" || stats.Treated {
		t.Fatalf("SD gate = %+v, %v", stats, err)
	}
}

func TestGrainDecisionFailureAndRecordedObservation(t *testing.T) {
	cfg := &EncodeConfig{}
	in := GrainGateInput{WorkDir: t.TempDir(), Info: uhdInfo()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ResolveGrainTreatment(ctx, config.GrainTreatmentAuto, cfg, in); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled gate: %v", err)
	}
	if got, err := RecordedGrainTreatment(config.GrainTreatmentAuto, cfg, in); err != nil || got.Stats != nil {
		t.Fatalf("unrecorded gate: %+v, %v", got, err)
	}
	// The observation path must not materialize the model; the gate path must.
	v := saveTestGrainVerdict(t, in.WorkDir, &perf.GrainTreatmentStats{Mode: config.GrainTreatmentAuto, Treated: true, Denoise: grainDenoiseFilter})
	got, err := RecordedGrainTreatment(config.GrainTreatmentAuto, cfg, in)
	if err != nil || got.Stats == nil || !got.Stats.Reused || got.TablePath != "" || got.Denoise != v.Denoise {
		t.Fatalf("recorded treatment = %+v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(in.WorkDir, "grain-estimated.tbl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("observation wrote model: %v", err)
	}
}

func TestGrainStage2ValidationAndMeasuredFallbacks(t *testing.T) {
	in := GrainGateInput{Info: uhdInfo()}
	cfg := &EncodeConfig{}
	for _, tc := range []struct {
		name string
		in   GrainGateInput
	}{
		{"no model", in},
		{"no quality band", GrainGateInput{Info: uhdInfo(), DisplayPath: "unused"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := newGrainStage2Measure(cfg, tc.in, t.TempDir(), 16, 16); err == nil {
				t.Fatal("invalid stage 2 setup succeeded")
			}
		})
	}
	stats := &perf.GrainTreatmentStats{LightBPPCutoff: 0.2}
	applyGrainStage2(context.Background(), in, stats, nil, func(context.Context, chunk.Chunk) (float64, int, error) {
		t.Fatal("called without samples")
		return 0, 0, nil
	})
	if stats.Stage2Error == "" || stats.Treated {
		t.Fatalf("empty stage 2 = %+v", stats)
	}
}
