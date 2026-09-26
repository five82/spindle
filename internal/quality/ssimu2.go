package quality

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/video"
)

type SSIMU2Options struct {
	SourcePath string
	ProbePath  string
	Info       *video.Info
	Chunk      chunk.Chunk
	CropRect   *video.CropRect
	Width      uint32
	Height     uint32
	Denoise    string // Experimental libavfilter graph applied to reference frames
	// Reference optionally supplies the chunk's reference frames already
	// decoded, cropped, and denoise-filtered; see CVVDPOptions.Reference.
	Reference video.FrameReader
	Processor *SSIMU2Processor
}

type SSIMU2Result struct {
	PerFrame      []float64
	Frames        int
	MetricSeconds float64
	Mean          float64
	Min           float64
	P5            float64
	P10           float64
}

// ComputeChunkSSIMU2 mirrors ComputeChunkCVVDP's decode-producer/GPU-consumer
// pipeline (see cvvdp.go): decode runs on a producer goroutine so CPU frame
// decode overlaps GPU metric compute, with two buffer pairs rotating between
// producer and consumer, and all GPU calls on this goroutine. Unlike CVVDP,
// SSIMU2 is stateless per frame pair -- there is no handler Reset and no
// running accumulation -- so this collects a per-frame score slice instead of
// a single final score.
func ComputeChunkSSIMU2(ctx context.Context, opts SSIMU2Options) (SSIMU2Result, error) {
	if opts.Processor == nil {
		return SSIMU2Result{}, fmt.Errorf("nil SSIMU2 processor")
	}
	if opts.Info == nil {
		return SSIMU2Result{}, fmt.Errorf("nil video info")
	}
	if opts.Width == 0 || opts.Height == 0 {
		return SSIMU2Result{}, fmt.Errorf("invalid SSIMU2 dimensions %dx%d", opts.Width, opts.Height)
	}

	return computeChunkSSIMU2(ctx, opts, opts.Processor, newMetricBuffer)
}

type ssimu2Scorer interface {
	ComputeSSIMU2(FramePlanes, FramePlanes) (float64, error)
}

func computeChunkSSIMU2(ctx context.Context, opts SSIMU2Options, processor ssimu2Scorer, allocate func(int) ([]byte, func(), error)) (SSIMU2Result, error) {
	ref := opts.Reference
	if ref == nil {
		// The reference is read through the same denoise graph the encoder
		// used, so the score measures the encode against the denoised source.
		src, err := video.OpenFiltered(opts.SourcePath, metricSourceDecoderThreads, opts.Denoise)
		if err != nil {
			return SSIMU2Result{}, fmt.Errorf("failed to open source for SSIMU2: %w", err)
		}
		defer src.Close()
		// Match the encoder worker, which resets filter state at every chunk start.
		src.ResetFilter()
		ref = src.FrameReader(opts.Info, opts.CropRect)
	}

	probeInfo, err := video.Probe(opts.ProbePath)
	if err != nil {
		return SSIMU2Result{}, fmt.Errorf("failed to probe encoded chunk for SSIMU2: %w", err)
	}
	dist, err := video.Open(opts.ProbePath, 1)
	if err != nil {
		return SSIMU2Result{}, fmt.Errorf("failed to open encoded chunk for SSIMU2: %w", err)
	}
	defer dist.Close()

	ctx, cancelDecode := context.WithCancel(ctx)
	defer cancelDecode()

	type framePair struct {
		srcBuf, distBuf       []byte
		srcPlanes, distPlanes FramePlanes
	}
	frameSize := yuv420p10Size(opts.Width, opts.Height)
	freeCh := make(chan *framePair, 2)
	for i := 0; i < 2; i++ {
		srcBuf, releaseSrc, err := allocate(frameSize)
		if err != nil {
			return SSIMU2Result{}, err
		}
		defer releaseSrc() // Runs after the producer's cancel/drain defer below.
		distBuf, releaseDist, err := allocate(frameSize)
		if err != nil {
			return SSIMU2Result{}, err
		}
		defer releaseDist()
		pair := &framePair{srcBuf: srcBuf, distBuf: distBuf}
		if pair.srcPlanes, err = PlanesFromYUV420P10(pair.srcBuf, opts.Width, opts.Height); err != nil {
			return SSIMU2Result{}, err
		}
		if pair.distPlanes, err = PlanesFromYUV420P10(pair.distBuf, opts.Width, opts.Height); err != nil {
			return SSIMU2Result{}, err
		}
		freeCh <- pair
	}

	type decodedFrame struct {
		pair *framePair
		err  error
	}
	decodedCh := make(chan decodedFrame, 2)
	// On early return, stop the producer and wait for it to exit before the
	// deferred decoder Closes run; the producer still holds src/dist.
	defer func() {
		cancelDecode()
		for range decodedCh { //nolint:revive // drain until the producer closes the channel
		}
	}()
	go func() {
		defer close(decodedCh)
		for i := 0; i < opts.Chunk.Frames(); i++ {
			var pair *framePair
			select {
			case pair = <-freeCh:
			case <-ctx.Done():
				decodedCh <- decodedFrame{err: ctx.Err()}
				return
			}
			if err := ref.ReadFrame(opts.Chunk.Start+i, pair.srcBuf); err != nil {
				decodedCh <- decodedFrame{err: fmt.Errorf("failed to read source frame %d for SSIMU2: %w", opts.Chunk.Start+i, err)}
				return
			}
			if err := dist.ReadFrame(i, pair.distBuf, probeInfo, nil); err != nil {
				decodedCh <- decodedFrame{err: fmt.Errorf("failed to read probe frame %d for SSIMU2: %w", i, err)}
				return
			}
			decodedCh <- decodedFrame{pair: pair}
		}
	}()

	start := time.Now()
	perFrame := make([]float64, 0, opts.Chunk.Frames())
	for decoded := range decodedCh {
		if decoded.err != nil {
			return SSIMU2Result{}, decoded.err
		}
		score, err := processor.ComputeSSIMU2(decoded.pair.srcPlanes, decoded.pair.distPlanes)
		if err != nil {
			return SSIMU2Result{}, fmt.Errorf("SSIMU2 failed on frame %d: %w", len(perFrame), err)
		}
		perFrame = append(perFrame, score)
		freeCh <- decoded.pair
	}

	mean, min, p5, p10 := ssimu2Stats(perFrame)
	return SSIMU2Result{
		PerFrame:      perFrame,
		Frames:        opts.Chunk.Frames(),
		MetricSeconds: time.Since(start).Seconds(),
		Mean:          mean,
		Min:           min,
		P5:            p5,
		P10:           p10,
	}, nil
}

// ssimu2Stats pools per-frame scores into Mean/Min/P5/P10. SSIMU2 scores can
// go negative on badly mismatched frames, so unlike CVVDP/JOD pooling
// elsewhere in Reel, this must not use a harmonic mean.
func ssimu2Stats(scores []float64) (mean, min, p5, p10 float64) {
	if len(scores) == 0 {
		return 0, 0, 0, 0
	}
	sorted := make([]float64, len(scores))
	copy(sorted, scores)
	sort.Float64s(sorted)

	min = sorted[0]
	var sum float64
	for _, s := range scores {
		sum += s
	}
	mean = sum / float64(len(scores))
	p5 = ssimu2Percentile(sorted, 0.05)
	p10 = ssimu2Percentile(sorted, 0.10)
	return mean, min, p5, p10
}

func ssimu2Percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}
