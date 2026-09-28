package encode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/config"
	"github.com/five82/spindle/reel/internal/perf"
)

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
