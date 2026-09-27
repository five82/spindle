package quality

import (
	"context"
	"fmt"
	"time"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/video"
)

type FramePlanes struct {
	Planes  [3]*byte
	Strides [3]int64
}

type CVVDPOptions struct {
	SourcePath string
	ProbePath  string
	Info       *video.Info
	Chunk      chunk.Chunk
	CropRect   *video.CropRect
	Width      uint32
	Height     uint32
	Denoise    string // Experimental libavfilter graph applied to reference frames
	// Reference optionally supplies the chunk's reference frames already
	// decoded, cropped, and denoise-filtered. When nil the source is decoded
	// and filtered here. See the target-quality reference cache: re-filtering
	// the reference for every probe dominated denoised 4K wall time.
	Reference video.FrameReader
	Processor *VshipProcessor
}

type CVVDPResult struct {
	Score         float32
	Frames        int
	MetricSeconds float64
}

// DenoiseCeilingOptions describes a denoise-ceiling measurement: CVVDP of the
// denoised source against the unfiltered source, with no encode in between.
type DenoiseCeilingOptions struct {
	SourcePath string
	Info       *video.Info
	Chunk      chunk.Chunk
	CropRect   *video.CropRect
	Width      uint32
	Height     uint32
	Denoise    string
	Processor  *VshipProcessor
	// Observe receives aligned YUV420P10LE pairs on the decode producer,
	// before their buffers are recycled. i is chunk-relative. It must not
	// retain or modify the slices. Only the ceiling pass samples grain.
	Observe func(i int, original, denoised []byte) error
}

// metricSourceDecoderThreads sizes the CVVDP reference decoder. One thread
// starves the GPU at 4K: HEVC10 decodes ~23 fps single-threaded against a
// ~50 fps GPU CVVDP ceiling, leaving the GPU mostly idle during 4K scoring.
// Two frame threads roughly double producer throughput. Kept low because four
// metric workers each own a decoder and compete with the SVT encode lanes for
// CPU. The probe decoder stays at one thread: AV1 probe decode is ~5x faster
// than the HEVC source and is not the producer bottleneck.
const metricSourceDecoderThreads = 2

// A split Open-before-pool-checkout / Compute-after variant (setup hoist,
// ring depth 3) was tested and rejected 2026-07-02: wall gain was within
// run-to-run noise, so the simpler single-function pass stays. See
// docs/PERFORMANCE_TESTING.md.
func ComputeChunkCVVDP(ctx context.Context, opts CVVDPOptions) (CVVDPResult, error) {
	if opts.Processor == nil {
		return CVVDPResult{}, fmt.Errorf("nil VSHIP processor")
	}
	if opts.Info == nil {
		return CVVDPResult{}, fmt.Errorf("nil video info")
	}
	if opts.Width == 0 || opts.Height == 0 {
		return CVVDPResult{}, fmt.Errorf("invalid CVVDP dimensions %dx%d", opts.Width, opts.Height)
	}
	return computeChunkCVVDP(ctx, opts, opts.Processor)
}

func computeChunkCVVDP(ctx context.Context, opts CVVDPOptions, processor cvvdpScorer) (CVVDPResult, error) {
	ref := opts.Reference
	if ref == nil {
		// The reference is read through the same denoise graph the encoder
		// used, so the score measures the encode against the denoised source.
		src, err := video.OpenFiltered(opts.SourcePath, metricSourceDecoderThreads, opts.Denoise)
		if err != nil {
			return CVVDPResult{}, fmt.Errorf("failed to open source for CVVDP: %w", err)
		}
		defer src.Close()
		// Match the encoder worker, which resets filter state at every chunk start.
		src.ResetFilter()
		ref = src.FrameReader(opts.Info, opts.CropRect)
	}

	probeInfo, err := video.Probe(opts.ProbePath)
	if err != nil {
		return CVVDPResult{}, fmt.Errorf("failed to probe encoded chunk for CVVDP: %w", err)
	}
	dist, err := video.Open(opts.ProbePath, 1)
	if err != nil {
		return CVVDPResult{}, fmt.Errorf("failed to open encoded chunk for CVVDP: %w", err)
	}
	defer dist.Close()

	readRef := func(i int, buf []byte) error {
		if err := ref.ReadFrame(opts.Chunk.Start+i, buf); err != nil {
			return fmt.Errorf("failed to read source frame %d for CVVDP: %w", opts.Chunk.Start+i, err)
		}
		return nil
	}
	readDist := func(i int, buf []byte) error {
		if err := dist.ReadFrame(i, buf, probeInfo, nil); err != nil {
			return fmt.Errorf("failed to read probe frame %d for CVVDP: %w", i, err)
		}
		return nil
	}
	return computeCVVDPFrames(ctx, processor, opts.Width, opts.Height, opts.Chunk.Frames(), readRef, readDist, nil)
}

// ComputeChunkDenoiseCeiling scores the denoised source against the unfiltered
// source for one chunk. Target-quality mode scores probes against the denoised
// reference, so its per-chunk scores overstate delivered quality by whatever
// the denoiser itself removed; this measures that honestly as the best score
// any encode of the denoised source could reach.
func ComputeChunkDenoiseCeiling(ctx context.Context, opts DenoiseCeilingOptions) (CVVDPResult, error) {
	if opts.Processor == nil {
		return CVVDPResult{}, fmt.Errorf("nil VSHIP processor")
	}
	if opts.Info == nil {
		return CVVDPResult{}, fmt.Errorf("nil video info")
	}
	if opts.Denoise == "" {
		return CVVDPResult{}, fmt.Errorf("denoise ceiling requires a denoise filter")
	}
	return computeChunkDenoiseCeiling(ctx, opts, opts.Processor)
}

func computeChunkDenoiseCeiling(ctx context.Context, opts DenoiseCeilingOptions, processor cvvdpScorer) (CVVDPResult, error) {
	original, err := video.Open(opts.SourcePath, metricSourceDecoderThreads)
	if err != nil {
		return CVVDPResult{}, fmt.Errorf("failed to open source for denoise ceiling: %w", err)
	}
	defer original.Close()
	denoised, err := video.OpenFiltered(opts.SourcePath, metricSourceDecoderThreads, opts.Denoise)
	if err != nil {
		return CVVDPResult{}, fmt.Errorf("failed to open denoised source for denoise ceiling: %w", err)
	}
	defer denoised.Close()
	denoised.ResetFilter()

	refReader := original.FrameReader(opts.Info, opts.CropRect)
	distReader := denoised.FrameReader(opts.Info, opts.CropRect)
	read := func(r video.FrameReader, what string) func(int, []byte) error {
		return func(i int, buf []byte) error {
			if err := r.ReadFrame(opts.Chunk.Start+i, buf); err != nil {
				return fmt.Errorf("failed to read %s frame %d for denoise ceiling: %w", what, opts.Chunk.Start+i, err)
			}
			return nil
		}
	}
	return computeCVVDPFrames(ctx, processor, opts.Width, opts.Height, opts.Chunk.Frames(),
		read(refReader, "source"), read(distReader, "denoised"), opts.Observe)
}

// computeCVVDPFrames runs the whole-chunk CVVDP pass. Decode runs in a
// producer goroutine so CPU frame decode overlaps the GPU metric compute; two
// buffer pairs rotate between the producer and the consumer. All GPU calls
// stay on this goroutine.
type cvvdpScorer interface {
	ResetCVVDP() error
	ComputeCVVDP(FramePlanes, FramePlanes) (float32, error)
}

func computeCVVDPFrames(
	ctx context.Context,
	processor cvvdpScorer,
	width, height uint32,
	frames int,
	readRef, readDist func(i int, buf []byte) error,
	observe func(i int, original, denoised []byte) error,
) (CVVDPResult, error) {
	return computeCVVDPFramesWithBuffer(ctx, processor, width, height, frames, readRef, readDist, observe, newMetricBuffer)
}

func computeCVVDPFramesWithBuffer(
	ctx context.Context,
	processor cvvdpScorer,
	width, height uint32,
	frames int,
	readRef, readDist func(i int, buf []byte) error,
	observe func(i int, original, denoised []byte) error,
	allocate func(int) ([]byte, func(), error),
) (CVVDPResult, error) {
	ctx, cancelDecode := context.WithCancel(ctx)
	defer cancelDecode()

	type framePair struct {
		srcBuf, distBuf       []byte
		srcPlanes, distPlanes FramePlanes
	}
	frameSize := yuv420p10Size(width, height)
	freeCh := make(chan *framePair, 2)
	for i := 0; i < 2; i++ {
		srcBuf, releaseSrc, err := allocate(frameSize)
		if err != nil {
			return CVVDPResult{}, err
		}
		defer releaseSrc() // Runs after the producer's cancel/drain defer below.
		distBuf, releaseDist, err := allocate(frameSize)
		if err != nil {
			return CVVDPResult{}, err
		}
		defer releaseDist()
		pair := &framePair{srcBuf: srcBuf, distBuf: distBuf}
		if pair.srcPlanes, err = PlanesFromYUV420P10(pair.srcBuf, width, height); err != nil {
			return CVVDPResult{}, err
		}
		if pair.distPlanes, err = PlanesFromYUV420P10(pair.distBuf, width, height); err != nil {
			return CVVDPResult{}, err
		}
		freeCh <- pair
	}

	type decodedFrame struct {
		pair *framePair
		err  error
	}
	decodedCh := make(chan decodedFrame, 2)
	// On early return, stop the producer and wait for it to exit before the
	// caller's deferred decoder Closes run; the producer still holds them.
	defer func() {
		cancelDecode()
		for range decodedCh { //nolint:revive // drain until the producer closes the channel
		}
	}()
	go func() {
		defer close(decodedCh)
		for i := 0; i < frames; i++ {
			var pair *framePair
			select {
			case pair = <-freeCh:
			case <-ctx.Done():
				decodedCh <- decodedFrame{err: ctx.Err()}
				return
			}
			if err := readCVVDPPair(ctx, i, pair.srcBuf, pair.distBuf, readRef, readDist, observe); err != nil {
				decodedCh <- decodedFrame{err: err}
				return
			}
			decodedCh <- decodedFrame{pair: pair}
		}
	}()

	// Each metric worker owns a DISTINCT VshipProcessor, so this whole
	// Reset->Compute sequence runs concurrently with other workers' handlers and
	// needs no locking. CORRECTNESS DEPENDS on libvship being built with
	// MITIGATE_MALLOC_ASYNC: the default cudaMallocAsync allocator races across
	// coexisting handlers and silently corrupts scores. Verify the linked library
	// with scripts/handlertest; see docs/VSHIP_CONCURRENCY_BUG.md.
	if err := processor.ResetCVVDP(); err != nil {
		return CVVDPResult{}, err
	}

	start := time.Now()
	var score float32
	computed := 0
	for decoded := range decodedCh {
		if decoded.err != nil {
			return CVVDPResult{}, decoded.err
		}
		var err error
		score, err = processor.ComputeCVVDP(decoded.pair.srcPlanes, decoded.pair.distPlanes)
		if err != nil {
			return CVVDPResult{}, fmt.Errorf("CVVDP failed on frame %d: %w", computed, err)
		}
		computed++
		freeCh <- decoded.pair
	}

	return CVVDPResult{
		Score:         score,
		Frames:        frames,
		MetricSeconds: time.Since(start).Seconds(),
	}, nil
}

// Keep paired observation on the producer, before either ring buffer can be
// recycled. Both readers must succeed before an estimator sees the pair.
func readCVVDPPair(ctx context.Context, i int, source, denoised []byte,
	readRef, readDist func(int, []byte) error, observe func(int, []byte, []byte) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := readRef(i, source); err != nil {
		return err
	}
	if err := readDist(i, denoised); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if observe != nil {
		if err := observe(i, source, denoised); err != nil {
			return fmt.Errorf("frame-pair analysis failed on frame %d: %w", i, err)
		}
	}
	return nil
}

func PlanesFromYUV420P10(buf []byte, width, height uint32) (FramePlanes, error) {
	required := yuv420p10Size(width, height)
	if len(buf) < required {
		return FramePlanes{}, fmt.Errorf("YUV420P10 buffer too small: got %d, need %d", len(buf), required)
	}
	ySize := int(width * height * 2)
	uvSize := int(width * height / 2)
	return FramePlanes{
		Planes: [3]*byte{&buf[0], &buf[ySize], &buf[ySize+uvSize]},
		Strides: [3]int64{
			int64(width * 2),
			int64((width / 2) * 2),
			int64((width / 2) * 2),
		},
	}, nil
}

func yuv420p10Size(width, height uint32) int {
	return int(width * height * 3)
}
