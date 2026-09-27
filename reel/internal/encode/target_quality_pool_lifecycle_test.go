package encode

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/reel/internal/quality"
	"github.com/five82/spindle/reel/internal/video"
)

func TestWarmupPoolFailureClosesPrimaryPool(t *testing.T) {
	failure := errors.New("warmup unavailable")
	closed := 0
	r := newSSIMU2TestRun(t, nil)
	r.tq.MetricWorkers = 1
	r.tq.scorerFactory = func(kind quality.MetricKind, _, _ uint32, _ *video.Info, _ string) (quality.ChunkScorer, error) {
		if kind == quality.MetricCVVDP {
			return nil, failure
		}
		return &closeTrackingScorer{closed: &closed}, nil
	}
	if err := r.openScorerPools(); !errors.Is(err, failure) || r.metricPool != nil || closed != 1 {
		t.Fatalf("warmup error %v, pool %v, closed %d", err, r.metricPool, closed)
	}
}

func TestResumedSSIMU2RunDoesNotOpenWarmupPool(t *testing.T) {
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "tq"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "tq", "ssimu2-calibration.json"), []byte(`{"offset":2,"samples":20}`), 0600); err != nil {
		t.Fatal(err)
	}
	created, closed := 0, 0
	tq := TargetQualityConfig{Metric: quality.MetricSSIMU2, MetricWorkers: 1, InitialCRF: 30, CRFMin: 10, CRFMax: 50, Target: 80, scorerFactory: func(kind quality.MetricKind, _, _ uint32, _ *video.Info, _ string) (quality.ChunkScorer, error) {
		if kind != quality.MetricSSIMU2 {
			t.Fatalf("unexpected warmup metric %s", kind)
		}
		created++
		return &closeTrackingScorer{closed: &closed}, nil
	}}
	r := newTargetQualityRun(tq, &EncodeConfig{}, "unused", work, testVideoInfo(), nil, 1920, 1080, newAdaptiveLimiter(1, 1, 1, 0, nil, nil), 1, nil, nil)
	if err := r.openScorerPools(); err != nil {
		t.Fatal(err)
	}
	r.closeScorerPools()
	if created != 1 || closed != 1 || r.warmupPool != nil {
		t.Fatalf("resume pools: created %d closed %d warmup %v", created, closed, r.warmupPool)
	}
}
