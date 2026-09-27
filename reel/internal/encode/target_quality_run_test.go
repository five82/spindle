package encode

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/five82/reel/internal/quality"
	"github.com/five82/reel/internal/video"
)

func testVideoInfo() *video.Info {
	return &video.Info{Width: 1920, Height: 1080, FPSNum: 24000, FPSDen: 1001}
}

// newSSIMU2TestRun builds a run in SSIMU2 mode without scorer pools; the
// stand-in pool channels only need identity, since chunkPlan routes pools but
// never scores.
func newSSIMU2TestRun(t *testing.T, verbose func(string)) *targetQualityRun {
	t.Helper()
	tq := TargetQualityConfig{
		Metric:        quality.MetricSSIMU2,
		Target:        80,
		Tolerance:     2,
		CRFMin:        10,
		CRFMax:        50,
		MaxProbes:     4,
		MetricWorkers: 2,
		InitialCRF:    30,
		Verbose:       verbose,
	}
	limiter := newAdaptiveLimiter(4, 2, 4, 0, nil, nil)
	r := newTargetQualityRun(tq, &EncodeConfig{}, "input.mkv", t.TempDir(), testVideoInfo(), nil, 1920, 1080, limiter, 3, nil, nil)
	r.metricPool = make(chan quality.ChunkScorer, tq.MetricWorkers)
	r.warmupPool = make(chan quality.ChunkScorer, tq.MetricWorkers)
	return r
}

func lockCalibration(c *ssimu2Calibration, offset float32) {
	for i := 0; i < ssimu2CalibrationMinSamples; i++ {
		c.AddSample(9, quality.SSIMU2FromJOD(9)+offset)
	}
}

func exhaustWarmupClaims(c *ssimu2Calibration) {
	for c.ClaimWarmup() {
	}
}

func nearlyEqual(a, b float32) bool {
	diff := a - b
	return diff < 0.01 && diff > -0.01
}

func TestWithSlotReleasedRestoresSlot(t *testing.T) {
	r := &targetQualityRun{limiter: newAdaptiveLimiter(2, 1, 2, 0, nil, nil)}
	ctx := context.Background()
	if _, err := r.limiter.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	during := -1
	if err := r.withSlotReleased(ctx, func() error {
		during, _, _ = r.limiter.stats()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if during != 0 {
		t.Fatalf("slot still held while fn ran: active=%d", during)
	}
	if active, _, _ := r.limiter.stats(); active != 1 {
		t.Fatalf("slot not re-acquired: active=%d", active)
	}
}

func TestFlightCapPrimesThenGrowsWithPriors(t *testing.T) {
	r := &targetQualityRun{
		tq:               TargetQualityConfig{MetricWorkers: 2},
		limiter:          newAdaptiveLimiter(16, 4, 16, 0, nil, nil),
		prior:            newTargetQualityPrior(30, 10, 50, 9.5, 0.025, quality.MetricCVVDP),
		primeConcurrency: 3,
	}
	// Limiter target is 4, so the post-prime ceiling is 4 + max(4, 2) = 8.
	steps := []struct {
		completed int
		want      int
	}{
		{completed: 0, want: 3}, // prime floor until enough priors exist
		{completed: 3, want: 3},
		{completed: 4, want: 3}, // growth starts counting past the prime count
		{completed: 5, want: 6},
		{completed: 6, want: 8}, // 3 + 3*2 = 9 clamps to the ceiling
		{completed: 10, want: 8},
	}
	done := 0
	for _, step := range steps {
		for ; done < step.completed; done++ {
			r.prior.AddResult(done, 30, nil)
		}
		if got := r.flightCap(); got != step.want {
			t.Fatalf("flightCap with %d completed chunks = %d, want %d", step.completed, got, step.want)
		}
	}
}

func TestChunkPlanCVVDPUsesBaseSearch(t *testing.T) {
	tq := TargetQualityConfig{
		Metric:        quality.MetricCVVDP,
		Target:        9.5,
		Tolerance:     0.1,
		CRFMin:        10,
		CRFMax:        50,
		MaxProbes:     4,
		MetricWorkers: 2,
		InitialCRF:    30,
	}
	limiter := newAdaptiveLimiter(4, 2, 4, 0, nil, nil)
	r := newTargetQualityRun(tq, &EncodeConfig{}, "input.mkv", t.TempDir(), testVideoInfo(), nil, 1920, 1080, limiter, 3, nil, nil)
	r.metricPool = make(chan quality.ChunkScorer, tq.MetricWorkers)

	plan, err := r.chunkPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.isWarmup {
		t.Fatal("CVVDP run must not produce warmup chunks")
	}
	if plan.searchCtx.Metric != quality.MetricCVVDP || !nearlyEqual(plan.searchCtx.Target, 9.5) {
		t.Fatalf("unexpected search context: metric=%s target=%v", plan.searchCtx.Metric, plan.searchCtx.Target)
	}
	if plan.pool != r.metricPool || plan.dualPool != nil {
		t.Fatal("CVVDP run must score with the metric pool and never dual-score")
	}
}

func TestChunkPlanClaimsWarmupWhileCalibrating(t *testing.T) {
	r := newSSIMU2TestRun(t, nil)

	plan, err := r.chunkPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.isWarmup {
		t.Fatal("expected a warmup chunk while the offset is unlocked")
	}
	if plan.searchCtx.Metric != quality.MetricCVVDP || !nearlyEqual(plan.searchCtx.Target, quality.JODAnchorTarget) {
		t.Fatalf("warmup chunk must search CVVDP at the JOD anchor, got metric=%s target=%v", plan.searchCtx.Metric, plan.searchCtx.Target)
	}
	if plan.pool != r.warmupPool || plan.dualPool != r.metricPool {
		t.Fatal("warmup chunk must probe with the warmup pool and dual-score with the metric pool")
	}
	if got := r.warmupOutstanding.Load(); got != 1 {
		t.Fatalf("warmupOutstanding = %d, want 1", got)
	}
}

func TestChunkPlanUsesOffsetCorrectedTargetOnceLocked(t *testing.T) {
	lockLines := 0
	r := newSSIMU2TestRun(t, func(msg string) {
		if strings.Contains(msg, "calibration locked") {
			lockLines++
		}
	})
	lockCalibration(r.calibration, 2)

	for i := 0; i < 2; i++ {
		plan, err := r.chunkPlan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if plan.isWarmup {
			t.Fatal("locked calibration must not start warmup chunks")
		}
		if plan.searchCtx.Metric != quality.MetricSSIMU2 || !nearlyEqual(plan.searchCtx.Target, 82) {
			t.Fatalf("expected SSIMU2 search at offset-corrected target 82, got metric=%s target=%v", plan.searchCtx.Metric, plan.searchCtx.Target)
		}
		if plan.pool != r.metricPool || plan.dualPool != nil {
			t.Fatal("post-lock chunks must score with the metric pool only")
		}
	}
	if lockLines != 1 {
		t.Fatalf("calibration lock logged %d times, want exactly once", lockLines)
	}
}

func TestChunkPlanWaitsForLockWhenWarmupExhausted(t *testing.T) {
	r := newSSIMU2TestRun(t, nil)
	exhaustWarmupClaims(r.calibration)

	ctx := context.Background()
	// chunkPlan is entered with an encode slot held.
	if _, err := r.limiter.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		lockCalibration(r.calibration, -1.5)
	}()

	plan, err := r.chunkPlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if plan.isWarmup {
		t.Fatal("chunk that waited for the lock must not be a warmup chunk")
	}
	if !nearlyEqual(plan.searchCtx.Target, 78.5) {
		t.Fatalf("expected offset-corrected target 78.5, got %v", plan.searchCtx.Target)
	}
	if active, _, _ := r.limiter.stats(); active != 1 {
		t.Fatalf("encode slot not re-acquired after the wait: active=%d", active)
	}
}

func TestChunkPlanWaitFailsOnCancel(t *testing.T) {
	r := newSSIMU2TestRun(t, nil)
	exhaustWarmupClaims(r.calibration)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := r.limiter.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	if _, err := r.chunkPlan(ctx); err == nil {
		t.Fatal("expected an error when the context is canceled during the calibration wait")
	}
}
