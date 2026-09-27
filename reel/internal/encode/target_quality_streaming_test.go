package encode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/quality"
	"github.com/five82/spindle/reel/internal/video"
)

type syntheticScorer struct {
	t     *testing.T
	err   error
	calls int
}

func (s *syntheticScorer) ScoreChunk(_ context.Context, req quality.ChunkScoreRequest) (float32, float64, error) {
	s.calls++
	if _, err := os.Stat(req.ProbePath); err != nil {
		s.t.Errorf("probe not on disk: %v", err)
	}
	if req.Chunk.Frames() != 3 || req.Width != 32 || req.Height != 32 {
		s.t.Errorf("request = %+v", req)
	}
	return 9.5, 0, s.err
}
func (*syntheticScorer) Close() error { return nil }

func TestTargetQualityChunkSearchWithSyntheticScorer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.y4m")
	data := []byte("YUV4MPEG2 W32 H32 F25:1 Ip A1:1 C420\n")
	for i := 0; i < 3; i++ {
		data = append(data, []byte("FRAME\n")...)
		data = append(data, make([]byte, 32*32*3/2)...)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	inf, err := video.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	ch := chunk.Chunk{Idx: 0, Start: 0, End: 3}
	for _, tc := range []struct {
		name     string
		scoreErr error
		source   string
		want     string
	}{
		{name: "accepted probe", source: path},
		{name: "metric failure", source: path, scoreErr: errors.New("metric unavailable"), want: "metric unavailable"},
		{name: "missing source", source: path + ".missing", want: "open source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := filepath.Join(dir, tc.name)
			if err := os.MkdirAll(filepath.Join(work, "probes"), 0700); err != nil {
				t.Fatal(err)
			}
			limiter := newAdaptiveLimiter(1, 1, 1, 3, nil, nil)
			r := newTargetQualityRun(TargetQualityConfig{Metric: quality.MetricCVVDP, Target: 9.5, Tolerance: 0.1, CRFMin: 10, CRFMax: 50, MaxProbes: 1, InitialCRF: 30}, &EncodeConfig{CRF: 30, Preset: 12}, tc.source, work, inf, nil, 32, 32, limiter, 1, nil, nil)
			scorer := &syntheticScorer{t: t, err: tc.scoreErr}
			pool := make(chan quality.ChunkScorer, 1)
			pool <- scorer
			ctx := context.Background()
			if _, err := limiter.acquire(ctx); err != nil {
				t.Fatal(err)
			}
			res := r.encodeChunk(ctx, ch, chunkSearchPlan{searchCtx: r.searchCtx, pool: pool}, "initial")
			limiter.release()
			if tc.want != "" {
				if res.Error == nil || !strings.Contains(res.Error.Error(), tc.want) {
					t.Fatalf("error = %v", res.Error)
				}
				return
			}
			if res.Error != nil || res.Log.FinalCRF != 30 || res.Log.FinalScore != 9.5 || len(res.Log.Probes) != 1 || res.Frames != 3 {
				t.Fatalf("result = %+v", res)
			}
			if _, err := os.Stat(chunk.IVFPath(work, 0)); err != nil {
				t.Fatalf("final IVF: %v", err)
			}
			if scorer.calls != 1 {
				t.Fatalf("scored %d times", scorer.calls)
			}
		})
	}
}
