package encode

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/five82/spindle/reel/internal/util"
)

const (
	memoryMonitorInterval = 5 * time.Second
	rampWindowTicks       = 12 // 60 seconds of utilization samples per ramp decision
	pressureCooldownTicks = 12 // 60 seconds

	memoryCriticalAvailableFraction = 0.08
	memoryPressureAvailableFraction = 0.20
	memoryStableAvailableFraction   = 0.35

	// rampUtilizationThreshold is the mean encode-slot utilization above which
	// the slot cap is treated as the bottleneck and another worker is added.
	rampUtilizationThreshold = 0.85

	// initialMemoryFraction is the share of available memory used to bound the
	// starting worker count for memory-heavy (4K) encodes on low-RAM machines.
	initialMemoryFraction = 0.5
	// workerMemoryUHD is the rough resident memory of one concurrent 4K
	// SVT-AV1 encode (encoder internals plus frame buffers). Used only to keep
	// the 4K start within RAM on small machines; the backoff guards the rest.
	workerMemoryUHD = 3 << 30

	// uhdCoreDivisor sets the 4K concurrency ceiling at ~1 active encode per
	// this many logical cores. Measured finding (2026-06-12): 4K SVT-AV1
	// encodes are memory-bandwidth bound, not RAM-capacity bound. Past roughly
	// one encode per 6 logical cores, per-encode throughput drops faster than
	// concurrency rises and total wall time regresses, even with tens of GiB of
	// RAM free. So 4K is capped here rather than ramped toward CPU/RAM limits.
	uhdCoreDivisor = 6

	swapStableGrowthBytes          = 16 << 20
	swapRampTotalGrowthFloorBytes  = 512 << 20
	swapRampTotalGrowthDenominator = 100 // 1% of configured swap
	swapPressureGrowthBytes        = 64 << 20
	swapCriticalGrowthBytes        = 1 << 30
)

// ErrMemoryPressure is returned when Reel cancels encoding to avoid system OOM.
var ErrMemoryPressure = errors.New("memory pressure critical; canceled before swap exhaustion")

type statusCallback func(string)

type adaptiveLimiter struct {
	mu          sync.Mutex
	cond        *sync.Cond
	min         int
	max         int
	rampCeiling int
	target      int
	active      int

	// slotWaitNanos accumulates wall time workers spend blocked in acquire
	// waiting for a free encode slot, summed across all workers. Read lock-free
	// for performance attribution (perf.json); never gates encoding.
	slotWaitNanos atomic.Int64

	status statusCallback
	// warn receives degraded-behavior limiter messages (worker reductions and
	// the critical cancel) so they can reach the consumer unconditionally,
	// independent of verbose mode. See statusCallback field for the
	// verbose-only ramp-up path.
	warn statusCallback

	observedFrames int
	totalFrames    int

	// Utilization samples accumulate across a ramp window; the up-ramp fires
	// when slots stay saturated and memory is stable.
	utilSum       float64
	utilTicks     int
	cooldownTicks int
}

// MaxAdaptiveWorkers returns the hardware-derived adaptive concurrency ceiling.
func MaxAdaptiveWorkers() int {
	return max(util.LogicalCores(), 1)
}

func newAdaptiveLimiter(maxWorkers, initialWorkers, rampCeiling, totalFrames int, status, warn statusCallback) *adaptiveLimiter {
	maxWorkers = max(maxWorkers, 1)
	initialWorkers = min(max(initialWorkers, 1), maxWorkers)
	if rampCeiling <= 0 || rampCeiling > maxWorkers {
		rampCeiling = maxWorkers
	}
	rampCeiling = max(rampCeiling, initialWorkers)

	l := &adaptiveLimiter{
		min:         1,
		max:         maxWorkers,
		rampCeiling: rampCeiling,
		target:      initialWorkers,
		totalFrames: max(totalFrames, 0),
		status:      status,
		warn:        warn,
	}
	l.cond = sync.NewCond(&l.mu)
	return l
}

// resolutionRampCeiling caps how high the adaptive ramp may raise the worker
// target for a given resolution. 4K is bandwidth-bound (see uhdCoreDivisor);
// lower resolutions are GPU-metric bound and self-limit via utilization, so
// they may ramp to the full hardware ceiling.
func resolutionRampCeiling(maxWorkers int, width, height uint32) int {
	if width >= 3840 || height >= 2160 {
		return min(maxWorkers, max(3, maxWorkers/uhdCoreDivisor))
	}
	return maxWorkers
}

// resolutionWorkerFloor is the proven-safe starting worker count by resolution.
// It is also the prime-phase concurrency used while the CRF prior is built.
func resolutionWorkerFloor(maxWorkers int, width, height uint32) int {
	if maxWorkers <= 1 {
		return 1
	}
	switch {
	case width >= 3840 || height >= 2160:
		return min(maxWorkers, 3)
	case width >= 1920 || height >= 1080:
		return min(maxWorkers, max(3, maxWorkers/4))
	default:
		return min(maxWorkers, 4)
	}
}

// initialAdaptiveWorkers picks the starting worker count. HD/SD encodes are
// GPU-metric bound, so they start at the resolution floor and let the
// utilization ramp extend them as the GPU keeps up. 4K encodes are
// memory-bandwidth bound at a low, roughly fixed concurrency, and the old
// throughput ramp was too slow and noisy to climb to it within an encode, so
// they start directly at the bandwidth-aware ceiling (bounded by RAM on small
// machines). availableBytes of 0 (no memory reading) keeps the full ceiling.
func initialAdaptiveWorkers(maxWorkers int, width, height uint32, availableBytes uint64) int {
	if maxWorkers <= 1 {
		return 1
	}
	floor := resolutionWorkerFloor(maxWorkers, width, height)
	if width < 3840 && height < 2160 {
		return floor
	}
	target := resolutionRampCeiling(maxWorkers, width, height)
	if availableBytes > 0 {
		memWorkers := int(uint64(float64(availableBytes)*initialMemoryFraction) / workerMemoryUHD)
		if memWorkers < target {
			target = memWorkers
		}
	}
	if target < floor {
		target = floor
	}
	return min(target, maxWorkers)
}

// levelOfParallelismForWorkers picks SVT-AV1's level_of_parallelism from the
// resolution-aware worker target (the ramp ceiling), not the hardware core
// count. lp is verified bitstream-identical across values
// (TestLevelOfParallelismBitstreamIdentical), so it is purely a throughput
// knob: when few encoders run concurrently each should use more internal
// threads, and when many run each should use fewer. Driving it off the raw
// hardware max pins lp=2 on big machines even for 4K, where the bandwidth cap
// holds concurrency near maxWorkers/uhdCoreDivisor and the extra cores would
// otherwise sit idle while early probes run only 2-4 encoders.
func levelOfParallelismForWorkers(workers int) uint32 {
	switch {
	case workers <= 2:
		return 4
	case workers <= 5:
		return 3
	default:
		return 2
	}
}

// resolveLevelOfParallelism applies the lp auto-selection policy in one place so
// the encode and target-quality paths cannot drift: an explicit value (non-zero)
// is honored, otherwise lp is derived from the resolution-aware worker target.
func resolveLevelOfParallelism(current uint32, rampCeiling int) uint32 {
	if current != 0 {
		return current
	}
	return levelOfParallelismForWorkers(rampCeiling)
}

func (l *adaptiveLimiter) acquire(ctx context.Context) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var waitStart time.Time
	for l.active >= l.target {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if waitStart.IsZero() {
			waitStart = time.Now()
		}
		l.cond.Wait()
	}
	if !waitStart.IsZero() {
		l.slotWaitNanos.Add(int64(time.Since(waitStart)))
	}
	l.active++
	return l.active, nil
}

// slotWaitSeconds returns the cumulative encode-slot wait time across all
// workers. Safe to call concurrently with encoding.
func (l *adaptiveLimiter) slotWaitSeconds() float64 {
	return float64(l.slotWaitNanos.Load()) / float64(time.Second)
}

func (l *adaptiveLimiter) release() {
	l.mu.Lock()
	if l.active > 0 {
		l.active--
	}
	l.cond.Broadcast()
	l.mu.Unlock()
}

func (l *adaptiveLimiter) wake() {
	l.mu.Lock()
	l.cond.Broadcast()
	l.mu.Unlock()
}

func (l *adaptiveLimiter) stats() (active, target, maxWorkers int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active, l.target, l.max
}

func (l *adaptiveLimiter) observeProgress(frames int) {
	l.mu.Lock()
	if frames > l.observedFrames {
		l.observedFrames = frames
	}
	l.mu.Unlock()
}

func (l *adaptiveLimiter) rampDisabledLate() bool {
	return l.totalFrames > 0 && float64(l.observedFrames)/float64(l.totalFrames) >= 0.80
}

// recordUtilization samples current slot utilization (active/target) for the
// ramp decision. Sampled once per monitor tick.
func (l *adaptiveLimiter) recordUtilization() {
	l.mu.Lock()
	if l.target > 0 {
		l.utilSum += float64(l.active) / float64(l.target)
		l.utilTicks++
	}
	l.mu.Unlock()
}

func (l *adaptiveLimiter) monitor(ctx context.Context, cancel context.CancelFunc, setError func(error)) {
	stats, ok := util.ReadMemoryStats()
	if !ok || stats.MemTotal == 0 {
		return
	}

	baselineSwapUsed := stats.SwapUsed()
	lastSwapUsed := baselineSwapUsed
	ticker := time.NewTicker(memoryMonitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			l.wake()
			return
		case <-ticker.C:
		}

		l.recordUtilization()

		stats, ok := util.ReadMemoryStats()
		if !ok || stats.MemTotal == 0 {
			continue
		}
		lastSwapUsed = l.monitorSample(stats, baselineSwapUsed, lastSwapUsed, cancel, setError)
		if ctx.Err() != nil {
			return
		}
	}
}

// monitorSample keeps pressure decisions independent of the clock and /proc so
// they can be tested without changing the real monitor's sampling interval.
func (l *adaptiveLimiter) monitorSample(stats util.MemoryStats, baselineSwapUsed, lastSwapUsed uint64, cancel context.CancelFunc, setError func(error)) uint64 {
	availableFraction := float64(stats.MemAvailable) / float64(stats.MemTotal)
	currentSwapUsed := stats.SwapUsed()
	swapGrowthTotal := positiveDelta(currentSwapUsed, baselineSwapUsed)
	swapGrowthInterval := positiveDelta(currentSwapUsed, lastSwapUsed)

	if critical, reason := l.criticalPressure(availableFraction, swapGrowthTotal, stats); critical {
		detail := fmt.Sprintf("%s, available %.0f%%, swap +%s", reason, availableFraction*100, util.FormatBytesReadable(swapGrowthTotal))
		setError(fmt.Errorf("%w: %s", ErrMemoryPressure, detail))
		l.warnf("Memory pressure is critical (%s); canceling encode before swap exhaustion", detail)
		cancel()
		l.wake()
		return currentSwapUsed
	}

	if l.hasPressure(availableFraction) {
		l.reduceTarget(availableFraction, swapGrowthInterval)
		return currentSwapUsed
	}

	swapGrowthStable := swapGrowthStableForRamp(stats, swapGrowthTotal, swapGrowthInterval)
	memoryStable := availableFraction > memoryStableAvailableFraction && swapGrowthStable
	l.maybeRampUp(memoryStable)
	return currentSwapUsed
}

func positiveDelta(current, previous uint64) uint64 {
	if current <= previous {
		return 0
	}
	return current - previous
}

func swapGrowthStableForRamp(stats util.MemoryStats, totalGrowth, intervalGrowth uint64) bool {
	return totalGrowth <= swapRampTotalGrowthLimit(stats) && intervalGrowth <= swapStableGrowthBytes
}

func swapRampTotalGrowthLimit(stats util.MemoryStats) uint64 {
	limit := uint64(swapRampTotalGrowthFloorBytes)
	if stats.SwapTotal == 0 {
		return limit
	}
	return max(limit, stats.SwapTotal/swapRampTotalGrowthDenominator)
}

// criticalPressure reports whether encoding must stop immediately to avoid a
// genuine OOM/thrash spiral, and if so, names the signal that fired.
//
// Invariant: swap growth alone is never memory pressure -- not for the
// critical cancel here and not for worker reductions (hasPressure). Linux
// swaps out cold anonymous pages under heavy page-cache churn (e.g. another
// process streaming tens of GB of files through the page cache) even while
// tens of GiB of MemAvailable remain -- that is the kernel rebalancing
// memory, not a machine heading for OOM. A genuine spiral pairs swap growth
// with low MemAvailable. So the swap-growth triggers below only fire once
// availableFraction has already dropped into the pressure band
// (memoryPressureAvailableFraction); low available memory alone is always
// critical regardless of swap.
func (l *adaptiveLimiter) criticalPressure(availableFraction float64, swapGrowth uint64, stats util.MemoryStats) (bool, string) {
	if availableFraction < memoryCriticalAvailableFraction {
		return true, "available memory critically low"
	}
	if availableFraction >= memoryPressureAvailableFraction {
		return false, ""
	}
	if swapGrowth >= swapCriticalGrowthBytes {
		return true, "swap growth under low available memory"
	}
	// Swap is a performance cliff, not normal operating headroom. If Reel has
	// already consumed a meaningful fraction of swap during this encode while
	// available memory is also low, stop before the machine spends minutes
	// thrashing or reaches OOM.
	if stats.SwapTotal > 0 && swapGrowth > stats.SwapTotal/20 {
		return true, "swap growth under low available memory"
	}
	return false, ""
}

// hasPressure follows the criticalPressure invariant: only low available
// memory is pressure. Swap growth at high availability is cache-churn noise
// (it throttled a healthy solo 4K encode to 1 worker for hours with 59-75%
// available); within the band it escalates the reduction step instead.
func (l *adaptiveLimiter) hasPressure(availableFraction float64) bool {
	return availableFraction < memoryPressureAvailableFraction
}

func (l *adaptiveLimiter) reduceTarget(availableFraction float64, swapGrowth uint64) {
	l.mu.Lock()
	old := l.target
	step := max(old/3, 1)
	if swapGrowth >= swapPressureGrowthBytes {
		step = max(step, old/2)
	}
	l.target = max(l.min, old-step)
	l.utilSum = 0
	l.utilTicks = 0
	l.cooldownTicks = pressureCooldownTicks
	active := l.active
	newTarget := l.target
	l.cond.Broadcast()
	l.mu.Unlock()

	if newTarget < old {
		l.warnf("Memory pressure detected; reducing workers %d -> %d (active %d, available %.0f%%, swap +%s)",
			old, newTarget, active, availableFraction*100, util.FormatBytesReadable(swapGrowth))
	}
}

// maybeRampUp adds a worker when encode slots have stayed saturated across a
// full window and memory is stable. Utilization (active/target) is a smooth,
// direct signal that the slot cap is the bottleneck; unlike throughput it does
// not jump only on chunk completion, so it stays meaningful at low 4K chunk
// rates. When slots are not saturated the bottleneck is elsewhere (GPU
// scoring, dispatch, or the duty cycle) and adding slots would not help.
func (l *adaptiveLimiter) maybeRampUp(memoryStable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.cooldownTicks > 0 {
		l.cooldownTicks--
		return
	}
	if l.utilTicks < rampWindowTicks {
		return
	}
	utilization := l.utilSum / float64(l.utilTicks)
	l.utilSum = 0
	l.utilTicks = 0

	if !memoryStable {
		return
	}
	if l.target >= l.rampCeiling || l.rampDisabledLate() {
		return
	}
	if utilization < rampUtilizationThreshold {
		return
	}

	old := l.target
	step := max(1, l.target/4)
	l.target = min(l.rampCeiling, old+step)
	l.cond.Broadcast()
	l.statusf("Encode slots saturated (%.0f%% utilization, memory stable); raising workers %d -> %d", utilization*100, old, l.target)
}

func (l *adaptiveLimiter) statusf(format string, args ...any) {
	if l.status != nil {
		l.status(fmt.Sprintf(format, args...))
	}
}

// warnf reports degraded-behavior limiter decisions (worker reductions and
// the critical cancel). Unlike statusf, this is meant to reach the consumer
// unconditionally rather than only in verbose mode.
func (l *adaptiveLimiter) warnf(format string, args ...any) {
	if l.warn != nil {
		l.warn(fmt.Sprintf(format, args...))
	}
}
