// Package encode provides the parallel chunk encoding pipeline.
package encode

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/encoder"
	"github.com/five82/reel/internal/util"
	"github.com/five82/reel/internal/video"
	"github.com/five82/reel/internal/worker"
)

// availableMemoryBytes returns currently available RAM in bytes, or 0 if it
// cannot be read. Used to seed the initial adaptive worker count.
func availableMemoryBytes() uint64 {
	if stats, ok := util.ReadMemoryStats(); ok {
		return stats.MemAvailable
	}
	return 0
}

// EncodeConfig contains configuration for the parallel encode pipeline.
type EncodeConfig struct {
	CRF        float32 // Quality (CRF value)
	Preset     uint8   // SVT-AV1 preset
	Tune       uint8   // SVT-AV1 tune
	GrainTable *string // Optional film grain table path
	// Denoise is an experimental libavfilter graph string (for example
	// "hqdn3d=2:1.5:3:2.25") applied to every frame before it reaches the
	// encoder AND to every reference frame the quality metric reads, so
	// target-quality scoring measures the encode against the denoised source
	// rather than the original. Empty disables it.
	Denoise            string
	LevelOfParallelism uint32 // SVT-AV1 level_of_parallelism (1-6); 0 lets Reel choose
	// StatusCallback receives verbose-only limiter status (ramp-up messages).
	StatusCallback func(message string)
	// WarningCallback receives degraded-behavior limiter status (worker
	// reductions and the critical cancel) unconditionally, independent of
	// verbose mode, since these describe output-affecting decisions.
	WarningCallback func(message string)

	// Advanced SVT-AV1 parameters
	ACBias                float32
	EnableVarianceBoost   bool
	VarianceBoostStrength uint8
	VarianceOctile        uint8
}

// ProgressCallback is called to report encoding progress.
type ProgressCallback func(progress worker.Progress)

type chunkProgressCallback func(chunkIdx, frames int)

// shouldReopenSource reports whether a worker needs a fresh decoder for the next chunk.
// Chunks may be dispatched by size, so a worker can receive an earlier chunk after a later one;
// reopening avoids unreliable backward seeks on some Matroska/HEVC sources.
func shouldReopenSource(nextFrame, chunkStart int) bool {
	return chunkStart < nextFrame
}

// EncodeAll runs the parallel encoding pipeline.
// Uses streaming frame pipeline: each worker decodes and encodes one frame at a time,
// avoiding the need to hold all frames in memory at once.
//
// Returns (maxWorkers, error) where maxWorkers is the adaptive concurrency ceiling.
func EncodeAll(
	ctx context.Context,
	chunks []chunk.Chunk,
	inputPath string,
	inf *video.Info,
	cfg *EncodeConfig,
	workDir string,
	cropRect *video.CropRect,
	progressCb ProgressCallback,
) (int, error) {
	// Ensure encode directory exists
	if err := chunk.EnsureEncodeDir(workDir); err != nil {
		return 0, fmt.Errorf("failed to create encode directory: %w", err)
	}

	// Load resume information
	resume, err := chunk.GetResume(workDir)
	if err != nil {
		return 0, fmt.Errorf("failed to load resume info: %w", err)
	}
	resume = resume.Validate(workDir, chunks)
	doneSet := resume.DoneSet()

	// Count remaining chunks
	remainingChunks := make([]chunk.Chunk, 0, len(chunks))
	totalFrames := 0
	for _, ch := range chunks {
		totalFrames += ch.Frames()
		if !doneSet[ch.Idx] {
			remainingChunks = append(remainingChunks, ch)
		}
	}

	if len(remainingChunks) == 0 {
		return MaxAdaptiveWorkers(), nil // All chunks already done
	}

	// Content-aware chunking creates variable-sized chunks. Dispatch larger chunks
	// first so the encode does not finish with one long tail chunk while other
	// workers are idle. Chunk indices are preserved for resume and final merge.
	sort.SliceStable(remainingChunks, func(i, j int) bool {
		return remainingChunks[i].Frames() > remainingChunks[j].Frames()
	})

	if cropRect != nil {
		if err := video.ValidateCropRect(inf, *cropRect); err != nil {
			return 0, fmt.Errorf("invalid crop rectangle: %w", err)
		}
	}

	// Calculate effective dimensions
	width, height := video.OutputDimensions(inf, cropRect)

	// Let the adaptive limiter ramp active encoders up/down based on real memory
	// pressure. Static memory estimates are intentionally not used as a hard cap:
	// they are too content/SVT-version dependent and can leave the machine underused.
	maxWorkers := MaxAdaptiveWorkers()
	initialWorkers := initialAdaptiveWorkers(maxWorkers, width, height, availableMemoryBytes())
	rampCeiling := resolutionRampCeiling(maxWorkers, width, height)
	limiter := newAdaptiveLimiter(maxWorkers, initialWorkers, rampCeiling, totalFrames, cfg.StatusCallback, cfg.WarningCallback)
	cfg.LevelOfParallelism = resolveLevelOfParallelism(cfg.LevelOfParallelism, rampCeiling)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Chunk channel - workers receive chunk metadata (not decoded frames)
	chunkChan := make(chan chunk.Chunk, maxWorkers)

	// Results channel
	resultChan := make(chan worker.EncodeResult, len(remainingChunks))

	// Progress tracking
	var progressMu sync.Mutex
	activeFrames := make(map[int]int, maxWorkers)
	progress := worker.Progress{
		ChunksTotal:    len(chunks),
		ChunksComplete: len(chunks) - len(remainingChunks),
		FramesTotal:    totalFrames,
		FramesComplete: resume.TotalEncodedFrames(),
		BytesComplete:  resume.TotalEncodedSize(),
	}
	limiter.observeProgress(progress.FramesComplete)
	snapshotProgress := func() worker.Progress {
		p := progress
		for _, frames := range activeFrames {
			p.FramesComplete += frames
		}
		p.FramesComplete = min(p.FramesComplete, p.FramesTotal)
		p.ActiveWorkers, p.TargetWorkers, p.MaxWorkers = limiter.stats()
		// Fixed-CRF work is slot-bound: a chunk is in flight exactly while it
		// holds an encode slot, so in-flight equals active workers.
		p.InFlight = p.ActiveWorkers
		p.EncodeSlotWaitSeconds = limiter.slotWaitSeconds()
		return p
	}
	chunkProgressCb := func(chunkIdx, frames int) {
		if progressCb == nil || frames < 0 {
			return
		}

		progressMu.Lock()
		if frames <= activeFrames[chunkIdx] {
			progressMu.Unlock()
			return
		}
		activeFrames[chunkIdx] = frames
		p := snapshotProgress()
		limiter.observeProgress(p.FramesComplete)
		progressMu.Unlock()

		progressCb(p)
	}

	// Error handling with atomic pointer for thread-safe access
	var encodeErr atomic.Pointer[error]
	setError := func(err error) {
		encodeErr.CompareAndSwap(nil, &err)
	}
	getError := func() error {
		if p := encodeErr.Load(); p != nil {
			return *p
		}
		return nil
	}

	// Start streaming workers - each creates its own decoder for thread safety
	var workerWg sync.WaitGroup
	for i := 0; i < maxWorkers; i++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			streamingWorker(ctx, inputPath, chunkChan, resultChan, limiter, cfg, inf, cropRect, workDir, width, height, chunkProgressCb, setError, getError)
		}()
	}

	// Start result collector
	var collectorWg sync.WaitGroup
	collectorWg.Add(1)
	go func() {
		defer collectorWg.Done()
		for result := range resultChan {
			if result.Error != nil {
				setError(result.Error)
				continue
			}

			// Update progress
			progressMu.Lock()
			delete(activeFrames, result.ChunkIdx)
			progress.ChunksComplete++
			progress.FramesComplete += result.Frames
			progress.BytesComplete += result.Size
			progressMu.Unlock()

			// Append to done file (ignore errors, resume will handle incomplete state)
			_ = chunk.AppendDone(chunk.ChunkComp{
				Idx:    result.ChunkIdx,
				Frames: result.Frames,
				Size:   result.Size,
			}, workDir)

			// Report progress
			if progressCb != nil {
				progressMu.Lock()
				p := snapshotProgress()
				limiter.observeProgress(p.FramesComplete)
				progressMu.Unlock()
				progressCb(p)
			}
		}
	}()

	go limiter.monitor(ctx, cancel, setError)
	go func() {
		<-ctx.Done()
		limiter.wake()
	}()

	// Chunk dispatcher goroutine
	go func() {
		defer close(chunkChan)

		for _, ch := range remainingChunks {
			if getError() != nil {
				return
			}
			_, err := limiter.acquire(ctx)
			if err != nil {
				return
			}

			// Send chunk metadata to worker
			select {
			case chunkChan <- ch:
				// Successfully sent
			case <-ctx.Done():
				limiter.release()
				return
			}
		}
	}()

	// Wait for workers to finish
	workerWg.Wait()
	close(resultChan)

	// Wait for result collector
	collectorWg.Wait()

	return maxWorkers, getError()
}

// streamingWorker runs in a goroutine and processes chunks using streaming decode/encode.
// Each worker creates its own decoder for thread safety, then streams frames one at a time.
func streamingWorker(
	ctx context.Context,
	inputPath string,
	chunkChan <-chan chunk.Chunk,
	resultChan chan<- worker.EncodeResult,
	limiter *adaptiveLimiter,
	cfg *EncodeConfig,
	inf *video.Info,
	cropRect *video.CropRect,
	workDir string,
	width, height uint32,
	progressCb chunkProgressCallback,
	setError func(error),
	getError func() error,
) {
	var src *video.Source
	srcNextFrame := 0
	defer func() {
		if src != nil {
			src.Close()
		}
	}()

	for ch := range chunkChan {
		// Check for cancellation
		select {
		case <-ctx.Done():
			limiter.release()
			resultChan <- worker.EncodeResult{
				ChunkIdx: ch.Idx,
				Error:    ctx.Err(),
			}
			continue
		default:
		}

		// Check for error from other workers
		if getError() != nil {
			limiter.release()
			continue
		}

		if src != nil && shouldReopenSource(srcNextFrame, ch.Start) {
			src.Close()
			src = nil
			srcNextFrame = 0
		}
		if src == nil {
			var err error
			src, err = video.OpenFiltered(inputPath, 1, cfg.Denoise)
			if err != nil {
				limiter.release()
				resultChan <- worker.EncodeResult{
					ChunkIdx: ch.Idx,
					Error:    fmt.Errorf("failed to create video source for worker: %w", err),
				}
				return
			}
		}

		// A worker reuses one Source across consecutive chunks, so reset
		// temporal denoise state at every chunk start: the scorers open a fresh
		// Source per chunk, and both sides must filter a frame with the same
		// history for the metric reference to be bit-identical to what the
		// encoder consumed.
		src.ResetFilter()

		// Encode the chunk using streaming (decode one frame, encode, repeat)
		result := encodeChunkStreaming(ctx, src.FrameReader(inf, cropRect), ch, inf, cfg, chunk.IVFPath(workDir, ch.Idx), cfg.CRF, width, height, progressCb)

		if result.Error == nil {
			srcNextFrame = ch.End
		}

		// Release adaptive worker slot
		limiter.release()

		// Send result
		resultChan <- result
	}
}

// encodeChunkStreaming decodes and encodes frames one at a time, reusing a single frame buffer.
// This dramatically reduces memory usage compared to decoding all frames upfront.
// Memory per worker: ~6 MB (single frame) instead of ~5 GB (all frames in chunk).
// frames supplies the chunk's post-crop 10-bit frames: a decoder for a first
// pass, or the reference cache for a repeat probe of the same chunk. Callers
// that reuse a decoder across chunks must reset its denoise filter state
// before this call.
func encodeChunkStreaming(
	ctx context.Context,
	frames video.FrameReader,
	ch chunk.Chunk,
	inf *video.Info,
	cfg *EncodeConfig,
	outputPath string,
	crf float32,
	width, height uint32,
	progressCb chunkProgressCallback,
) worker.EncodeResult {
	frameCount := ch.Frames()

	encCfg := &encoder.EncConfig{
		Inf:                   inf,
		CRF:                   crf,
		Preset:                cfg.Preset,
		Tune:                  cfg.Tune,
		Output:                outputPath,
		GrainTable:            cfg.GrainTable,
		Width:                 width,
		Height:                height,
		Frames:                frameCount,
		ACBias:                cfg.ACBias,
		EnableVarianceBoost:   cfg.EnableVarianceBoost,
		VarianceBoostStrength: cfg.VarianceBoostStrength,
		VarianceOctile:        cfg.VarianceOctile,
		LevelOfParallelism:    cfg.LevelOfParallelism,
	}

	frameIdx := 0
	readFrame := func(buf []byte) error {
		if frameIdx >= frameCount {
			return fmt.Errorf("readFrame called after all frames consumed")
		}
		idx := ch.Start + frameIdx
		if err := frames.ReadFrame(idx, buf); err != nil {
			return err
		}
		frameIdx++
		return nil
	}

	var lastReported int
	progressWrapper := func(encoded int) {
		if progressCb == nil {
			return
		}
		if encoded > lastReported {
			lastReported = encoded
			progressCb(ch.Idx, min(encoded, frameCount))
		}
	}

	if err := encoder.EncodeChunkToIVF(ctx, encCfg, readFrame, progressWrapper); err != nil {
		return worker.EncodeResult{
			ChunkIdx: ch.Idx,
			Error:    err,
		}
	}

	stat, err := os.Stat(outputPath)
	if err != nil {
		return worker.EncodeResult{
			ChunkIdx: ch.Idx,
			Error:    fmt.Errorf("failed to stat output: %w", err),
		}
	}

	return worker.EncodeResult{
		ChunkIdx: ch.Idx,
		Frames:   frameCount,
		Size:     uint64(stat.Size()),
	}
}
