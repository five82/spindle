package encode

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/five82/reel/internal/util"
)

func TestInitialAdaptiveWorkers(t *testing.T) {
	tests := []struct {
		name       string
		maxWorkers int
		width      uint32
		height     uint32
		available  uint64
		want       int
	}{
		{
			name:       "single worker",
			maxWorkers: 1,
			width:      1920,
			height:     1080,
			want:       1,
		},
		{
			name:       "4k without memory reading starts at bandwidth ceiling",
			maxWorkers: 32,
			width:      3840,
			height:     2160,
			available:  0, // ceiling 32/6 = 5
			want:       5,
		},
		{
			name:       "4k with abundant memory starts at bandwidth ceiling",
			maxWorkers: 32,
			width:      3840,
			height:     2160,
			available:  60 << 30, // mem allows 10, ceiling 5 wins
			want:       5,
		},
		{
			name:       "4k memory-limited below ceiling",
			maxWorkers: 32,
			width:      3840,
			height:     2160,
			available:  18 << 30, // 0.5*18/3 = 3 < ceiling 5
			want:       3,
		},
		{
			name:       "4k never starts below floor",
			maxWorkers: 32,
			width:      3840,
			height:     2160,
			available:  8 << 30, // mem allows 1, floor 3 wins
			want:       3,
		},
		{
			name:       "4k small machine ceiling and floor coincide",
			maxWorkers: 8,
			width:      3840,
			height:     2160,
			available:  120 << 30, // ceiling max(3, 8/6)=3
			want:       3,
		},
		{
			name:       "hd ignores memory and uses quarter of large machines",
			maxWorkers: 32,
			width:      1920,
			height:     1080,
			available:  60 << 30,
			want:       8,
		},
		{
			name:       "hd keeps small machine floor",
			maxWorkers: 8,
			width:      1920,
			height:     1080,
			want:       3,
		},
		{
			name:       "hd caps to max workers",
			maxWorkers: 2,
			width:      1920,
			height:     1080,
			want:       2,
		},
		{
			name:       "sd starts at four",
			maxWorkers: 32,
			width:      1280,
			height:     720,
			want:       4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := initialAdaptiveWorkers(tt.maxWorkers, tt.width, tt.height, tt.available)
			if got != tt.want {
				t.Fatalf("initialAdaptiveWorkers() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLevelOfParallelismForWorkers(t *testing.T) {
	tests := []struct {
		name    string
		workers int
		want    uint32
	}{
		{"one worker", 1, 4},
		{"two workers", 2, 4},
		{"three workers", 3, 3},
		{"five workers", 5, 3},
		{"six workers", 6, 2},
		{"hardware max", 32, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := levelOfParallelismForWorkers(tt.workers); got != tt.want {
				t.Errorf("levelOfParallelismForWorkers(%d) = %d, want %d", tt.workers, got, tt.want)
			}
		})
	}
}

// TestLevelOfParallelismFromRampCeiling pins the intended scaling: lp is driven
// off the resolution-aware worker target, so 4K (bandwidth-capped near
// maxWorkers/uhdCoreDivisor) gets a higher lp than non-4K (which ramps to the
// full hardware ceiling) on the same machine.
func TestLevelOfParallelismFromRampCeiling(t *testing.T) {
	tests := []struct {
		name       string
		maxWorkers int
		width      uint32
		height     uint32
		want       uint32
	}{
		{"4k big machine", 32, 3840, 2160, 3},    // ceiling max(3,32/6)=5 -> lp 3
		{"1080p big machine", 32, 1920, 1080, 2}, // ceiling 32 -> lp 2
		{"4k small machine", 8, 3840, 2160, 3},   // ceiling max(3,8/6)=3 -> lp 3
		{"4k huge machine", 64, 3840, 2160, 2},   // ceiling max(3,64/6)=10 -> lp 2
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ceiling := resolutionRampCeiling(tt.maxWorkers, tt.width, tt.height)
			if got := levelOfParallelismForWorkers(ceiling); got != tt.want {
				t.Errorf("lp for %dx%d on %d cores (ceiling %d) = %d, want %d",
					tt.width, tt.height, tt.maxWorkers, ceiling, got, tt.want)
			}
		})
	}
}

func TestResolveLevelOfParallelism(t *testing.T) {
	// Explicit (non-zero) value is honored regardless of the ceiling.
	if got := resolveLevelOfParallelism(5, 32); got != 5 {
		t.Errorf("explicit lp not honored: got %d, want 5", got)
	}
	// 0 derives from the worker target (ceiling 5 -> lp 3).
	if got := resolveLevelOfParallelism(0, 5); got != 3 {
		t.Errorf("auto lp from ceiling 5: got %d, want 3", got)
	}
}

func TestShouldReopenSource(t *testing.T) {
	tests := []struct {
		name       string
		nextFrame  int
		chunkStart int
		want       bool
	}{
		{
			name:       "keeps source for forward chunks",
			nextFrame:  1000,
			chunkStart: 1200,
			want:       false,
		},
		{
			name:       "keeps source for contiguous chunks",
			nextFrame:  1000,
			chunkStart: 1000,
			want:       false,
		},
		{
			name:       "reopens before backward chunks",
			nextFrame:  12759,
			chunkStart: 5498,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldReopenSource(tt.nextFrame, tt.chunkStart)
			if got != tt.want {
				t.Fatalf("shouldReopenSource() = %t, want %t", got, tt.want)
			}
		})
	}
}

// sampleUtilization feeds n monitor ticks at the given active level, then runs
// one ramp decision, mirroring the monitor loop (recordUtilization per tick,
// maybeRampUp once a window has accumulated).
func sampleUtilization(l *adaptiveLimiter, active, ticks int, memoryStable bool) {
	for range ticks {
		l.active = active
		l.recordUtilization()
		l.maybeRampUp(memoryStable)
	}
}

func TestAdaptiveLimiterAccumulatesSlotWait(t *testing.T) {
	limiter := newAdaptiveLimiter(4, 1, 4, 0, nil, nil)
	ctx := context.Background()

	// The only slot is taken without waiting, so no time is charged.
	if _, err := limiter.acquire(ctx); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if got := limiter.slotWaitSeconds(); got != 0 {
		t.Fatalf("slot wait after unblocked acquire = %v, want 0", got)
	}

	// A second acquire must block until the held slot is released. Whatever the
	// scheduler does, that second acquire cannot return until release() runs, so
	// the charged wait is strictly positive.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = limiter.acquire(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	limiter.release()
	wg.Wait()

	if got := limiter.slotWaitSeconds(); got <= 0 {
		t.Fatalf("slot wait after blocked acquire = %v, want > 0", got)
	}
}

func TestAdaptiveLimiterRampsUpWhenSlotsSaturated(t *testing.T) {
	limiter := newAdaptiveLimiter(8, 2, 8, 0, nil, nil)

	// Slots fully utilized for a full window: expect a ramp.
	sampleUtilization(limiter, 2, rampWindowTicks, true)
	_, target, _ := limiter.stats()
	if target != 3 {
		t.Fatalf("target after saturated window = %d, want 3", target)
	}
}

func TestAdaptiveLimiterDoesNotRampWhenSlotsIdle(t *testing.T) {
	limiter := newAdaptiveLimiter(8, 4, 8, 0, nil, nil)

	// Only half the slots used: utilization 0.5 < threshold, no ramp.
	sampleUtilization(limiter, 2, rampWindowTicks*3, true)
	_, target, _ := limiter.stats()
	if target != 4 {
		t.Fatalf("target with idle slots = %d, want 4 (no ramp)", target)
	}
}

func TestAdaptiveLimiterDoesNotRampUnderMemoryPressure(t *testing.T) {
	limiter := newAdaptiveLimiter(8, 4, 8, 0, nil, nil)

	// Saturated slots but memory not stable: no ramp.
	sampleUtilization(limiter, 4, rampWindowTicks*2, false)
	_, target, _ := limiter.stats()
	if target != 4 {
		t.Fatalf("target with unstable memory = %d, want 4 (no ramp)", target)
	}
}

func TestAdaptiveLimiterRampStepGrowsWithTarget(t *testing.T) {
	limiter := newAdaptiveLimiter(64, 8, 64, 0, nil, nil)

	// At target 8, step is max(1, 8/4) = 2 -> 10.
	sampleUtilization(limiter, 8, rampWindowTicks, true)
	_, target, _ := limiter.stats()
	if target != 10 {
		t.Fatalf("target after one saturated window = %d, want 10", target)
	}
}

func TestAdaptiveLimiterHoldsCooldownAfterPressure(t *testing.T) {
	limiter := newAdaptiveLimiter(8, 6, 8, 0, nil, nil)
	limiter.reduceTarget(0.15, swapPressureGrowthBytes)
	reduced := func() int { _, target, _ := limiter.stats(); return target }()
	if reduced >= 6 {
		t.Fatalf("reduceTarget did not lower target: %d", reduced)
	}

	// During the cooldown, saturated slots must not immediately ramp back up.
	sampleUtilization(limiter, reduced, pressureCooldownTicks-1, true)
	if got := func() int { _, target, _ := limiter.stats(); return target }(); got != reduced {
		t.Fatalf("target ramped during cooldown = %d, want %d", got, reduced)
	}
}

// TestCriticalPressure pins the invariant that swap growth alone never stops
// an encode: Linux swaps out cold anonymous pages under heavy page-cache
// churn from unrelated processes even while tens of GiB of MemAvailable
// remain free, which is harmless kernel rebalancing, not a genuine
// OOM/thrash spiral. A real spiral pairs swap growth with low available
// memory, so both swap-growth triggers (the flat swapCriticalGrowthBytes
// threshold and the SwapTotal/20 threshold) only fire once availableFraction
// has dropped below memoryPressureAvailableFraction. Low available memory
// alone is always critical regardless of swap.
func TestCriticalPressure(t *testing.T) {
	l := &adaptiveLimiter{}

	t.Run("flat threshold gated by available memory", func(t *testing.T) {
		stats := util.MemoryStats{SwapTotal: 64 << 30} // 64 GiB: SwapTotal/20 = 3.2 GiB, above the 2 GiB growth used here.
		swapGrowth := uint64(2 << 30)                  // Above swapCriticalGrowthBytes (1 GiB).

		if critical, reason := l.criticalPressure(0.40, swapGrowth, stats); critical {
			t.Fatalf("2 GiB swap growth with 40%% available should not be critical, got reason %q", reason)
		}
		critical, reason := l.criticalPressure(0.15, swapGrowth, stats)
		if !critical {
			t.Fatal("2 GiB swap growth with 15% available should be critical")
		}
		if reason == "" {
			t.Fatal("expected a non-empty reason when critical")
		}
	})

	t.Run("low available memory alone is critical", func(t *testing.T) {
		stats := util.MemoryStats{SwapTotal: 64 << 30}
		critical, reason := l.criticalPressure(0.05, 0, stats)
		if !critical {
			t.Fatal("5% available with zero swap growth should be critical")
		}
		if reason == "" {
			t.Fatal("expected a non-empty reason when critical")
		}
	})

	t.Run("SwapTotal/20 threshold gated by available memory", func(t *testing.T) {
		stats := util.MemoryStats{SwapTotal: 8 << 30} // SwapTotal/20 = 429.5 MiB, well under swapCriticalGrowthBytes.
		swapGrowth := stats.SwapTotal/20 + (10 << 20) // Just over the SwapTotal/20 floor, far under the flat 1 GiB threshold.

		if critical, reason := l.criticalPressure(0.40, swapGrowth, stats); critical {
			t.Fatalf("SwapTotal/20 growth with 40%% available should not be critical, got reason %q", reason)
		}
		if critical, _ := l.criticalPressure(0.15, swapGrowth, stats); !critical {
			t.Fatal("SwapTotal/20 growth with 15% available should be critical")
		}
	})

	t.Run("no pressure", func(t *testing.T) {
		stats := util.MemoryStats{SwapTotal: 64 << 30}
		if critical, reason := l.criticalPressure(0.90, 0, stats); critical {
			t.Fatalf("no pressure should not be critical, got reason %q", reason)
		}
	})
}

// TestHasPressure pins the same invariant for worker reductions: swap growth
// with high available memory is cache-churn noise, never pressure. Before
// this gate, a healthy solo 4K encode was throttled to 1 worker for hours by
// swap trickle at 59-75% available.
func TestHasPressure(t *testing.T) {
	l := &adaptiveLimiter{}

	if l.hasPressure(0.75) {
		t.Fatal("75% available should not be pressure regardless of swap")
	}
	if l.hasPressure(memoryPressureAvailableFraction) {
		t.Fatal("exactly at the band boundary should not be pressure")
	}
	if !l.hasPressure(0.15) {
		t.Fatal("15% available should be pressure")
	}
}

func TestSwapGrowthStableForRamp(t *testing.T) {
	stats := util.MemoryStats{SwapTotal: 64 << 30}

	if !swapGrowthStableForRamp(stats, 150<<20, 1<<20) {
		t.Fatal("expected small swap growth on a large swap device to allow ramping")
	}

	if swapGrowthStableForRamp(stats, 150<<20, swapStableGrowthBytes+1) {
		t.Fatal("expected active swap growth to block ramping")
	}

	if swapGrowthStableForRamp(stats, swapRampTotalGrowthLimit(stats)+1, 0) {
		t.Fatal("expected excessive total swap growth to block ramping")
	}
}

func TestAdaptiveLimiterDoesNotRampLateInEncode(t *testing.T) {
	limiter := newAdaptiveLimiter(4, 2, 4, 100, nil, nil)
	limiter.observeProgress(80) // 80% complete -> late-encode ramp guard

	sampleUtilization(limiter, 2, rampWindowTicks*2, true)

	_, target, _ := limiter.stats()
	if target != 2 {
		t.Fatalf("target after late saturated window = %d, want 2", target)
	}
}
