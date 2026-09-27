package encode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/config"
	"github.com/five82/spindle/reel/internal/quality"
	"github.com/five82/spindle/reel/internal/video"
)

func TestGrainGateMeasuresSampleCostsWithoutEncoder(t *testing.T) {
	info := &video.Info{Width: config.HDWidthThreshold, Height: 1080, Frames: 200, FPSNum: 25, FPSDen: 1}
	in := GrainGateInput{WorkDir: t.TempDir(), Info: info, Chunks: []chunk.Chunk{{Idx: 7, Start: 0, End: 100}, {Idx: 8, Start: 100, End: 200}}}
	var verbose []string
	in.Verbose = func(s string) { verbose = append(verbose, s) }
	cfg := &EncodeConfig{CRF: 40, Denoise: "hflip"}
	measured := 0
	measure := func(_ context.Context, c *EncodeConfig, _ GrainGateInput, ch chunk.Chunk, path string, w, h uint32) (float64, error) {
		measured++
		if c.CRF != grainGateCRF || c.Denoise != "" || cfg.CRF != 40 || w != 1920 || h != 1080 || filepath.Base(path) != fmt.Sprintf("%04d.ivf", ch.Idx) {
			t.Errorf("measurement: cfg %+v, chunk %+v, path %s, dimensions %dx%d", c, ch, path, w, h)
		}
		return 0.02, nil
	}
	stats, err := runGrainGateWithMeasure(context.Background(), cfg, in, measure)
	if err != nil || stats.Treated || stats.GateStage != grainStageBPP || len(stats.SampleBPP) != 2 || measured != 2 || len(verbose) != 2 {
		t.Fatalf("gate: %+v, measured %d, log %v, err %v", stats, measured, verbose, err)
	}
	if _, err := os.Stat(filepath.Join(in.WorkDir, "gate")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary samples not cleaned up: %v", err)
	}
	sentinel := errors.New("encode failed")
	_, err = runGrainGateWithMeasure(context.Background(), cfg, in, func(context.Context, *EncodeConfig, GrainGateInput, chunk.Chunk, string, uint32, uint32) (float64, error) {
		return 0, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("sample failure: %v", err)
	}
}

func TestGrainGateAmbiguousBPPFallsBackWhenNoScorer(t *testing.T) {
	info := &video.Info{Width: config.HDWidthThreshold, Height: 1080, Frames: 100}
	in := GrainGateInput{WorkDir: t.TempDir(), Info: info, Chunks: []chunk.Chunk{{Start: 0, End: 100}}}
	stats, err := runGrainGateWithMeasure(context.Background(), &EncodeConfig{}, in, func(context.Context, *EncodeConfig, GrainGateInput, chunk.Chunk, string, uint32, uint32) (float64, error) {
		return (hdAmbiguousBPP + hdLightBPP) / 2, nil
	})
	if err != nil || stats.Stage2Error == "" || stats.Treated || stats.GateStage != grainStageBPP {
		t.Fatalf("stage 2 fallback: %+v, %v", stats, err)
	}
}

type grainLadderScorer struct {
	scores []float32
	err    error
	calls  int
}

func (s *grainLadderScorer) ScoreChunk(_ context.Context, _ quality.ChunkScoreRequest) (float32, float64, error) {
	s.calls++
	if s.err != nil {
		return 0, 0, s.err
	}
	return s.scores[min(s.calls-1, len(s.scores)-1)], 0, nil
}
func (*grainLadderScorer) Close() error { return nil }

func TestGrainStage2ProbeLadderWithSyntheticCostsAndScores(t *testing.T) {
	in := GrainGateInput{InputPath: "source", WorkDir: t.TempDir(), Info: uhdInfo()}
	ch := chunk.Chunk{Idx: 2, End: 100}
	sentinel := errors.New("cost failed")
	for _, tc := range []struct {
		name       string
		scores     []float32
		err        error
		costErr    error
		wantProbes int
		wantErr    string
	}{
		{name: "in band", scores: []float32{9.5}, wantProbes: 1},
		{name: "second rung", scores: []float32{10, 9.5}, wantProbes: 2},
		{name: "scoring failed", err: errors.New("scoring failed"), wantErr: "scoring failed"},
		{name: "encode failed", costErr: sentinel, wantErr: "cost failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scorer := &grainLadderScorer{scores: tc.scores, err: tc.err}
			measured := 0
			bpp, probes, err := measureTargetDeliveredBPPWithMeasure(context.Background(), &EncodeConfig{}, in, ch, in.WorkDir, 16, 16, 9.5, 0.1, scorer,
				func(_ context.Context, c *EncodeConfig, _ GrainGateInput, _ chunk.Chunk, path string, _, _ uint32) (float64, error) {
					measured++
					if c.CRF < quality.DefaultSearchMin || c.CRF > quality.DefaultSearchMax || filepath.Dir(path) != in.WorkDir {
						t.Errorf("invalid probe: %s CRF %v", path, c.CRF)
					}
					return 0.2, tc.costErr
				})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %v, want %s", err, tc.wantErr)
				}
			} else if err != nil || bpp != 0.2 || probes != tc.wantProbes {
				t.Fatalf("ladder: bpp %g probes %d: %v", bpp, probes, err)
			}
			if scorer.calls != probes && tc.costErr == nil && tc.err == nil {
				t.Fatalf("scorer calls %d, probes %d", scorer.calls, probes)
			}
			if measured == 0 {
				t.Fatal("no probe measured")
			}
		})
	}
}
