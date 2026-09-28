package encode

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/encoder"
	"github.com/five82/spindle/reel/internal/perf"
	"github.com/five82/spindle/reel/internal/quality"
	"github.com/five82/spindle/reel/internal/video"
	"github.com/five82/spindle/reel/internal/worker"
)

const (
	// targetQualityScheduleBlockChunks keeps work close enough to timeline order
	// for neighbor priors while still starting large chunks early enough to avoid
	// a final long-tail. Smaller blocks measured slower because early priors were worse.
	targetQualityScheduleBlockChunks = 32

	// targetQualityPriorPrimeChunks is how many completed chunks must seed the
	// CRF prior before dispatch opens beyond the prime-phase concurrency.
	// Starting too many chunks cold measurably increases probes and error.
	targetQualityPriorPrimeChunks = 4

	// targetQualityFlightGrowth is how many extra in-flight chunks are allowed
	// per completed chunk after priming. Each completed chunk adds a usable
	// CRF prior, so concurrency opens up roughly in step with prior coverage.
	targetQualityFlightGrowth = 3

	// targetQualityProgressSampleInterval paces the timer-based progress
	// sampler. Matches the perf collector's unchanged-sample throttle so the
	// worker history stays bounded.
	targetQualityProgressSampleInterval = 2 * time.Second
)

type TargetQualityConfig struct {
	// Metric selects the probe metric; Target/Tolerance are denominated in
	// its units (CVVDP JOD or SSIMU2 points). Empty means CVVDP.
	Metric        quality.MetricKind
	Target        float32
	Tolerance     float32
	CRFMin        float32
	CRFMax        float32
	MaxProbes     int
	MetricWorkers int
	DisplayPath   string
	InitialCRF    float32
	// GrainTreatment is the grain gate's verdict for this title, recorded in
	// target-quality.json so a run's scores can be read next to the treatment
	// (and the honest denoise ceiling) they were measured under.
	GrainTreatment *perf.GrainTreatmentStats
	Verbose        func(string)
	// scorerFactory lets tests exercise the scheduler and encoder without GPU handlers.
	scorerFactory func(quality.MetricKind, uint32, uint32, *video.Info, string) (quality.ChunkScorer, error)
}

type targetQualityResult struct {
	worker.EncodeResult
	Log chunkTargetLog
}

// chunkSearchPlan is one chunk's search decision: which metric/target it
// searches with, which scorer pool scores its probes, and whether it is an
// SSIMU2 warmup chunk that dual-scores every probe to feed the calibration.
type chunkSearchPlan struct {
	searchCtx quality.SearchContext
	pool      chan quality.ChunkScorer
	dualPool  chan quality.ChunkScorer
	isWarmup  bool
}

// targetQualityRun is the shared state of one target-quality encode: the
// scheduler (adaptive limiter plus flight cap), the CRF prior, the optional
// SSIMU2 warmup calibration, the scorer pools, and progress/error tracking.
// Its methods are the goroutine bodies that EncodeTargetQuality wires together.
type targetQualityRun struct {
	tq        TargetQualityConfig
	cfg       *EncodeConfig
	inputPath string
	workDir   string
	inf       *video.Info
	cropRect  *video.CropRect
	width     uint32
	height    uint32

	limiter *adaptiveLimiter
	prior   *targetQualityPrior
	// calibration is non-nil only for SSIMU2 runs; see tq_calibration.go.
	calibration *ssimu2Calibration

	// primeConcurrency bounds in-flight chunks until the prior has enough
	// completed chunks to seed later searches; see flightCap.
	primeConcurrency int

	searchCtx       quality.SearchContext
	warmupSearchCtx quality.SearchContext

	metricPool chan quality.ChunkScorer
	warmupPool chan quality.ChunkScorer

	warmupOutstanding atomic.Int64
	warmupCloseOnce   sync.Once
	calibrationLocked sync.Once

	progressMu sync.Mutex
	progress   worker.Progress
	// progressCb is invoked from both the collector goroutine and the sampling
	// ticker; progressCbMu serializes calls so downstream reporters and the
	// perf collector see a single ordered stream.
	progressCb   ProgressCallback
	progressCbMu sync.Mutex

	// inFlight counts chunks currently probing/scoring/encoding. It is mutated
	// only under flightMu (so the dispatch cond stays correct) but stored
	// atomically so progress snapshots can read it lock-free.
	flightMu   sync.Mutex
	flightCond *sync.Cond
	probing    atomic.Int64
	scoring    atomic.Int64
	finishing  atomic.Int64
	inFlight   atomic.Int64

	encodeErr atomic.Pointer[error]

	logsMu sync.Mutex
	logs   []chunkTargetLog
}

func EncodeTargetQuality(
	ctx context.Context,
	chunks []chunk.Chunk,
	inputPath string,
	inf *video.Info,
	cfg *EncodeConfig,
	workDir string,
	cropRect *video.CropRect,
	progressCb ProgressCallback,
	tq TargetQualityConfig,
) (int, *perf.TargetQualityStats, error) {
	if tq.MetricWorkers < 1 {
		tq.MetricWorkers = 1
	}
	if tq.MaxProbes < 1 {
		tq.MaxProbes = 1
	}
	if tq.InitialCRF <= 0 {
		tq.InitialCRF = (tq.CRFMin + tq.CRFMax) / 2
	}
	if tq.Metric == "" {
		tq.Metric = quality.MetricCVVDP
	}
	if err := chunk.EnsureEncodeDir(workDir); err != nil {
		return 0, nil, fmt.Errorf("failed to create encode directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, "probes"), 0755); err != nil {
		return 0, nil, fmt.Errorf("failed to create probe directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, "tq"), 0755); err != nil {
		return 0, nil, fmt.Errorf("failed to create target-quality log directory: %w", err)
	}

	resume, err := chunk.GetResume(workDir)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to load resume info: %w", err)
	}
	resume = resume.Validate(workDir, chunks)
	doneSet := resume.DoneSet()

	remainingChunks := make([]chunk.Chunk, 0, len(chunks))
	totalFrames := 0
	for _, ch := range chunks {
		totalFrames += ch.Frames()
		if !doneSet[ch.Idx] {
			remainingChunks = append(remainingChunks, ch)
		}
	}
	if len(remainingChunks) == 0 {
		return MaxAdaptiveWorkers(), nil, nil
	}
	remainingChunks = orderTargetQualityChunks(remainingChunks, targetQualityScheduleBlockChunks)

	if cropRect != nil {
		if err := video.ValidateCropRect(inf, *cropRect); err != nil {
			return 0, nil, fmt.Errorf("invalid crop rectangle: %w", err)
		}
	}
	width, height := video.OutputDimensions(inf, cropRect)

	maxWorkers := MaxAdaptiveWorkers()
	initialWorkers := initialAdaptiveWorkers(maxWorkers, width, height, availableMemoryBytes())
	// Priming at the UHD slot target (5) instead of the floor (3) was tested
	// and rejected (2026-07-01): once the metric decoder stopped starving the
	// GPU the prime-phase idle it targeted mostly disappeared, so it bought no
	// wall beyond noise and cost extra cold-seeded probes (+4-5% probes/chunk)
	// and +1-3% size on two of three 4K clips. See
	// docs/PERFORMANCE_TESTING.md.
	primeConcurrency := resolutionWorkerFloor(maxWorkers, width, height)
	rampCeiling := resolutionRampCeiling(maxWorkers, width, height)
	limiter := newAdaptiveLimiter(maxWorkers, initialWorkers, rampCeiling, totalFrames, cfg.StatusCallback, cfg.WarningCallback)
	cfg.LevelOfParallelism = resolveLevelOfParallelism(cfg.LevelOfParallelism, rampCeiling)

	r := newTargetQualityRun(tq, cfg, inputPath, workDir, inf, cropRect, width, height, limiter, primeConcurrency, progressCb, doneSet)
	if err := r.openScorerPools(); err != nil {
		return 0, nil, err
	}
	defer r.closeScorerPools()

	r.progress = worker.Progress{
		ChunksTotal:    len(chunks),
		ChunksComplete: len(chunks) - len(remainingChunks),
		FramesTotal:    totalFrames,
		FramesComplete: resume.TotalEncodedFrames(),
		BytesComplete:  resume.TotalEncodedSize(),
	}
	limiter.observeProgress(r.progress.FramesComplete)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	chunkChan := make(chan chunk.Chunk, maxWorkers)
	resultChan := make(chan targetQualityResult, len(remainingChunks))

	var workerWg sync.WaitGroup
	for i := 0; i < maxWorkers; i++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			r.runWorker(ctx, chunkChan, resultChan)
		}()
	}
	go func() {
		<-ctx.Done()
		r.flightCond.Broadcast()
	}()

	var collectorWg sync.WaitGroup
	collectorWg.Add(1)
	go func() {
		defer collectorWg.Done()
		r.collect(resultChan)
	}()

	progressDone := make(chan struct{})
	go func() { defer close(progressDone); r.sampleProgress(ctx) }()
	// No callback may outlive encoding and race the caller's next phase.
	defer func() { cancel(); <-progressDone }()
	go limiter.monitor(ctx, cancel, r.setError)
	go func() {
		<-ctx.Done()
		limiter.wake()
	}()

	go r.dispatch(ctx, remainingChunks, chunkChan)

	workerWg.Wait()
	close(resultChan)
	collectorWg.Wait()
	writeAggregateTargetLog(workDir, r.logs, tq, r.calibration)
	logTargetAggregate(r.logs, tq.Verbose)
	return maxWorkers, targetQualityStats(r.logs, r.calibration), r.getError()
}

// newTargetQualityRun builds the run state minus the scorer pools, which cost
// real GPU/VRAM resources and are opened separately via openScorerPools (and
// left nil in unit tests).
func newTargetQualityRun(
	tq TargetQualityConfig,
	cfg *EncodeConfig,
	inputPath, workDir string,
	inf *video.Info,
	cropRect *video.CropRect,
	width, height uint32,
	limiter *adaptiveLimiter,
	primeConcurrency int,
	progressCb ProgressCallback,
	doneSet map[int]bool,
) *targetQualityRun {
	r := &targetQualityRun{
		tq:               tq,
		cfg:              cfg,
		inputPath:        inputPath,
		workDir:          workDir,
		inf:              inf,
		cropRect:         cropRect,
		width:            width,
		height:           height,
		limiter:          limiter,
		primeConcurrency: primeConcurrency,
		progressCb:       progressCb,
	}
	r.flightCond = sync.NewCond(&r.flightMu)

	initialJODPerCRF := tq.Metric.DefaultSlopePerCRF()
	r.searchCtx = quality.SearchContext{
		Metric:     tq.Metric,
		Target:     tq.Target,
		Tolerance:  tq.Tolerance,
		CRFMin:     tq.CRFMin,
		CRFMax:     tq.CRFMax,
		MaxProbes:  tq.MaxProbes,
		InitialCRF: tq.InitialCRF,
		JODPerCRF:  initialJODPerCRF,
		MaxRateBps: float64(encoder.MaxBitRateBps()),
		FPS:        float64(inf.FPSNum) / float64(inf.FPSDen),
	}
	r.prior = newTargetQualityPrior(tq.InitialCRF, tq.CRFMin, tq.CRFMax, tq.Target, initialJODPerCRF, tq.Metric)

	if tq.Metric == quality.MetricSSIMU2 {
		r.calibration = newSSIMU2Calibration(workDir)
	}
	seedTargetQualityPrior(workDir, doneSet, r.prior, tq.Metric, r.calibration)

	// Warmup chunks search with CVVDP at the JOD anchor band while the
	// per-title SSIMU2 offset calibrates; once it locks, later chunks search
	// with SSIMU2 against the offset-corrected target and the prior's target
	// shifts to match.
	r.warmupSearchCtx = r.searchCtx
	r.warmupSearchCtx.Metric = quality.MetricCVVDP
	r.warmupSearchCtx.Target = quality.JODAnchorTarget
	r.warmupSearchCtx.Tolerance = quality.JODAnchorTolerance
	if r.calibration != nil {
		if offset, locked := r.calibration.Offset(); locked {
			// Resumed run with a persisted offset. Burn the once quietly: the
			// lock was already logged by the run that measured it.
			r.calibrationLocked.Do(func() {})
			r.prior.SetTarget(tq.Target + offset)
		}
	}
	return r
}

// openScorerPools creates the metric scorer pool and, for SSIMU2 runs whose
// offset has not locked yet, the CVVDP warmup pool. The warmup pool is skipped
// entirely on resume with a persisted offset, and closed as soon as the last
// warmup chunk finishes after the lock -- CVVDP handlers hold ~3.5 GiB of VRAM
// that the rest of the run does not need, and freeing it is what makes
// cross-title pairing headroom real.
func (r *targetQualityRun) openScorerPools() error {
	pool, err := r.newScorerPool(r.tq.Metric)
	if err != nil {
		return err
	}
	r.metricPool = pool
	if r.calibration != nil {
		if _, locked := r.calibration.Offset(); !locked {
			r.warmupPool, err = r.newScorerPool(quality.MetricCVVDP)
			if err != nil {
				closeScorerPool(r.metricPool)
				r.metricPool = nil
				return err
			}
		}
	}
	return nil
}

func (r *targetQualityRun) closeScorerPools() {
	closeScorerPool(r.metricPool)
	// On titles too short to ever lock the calibration, all warmup scorers are
	// back in the pool by the time workers drain, so this cannot block.
	r.closeWarmupPool()
}

// closeWarmupPool releases the warmup CVVDP scorers exactly once, either when
// the last in-flight warmup chunk finishes after the offset locks (freeing the
// VRAM mid-run) or during final cleanup.
func (r *targetQualityRun) closeWarmupPool() {
	r.warmupCloseOnce.Do(func() {
		if r.warmupPool == nil {
			return
		}
		closeScorerPool(r.warmupPool)
		if r.tq.Verbose != nil {
			r.tq.Verbose("TQ warmup CVVDP scorers closed (VRAM released)")
		}
	})
}

// newScorerPool creates one VSHIP/CUDA handler PER metric worker, scored
// concurrently. Vship forbids using one handler from two threads at once (and
// CVVDP handlers carry per-handler temporal state), so each worker owns a
// distinct scorer. CORRECTNESS DEPENDS on libvship being built with
// MITIGATE_MALLOC_ASYNC: the default cudaMallocAsync allocator races across
// coexisting handlers, silently corrupts scores, trips the floor guard, and
// can cascade into huge output-size swings. Verify the linked library with
// scripts/handlertest; see docs/VSHIP_CONCURRENCY_BUG.md.
func (r *targetQualityRun) newScorerPool(kind quality.MetricKind) (chan quality.ChunkScorer, error) {
	factory := r.tq.scorerFactory
	if factory == nil {
		factory = quality.NewChunkScorer
	}
	return newScorerPoolWithFactory(kind, r.tq.MetricWorkers, r.width, r.height, r.inf, r.tq.DisplayPath, factory)
}

func newScorerPool(kind quality.MetricKind, workers int, width, height uint32, inf *video.Info, displayPath string) (chan quality.ChunkScorer, error) {
	return newScorerPoolWithFactory(kind, workers, width, height, inf, displayPath, quality.NewChunkScorer)
}

func newScorerPoolWithFactory(kind quality.MetricKind, workers int, width, height uint32, inf *video.Info, displayPath string, factory func(quality.MetricKind, uint32, uint32, *video.Info, string) (quality.ChunkScorer, error)) (chan quality.ChunkScorer, error) {
	pool := make(chan quality.ChunkScorer, workers)
	for i := 0; i < workers; i++ {
		scorer, err := factory(kind, width, height, inf, displayPath)
		if err != nil {
			for j := 0; j < i; j++ {
				_ = (<-pool).Close()
			}
			return nil, err
		}
		pool <- scorer
	}
	return pool, nil
}

func closeScorerPool(pool chan quality.ChunkScorer) {
	if pool == nil {
		return
	}
	for i := 0; i < cap(pool); i++ {
		_ = (<-pool).Close()
	}
}

func (r *targetQualityRun) setError(err error) {
	r.encodeErr.CompareAndSwap(nil, &err)
	r.flightCond.Broadcast()
}

func (r *targetQualityRun) getError() error {
	if p := r.encodeErr.Load(); p != nil {
		return *p
	}
	return nil
}

// snapshotProgressLocked returns the progress counters plus live scheduler
// stats. The caller must hold progressMu.
func (r *targetQualityRun) snapshotProgressLocked() worker.Progress {
	p := r.progress
	p.ActiveWorkers, p.TargetWorkers, p.MaxWorkers = r.limiter.stats()
	p.InFlight = int(r.inFlight.Load())
	p.Probing, p.Scoring, p.Finishing = int(r.probing.Load()), int(r.scoring.Load()), int(r.finishing.Load())
	p.CalibrationReady = true
	if r.calibration != nil {
		_, p.CalibrationReady = r.calibration.Offset()
	}
	p.EncodeSlotWaitSeconds = r.limiter.slotWaitSeconds()
	return p
}

func (r *targetQualityRun) emitProgress() {
	if r.progressCb == nil {
		return
	}
	r.progressCbMu.Lock()
	defer r.progressCbMu.Unlock()
	// Capture after serializing delivery, not before: a queued observation
	// must never regress counters already delivered by another callback.
	r.progressMu.Lock()
	p := r.snapshotProgressLocked()
	r.progressMu.Unlock()
	r.progressCb(p)
}

// flightCap bounds how many chunks may be in flight. Scoring releases encode
// slots, so slots alone no longer bound in-flight chunks. Two concerns are
// balanced here:
//   - protect the CRF prior: keep concurrency at the prime floor until a
//     few chunks complete, so early chunks are not all started cold;
//   - keep encode slots fed: a chunk spends ~half its time scoring with its
//     slot released, so in-flight must exceed the slot target (~2x) to keep
//     every slot busy across the probe/score duty cycle.
//
// After priming, the cap opens only as fast as completed chunks accumulate
// usable priors, so newly started chunks get neighbor/median seeds.
func (r *targetQualityRun) flightCap() int {
	done := r.prior.Count()
	if done < targetQualityPriorPrimeChunks {
		return r.primeConcurrency
	}
	_, target, _ := r.limiter.stats()
	ceil := target + max(target, r.tq.MetricWorkers)
	byPriors := r.primeConcurrency + targetQualityFlightGrowth*(done-targetQualityPriorPrimeChunks)
	return min(byPriors, ceil)
}

func (r *targetQualityRun) chunkDone() {
	r.flightMu.Lock()
	r.inFlight.Add(-1)
	r.flightCond.Broadcast()
	r.flightMu.Unlock()
}

// dispatch feeds chunks to workers, holding each below the flight cap and
// acquiring an encode slot before the send so a worker always starts a chunk
// with a slot held.
func (r *targetQualityRun) dispatch(ctx context.Context, chunks []chunk.Chunk, chunkChan chan<- chunk.Chunk) {
	defer close(chunkChan)
	for _, ch := range chunks {
		if r.getError() != nil {
			return
		}
		r.flightMu.Lock()
		for int(r.inFlight.Load()) >= r.flightCap() && ctx.Err() == nil && r.getError() == nil {
			r.flightCond.Wait()
		}
		r.inFlight.Add(1)
		r.flightMu.Unlock()
		if ctx.Err() != nil || r.getError() != nil {
			return
		}
		if _, err := r.limiter.acquire(ctx); err != nil {
			return
		}
		select {
		case chunkChan <- ch:
		case <-ctx.Done():
			r.limiter.release()
			return
		}
	}
}

// collect drains chunk results, updating progress, resume state, and the
// target-quality logs.
func (r *targetQualityRun) collect(resultChan <-chan targetQualityResult) {
	for result := range resultChan {
		r.chunkDone()
		if result.Error != nil {
			r.setError(result.Error)
			continue
		}
		r.progressMu.Lock()
		r.progress.ChunksComplete++
		r.progress.FramesComplete += result.Frames
		r.progress.BytesComplete += result.Size
		r.limiter.observeProgress(r.progress.FramesComplete)
		r.progressMu.Unlock()

		_ = chunk.AppendDone(chunk.ChunkComp{Idx: result.ChunkIdx, Frames: result.Frames, Size: result.Size}, r.workDir)
		r.logsMu.Lock()
		r.logs = append(r.logs, result.Log)
		r.logsMu.Unlock()
		r.emitProgress()
	}
}

// sampleProgress adds timer-based progress samples. Chunk-completion callbacks
// alone sample the scheduler at a biased instant -- the finishing worker just
// released its encode slot -- which under-reports active/in-flight in
// perf.json and leaves blind windows behind slow chunks. A coarse ticker adds
// unbiased samples between completions; the perf collector drops unchanged
// samples, so the history stays small.
func (r *targetQualityRun) sampleProgress(ctx context.Context) {
	ticker := time.NewTicker(targetQualityProgressSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.emitProgress()
		}
	}
}

// runWorker processes chunks until chunkChan closes. The dispatcher acquired
// an encode slot for every chunk it sent, and each loop iteration releases
// exactly one slot, so the one-held-slot-per-chunk invariant holds (modulo the
// teardown caveat documented on withSlotReleased).
func (r *targetQualityRun) runWorker(ctx context.Context, chunkChan <-chan chunk.Chunk, resultChan chan<- targetQualityResult) {
	for ch := range chunkChan {
		select {
		case <-ctx.Done():
			r.limiter.release()
			resultChan <- targetQualityResult{EncodeResult: worker.EncodeResult{ChunkIdx: ch.Idx, Error: ctx.Err()}}
			continue
		default:
		}
		if r.getError() != nil {
			r.limiter.release()
			continue
		}
		result := r.processChunk(ctx, ch)
		r.limiter.release()
		resultChan <- result
	}
}

// processChunk plans one chunk's search, runs it, and handles the warmup
// bookkeeping around it. Called with an encode slot held.
func (r *targetQualityRun) processChunk(ctx context.Context, ch chunk.Chunk) targetQualityResult {
	plan, err := r.chunkPlan(ctx)
	if err != nil {
		return targetQualityResult{EncodeResult: worker.EncodeResult{ChunkIdx: ch.Idx, Error: err}}
	}
	initialCRF, initialCRFSource := r.prior.InitialCRF(ch.Idx)
	plan.searchCtx.InitialCRF = initialCRF
	plan.searchCtx.JODPerCRF = r.prior.JODPerCRF() * plan.searchCtx.Metric.ScoreScale() / r.tq.Metric.ScoreScale()
	result := r.encodeChunk(ctx, ch, plan, initialCRFSource)
	if plan.isWarmup && r.warmupOutstanding.Add(-1) == 0 {
		// Last in-flight warmup chunk. New claims are impossible once the
		// offset locks, so the CVVDP pool is idle for the rest of the run --
		// release its VRAM now.
		if _, locked := r.calibration.Offset(); locked {
			r.closeWarmupPool()
		}
	}
	return result
}

// chunkPlan decides how the next chunk searches. CVVDP runs always use the
// base search context. SSIMU2 runs claim bounded warmup (CVVDP) slots while
// the per-title offset calibrates; once warmup is fully claimed, later chunks
// wait for the lock instead of paying CVVDP cost, then search with SSIMU2
// against the offset-corrected target. Called with an encode slot held; the
// slot is released while waiting for the lock -- the warmup probes need it.
func (r *targetQualityRun) chunkPlan(ctx context.Context) (chunkSearchPlan, error) {
	plan := chunkSearchPlan{searchCtx: r.searchCtx, pool: r.metricPool}
	if r.calibration == nil {
		return plan, nil
	}
	offset, locked := r.calibration.Offset()
	if !locked {
		if r.calibration.ClaimWarmup() {
			r.warmupOutstanding.Add(1)
			plan.searchCtx = r.warmupSearchCtx
			plan.pool = r.warmupPool
			plan.dualPool = r.metricPool
			plan.isWarmup = true
			return plan, nil
		}
		if err := r.withSlotReleased(ctx, func() error {
			return r.calibration.WaitLocked(ctx)
		}); err != nil {
			return chunkSearchPlan{}, err
		}
		offset, _ = r.calibration.Offset()
	}
	r.noteCalibrationLocked(offset)
	plan.searchCtx.Target = r.tq.Target + offset
	return plan, nil
}

// noteCalibrationLocked shifts the prior's target to the offset-corrected
// SSIMU2 target and logs the lock, exactly once per run. On resume with a
// persisted offset the once is burned quietly in newTargetQualityRun.
func (r *targetQualityRun) noteCalibrationLocked(offset float32) {
	r.calibrationLocked.Do(func() {
		r.prior.SetTarget(r.tq.Target + offset)
		if r.tq.Verbose != nil {
			r.tq.Verbose(fmt.Sprintf("TQ ssimu2 calibration locked: offset %+.2f -> target %.1f +/- %.1f (%d warmup probes)", offset, r.tq.Target+offset, r.tq.Tolerance, r.calibration.SampleCount()))
		}
	})
}

// withSlotReleased runs fn with the caller's encode slot released so other
// chunks can encode meanwhile (GPU scoring and calibration waits do not need a
// slot), then re-acquires it before returning. This is the single home of the
// one-held-slot-per-chunk invariant: on return the slot is held again, except
// when re-acquisition fails, which only happens once the context is canceled;
// there the caller's unconditional release harmlessly over-releases during
// teardown (release floors active at zero and nothing dispatches after
// cancellation).
func (r *targetQualityRun) withSlotReleased(ctx context.Context, fn func() error) error {
	r.limiter.release()
	err := fn()
	if _, acqErr := r.limiter.acquire(ctx); acqErr != nil && err == nil {
		err = acqErr
	}
	return err
}

func orderTargetQualityChunks(chunks []chunk.Chunk, blockSize int) []chunk.Chunk {
	ordered := append([]chunk.Chunk(nil), chunks...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Idx < ordered[j].Idx
	})
	if blockSize <= 0 {
		blockSize = len(ordered)
	}
	for start := 0; start < len(ordered); start += blockSize {
		end := min(start+blockSize, len(ordered))
		sort.SliceStable(ordered[start:end], func(i, j int) bool {
			return ordered[start+i].Frames() > ordered[start+j].Frames()
		})
	}
	return ordered
}

// encodeChunk runs one chunk's CRF search to convergence and installs the best
// probe as the final chunk.
func (r *targetQualityRun) encodeChunk(ctx context.Context, ch chunk.Chunk, plan chunkSearchPlan, initialCRFSource string) targetQualityResult {
	searchCtx := plan.searchCtx
	log := chunkTargetLog{
		ChunkIdx:         ch.Idx,
		Frames:           ch.Frames(),
		Metric:           string(searchCtx.Metric),
		Target:           searchCtx.Target,
		Tolerance:        searchCtx.Tolerance,
		CRFMin:           searchCtx.CRFMin,
		CRFMax:           searchCtx.CRFMax,
		InitialCRF:       searchCtx.InitialCRF,
		InitialCRFSource: initialCRFSource,
		StartedAt:        time.Now(),
	}
	fail := func(err error) targetQualityResult {
		return targetQualityResult{EncodeResult: worker.EncodeResult{ChunkIdx: ch.Idx, Error: err}, Log: log}
	}
	cache := r.newRefCache(ch)
	defer cache.remove()
	state := quality.NewSearchState(searchCtx)

	for {
		crf, ok := state.NextCRF(searchCtx)
		if !ok {
			break
		}
		probe, err := r.encodeAndScoreProbe(ctx, ch, crf, plan.pool, cache)
		if err != nil {
			return fail(err)
		}
		if plan.dualPool != nil {
			// Warmup probe in an SSIMU2 run: also score the same IVF with
			// SSIMU2 (cheap) to feed the per-title offset calibration.
			var s2 float32
			if err := r.withSlotReleased(ctx, func() error {
				var scoreErr error
				s2, _, scoreErr = r.scoreProbe(ctx, plan.dualPool, probeIVFPath(r.workDir, ch.Idx, crf), ch, cache)
				return scoreErr
			}); err != nil {
				return fail(err)
			}
			r.calibration.AddSample(probe.Score, s2)
		}
		state.AddProbe(searchCtx, probe)
		log.Probes = append(log.Probes, probe)
		if r.tq.Verbose != nil {
			var fps float64
			if probe.MetricSeconds > 0 {
				fps = float64(probe.Frames) / probe.MetricSeconds
			}
			initial := ""
			if state.Round == 1 {
				initial = fmt.Sprintf(" initial_source=%s", initialCRFSource)
			}
			rate := fmt.Sprintf(" peak_mbps=%.1f", probe.PeakBps/1e6)
			if quality.OverRate(searchCtx, probe) {
				// Rejected by the level cap: the score is ignored and the
				// search moves up. Without this the log shows a search
				// walking away from a near-target score for no reason.
				rate += " over_rate=true"
			}
			r.tq.Verbose(fmt.Sprintf("TQ probe chunk=%04d round=%d crf=%s%s score=%.4f delta=%+.4f size=%d%s frames=%d encode=%.1fs metric=%.1fs metric_fps=%.1f", ch.Idx, state.Round, quality.FormatCRF(crf), initial, probe.Score, probe.Score-searchCtx.Target, probe.Size, rate, probe.Frames, probe.EncodeSeconds, probe.MetricSeconds, fps))
		}
		if state.StopReason != quality.StopNone {
			break
		}
	}

	best, ok := state.BestProbe(searchCtx)
	if !ok {
		return fail(fmt.Errorf("no target-quality probes completed for chunk %04d", ch.Idx))
	}
	priorProbes := log.Probes
	if r.calibration != nil && searchCtx.Metric == quality.MetricCVVDP {
		// Warmup chunk in an SSIMU2 run: the prior operates on the SSIMU2
		// scale, so map JOD probe scores through the anchor plus whatever
		// offset is known by now (0 while still calibrating).
		offset := r.calibration.CurrentOffset()
		priorProbes = make([]quality.Probe, len(log.Probes))
		for i, p := range log.Probes {
			p.Score = quality.SSIMU2FromJOD(p.Score) + offset
			priorProbes[i] = p
		}
	}
	r.prior.AddResult(ch.Idx, best.CRF, priorProbes)
	// Every probe is a whole-chunk encode kept at its CRF path, so the converged
	// probe is reused verbatim as the final chunk -- no re-encode. copyFile errors
	// if that IVF is somehow absent.
	finalPath := chunk.IVFPath(r.workDir, ch.Idx)
	r.finishing.Add(1)
	defer r.finishing.Add(-1)
	if err := copyFile(probeIVFPath(r.workDir, ch.Idx, best.CRF), finalPath); err != nil {
		return fail(err)
	}
	stat, err := os.Stat(finalPath)
	if err != nil {
		return fail(err)
	}
	log.FinalCRF = best.CRF
	log.FinalScore = best.Score
	log.FinalSize = uint64(stat.Size())
	log.StopReason = state.StopReason
	log.CompletedAt = time.Now()
	_ = writeChunkTargetLog(r.workDir, log)
	if r.tq.Verbose != nil {
		r.tq.Verbose(fmt.Sprintf("TQ final chunk=%04d crf=%s score=%.4f size=%d probes=%d stop=%s", ch.Idx, quality.FormatCRF(best.CRF), best.Score, log.FinalSize, len(log.Probes), log.StopReason))
	}
	return targetQualityResult{EncodeResult: worker.EncodeResult{ChunkIdx: ch.Idx, Frames: ch.Frames(), Size: uint64(stat.Size())}, Log: log}
}

// encodeAndScoreProbe encodes the whole chunk at crf to a reusable IVF and
// scores it against the source with a single whole-chunk metric pass. The
// probe IVF is kept (fsynced) so the converged probe can be reused verbatim as
// the final chunk. The encode slot is released while the GPU scores, so other
// chunks can encode during the metric wait.
func (r *targetQualityRun) encodeAndScoreProbe(ctx context.Context, ch chunk.Chunk, crf float32, pool chan quality.ChunkScorer, cache *chunkRefCache) (quality.Probe, error) {
	if ch.Frames() <= 0 {
		return quality.Probe{}, fmt.Errorf("chunk %04d has no frames", ch.Idx)
	}
	probePath := probeIVFPath(r.workDir, ch.Idx, crf)

	encodeStart := time.Now()
	// Whole-chunk probe; reusable as the final chunk, so keep the fsync.
	probeResult := r.encodeProbe(ctx, ch, probePath, crf, cache)
	if probeResult.Error != nil {
		return quality.Probe{}, probeResult.Error
	}
	encodeSeconds := time.Since(encodeStart).Seconds()

	var score float32
	var metricSeconds float64
	if err := r.withSlotReleased(ctx, func() error {
		var scoreErr error
		score, metricSeconds, scoreErr = r.scoreProbe(ctx, pool, probePath, ch, cache)
		return scoreErr
	}); err != nil {
		return quality.Probe{}, err
	}

	peakBps, err := probePeakSecondBps(probePath, r.inf)
	if err != nil {
		return quality.Probe{}, err
	}

	return quality.Probe{
		CRF:           crf,
		Score:         score,
		Size:          probeResult.Size,
		PeakBps:       peakBps,
		EncodeSeconds: encodeSeconds,
		MetricSeconds: metricSeconds,
		Frames:        ch.Frames(),
	}, nil
}

// probePeakSecondBps measures a probe IVF's worst one-second bitrate for the
// search's rate gate.
func probePeakSecondBps(probePath string, inf *video.Info) (float64, error) {
	f, err := os.Open(probePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open probe for peak-rate scan: %w", err)
	}
	defer func() { _ = f.Close() }()
	peakBps, err := encoder.PeakSecondBps(bufio.NewReaderSize(f, 1<<20), inf.FPSNum, inf.FPSDen)
	if err != nil {
		return 0, fmt.Errorf("failed to scan probe peak rate: %w", err)
	}
	return peakBps, nil
}

func (r *targetQualityRun) scoreProbe(ctx context.Context, pool chan quality.ChunkScorer, probePath string, ch chunk.Chunk, cache *chunkRefCache) (float32, float64, error) {
	r.scoring.Add(1)
	defer r.scoring.Add(-1)
	var scorer quality.ChunkScorer
	select {
	case scorer = <-pool:
	case <-ctx.Done():
		return 0, 0, ctx.Err()
	}
	defer func() { pool <- scorer }()

	// The probe encode that just ran materialized the chunk's filtered frames,
	// so the reference comes from the cache instead of a second decode+filter
	// pass over the source.
	var reference video.FrameReader
	if cached := cache.open(); cached != nil {
		defer cached.Close()
		reference = cached
	}

	metricStart := time.Now()
	score, metricSeconds, err := scorer.ScoreChunk(ctx, quality.ChunkScoreRequest{
		SourcePath: r.inputPath,
		ProbePath:  probePath,
		Info:       r.inf,
		Chunk:      ch,
		CropRect:   r.cropRect,
		Width:      r.width,
		Height:     r.height,
		Denoise:    r.cfg.Denoise,
		Reference:  reference,
	})
	if err != nil {
		return 0, 0, err
	}
	if metricSeconds == 0 {
		metricSeconds = time.Since(metricStart).Seconds()
	}
	return score, metricSeconds, nil
}

func probeIVFPath(workDir string, chunkIdx int, crf float32) string {
	return filepath.Join(workDir, "probes", fmt.Sprintf("%04d_%s.ivf", chunkIdx, quality.FormatCRF(crf)))
}

// encodeProbe encodes a whole-chunk probe or final chunk to outputPath. The IVF
// is always fsynced: every probe is reusable as the final chunk and relied on
// for resume.
func (r *targetQualityRun) encodeProbe(ctx context.Context, ch chunk.Chunk, outputPath string, crf float32, cache *chunkRefCache) worker.EncodeResult {
	r.probing.Add(1)
	defer r.probing.Add(-1)
	if cached := cache.open(); cached != nil {
		defer cached.Close()
		return encodeChunkStreaming(ctx, cached, ch, r.inf, r.cfg, outputPath, crf, r.width, r.height, nil)
	}

	src, err := video.OpenFiltered(r.inputPath, 1, r.cfg.Denoise)
	if err != nil {
		return worker.EncodeResult{ChunkIdx: ch.Idx, Error: fmt.Errorf("failed to open source for probe: %w", err)}
	}
	defer src.Close()
	// Fresh decoder, but reset explicitly so the filter history contract is
	// stated at every chunk start rather than relied on implicitly.
	src.ResetFilter()

	frames, done := cache.fill(src.FrameReader(r.inf, r.cropRect))
	result := encodeChunkStreaming(ctx, frames, ch, r.inf, r.cfg, outputPath, crf, r.width, r.height, nil)
	done(result.Error == nil)
	return result
}

// newRefCache returns the chunk's filtered-frame cache, or nil when there is
// no filter to amortize: without a denoise graph a repeat read is a plain
// decode, which is cheaper than the tens of gigabytes the cache would cost.
func (r *targetQualityRun) newRefCache(ch chunk.Chunk) *chunkRefCache {
	if r.cfg.Denoise == "" {
		return nil
	}
	return newChunkRefCache(r.workDir, ch, video.FrameSize(r.inf, r.cropRect), r.cfg.WarningCallback)
}

func copyFile(srcPath, dstPath string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}
	if err := os.Link(srcPath, dstPath); err == nil {
		return nil
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}
