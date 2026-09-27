package quality

import (
	"context"
	"fmt"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/video"
)

// MetricKind selects the perceptual metric that scores target-quality probes.
type MetricKind string

const (
	MetricCVVDP  MetricKind = "cvvdp"
	MetricSSIMU2 MetricKind = "ssimulacra2"
)

// SSIMU2 target band, calibrated against the shipped CVVDP band on the 1080p
// SDR test corpus. At the 9.55 JOD center, the measured corpus median is 67.4
// SSIMU2 with a 37.5 points/JOD local exchange rate, so 67.4 +/- 7.5 preserves
// the 0.20 JOD half-width. See docs/PERFORMANCE_TESTING.md. These are
// mean-pooled per-frame scores (mean is the tightest pooling at constant
// CVVDP; percentiles amplify content-dependent worst-frame variance).
const (
	SSIMU2Target    float32 = 67.4
	SSIMU2Tolerance float32 = 7.5
)

// ProbeMetricForSource picks the probe metric for a source. SDR at or below
// 1080p uses SSIMULACRA2: CVVDP pays its display-model resize to a 4K raster
// even for 1080p input, making 1080p metric-bound (85-91% of encode-phase
// wall), while SSIMU2 runs ~8.5x faster on the same libvship and a fixed
// SSIMU2 target holds the CVVDP band to ~sd 0.10 JOD. HDR and >1080p keep
// CVVDP: SSIMU2 has no display/luminance model, and 4K is encode-bound so a
// faster metric buys little there. Source dimensions (not post-crop output)
// keep the choice stable across crop detection.
func ProbeMetricForSource(inf *video.Info) MetricKind {
	if inf == nil || isHDR(inf) {
		return MetricCVVDP
	}
	if inf.Width > 1920 || inf.Height > 1080 {
		return MetricCVVDP
	}
	return MetricSSIMU2
}

// JODAnchor ties the SSIMU2 scale to the CVVDP quality policy: SSIMU2Target
// corresponds to JODAnchorTarget on the shipped display model, and per-title
// calibration measures each title's offset from that corpus-level anchor
// (grainy content sits several points lower at equal JOD, clean digital
// higher -- per-title sd ~0.13 JOD, which a global target cannot absorb:
// the 2026-07-10 pilot A/B over-encoded a grainy clip +32% in size).
const (
	JODAnchorTarget    float32 = 9.55
	JODAnchorTolerance float32 = 0.20
)

// SSIMU2FromJOD maps a CVVDP JOD score onto the corpus-anchored SSIMU2 scale.
func SSIMU2FromJOD(jod float32) float32 {
	return SSIMU2Target + MetricSSIMU2.ScoreScale()*(jod-JODAnchorTarget)
}

// ScoreScale converts the calibrated CVVDP/JOD search constants into the
// metric's units: 1 for CVVDP, 37.5 for SSIMU2 (the measured pts/JOD exchange
// rate). Every score-denominated search constant multiplies by this so the
// search behaves identically in perceptual terms regardless of probe metric.
func (k MetricKind) ScoreScale() float32 {
	if k == MetricSSIMU2 {
		return 37.5
	}
	return 1
}

// DefaultSlopePerCRF is the no-information score-per-CRF slope used until
// measured slopes accumulate: the long-standing calibrated 0.025 JOD/CRF
// (see target_quality.go) in the metric's units. For SSIMU2 that is 0.9375
// pts/CRF, matching the measured clean-content SSIMU2 slope (~0.9-1.0).
func (k MetricKind) DefaultSlopePerCRF() float32 {
	return 0.025 * k.ScoreScale()
}

// SlopeClamp bounds measured probe slopes admitted into the learned-slope
// median, rejecting noise-dominated pairs: the original calibrated
// 0.005-0.2 JOD/CRF window in the metric's units.
func (k MetricKind) SlopeClamp() (min, max float32) {
	return 0.005 * k.ScoreScale(), 0.2 * k.ScoreScale()
}

// ChunkScoreRequest describes one whole-chunk probe scoring job, independent
// of which metric performs it.
type ChunkScoreRequest struct {
	SourcePath string
	ProbePath  string
	Info       *video.Info
	Chunk      chunk.Chunk
	CropRect   *video.CropRect
	Width      uint32
	Height     uint32
	// Denoise is the experimental libavfilter graph the encoder ran the source
	// through. The reference frames must go through the same graph, otherwise
	// the metric would score the encode against pixels it never saw.
	Denoise string
	// Reference optionally supplies the chunk's reference frames already
	// decoded, cropped, and filtered, bypassing the decode+filter pass here.
	Reference video.FrameReader
}

// ChunkScorer scores whole chunks with one metric. Implementations own a
// GPU handler and are not safe for concurrent use; the target-quality encoder
// keeps one scorer per metric worker (see the MITIGATE_MALLOC_ASYNC note in
// target_quality.go).
type ChunkScorer interface {
	ScoreChunk(ctx context.Context, req ChunkScoreRequest) (score float32, metricSeconds float64, err error)
	Close() error
}

// NewChunkScorer builds a scorer for the metric kind. displayPath is only
// consulted for CVVDP (SSIMU2 has no display model).
func NewChunkScorer(kind MetricKind, width, height uint32, inf *video.Info, displayPath string) (ChunkScorer, error) {
	switch kind {
	case MetricSSIMU2:
		proc, err := NewSSIMU2Processor(width, height, inf)
		if err != nil {
			return nil, err
		}
		return &ssimu2ChunkScorer{proc: proc}, nil
	case MetricCVVDP, "":
		proc, err := NewVshipProcessor(width, height, inf, displayPath)
		if err != nil {
			return nil, err
		}
		return &cvvdpChunkScorer{proc: proc}, nil
	default:
		return nil, fmt.Errorf("unknown probe metric %q", kind)
	}
}

type cvvdpChunkScorer struct {
	proc *VshipProcessor
}

func (s *cvvdpChunkScorer) ScoreChunk(ctx context.Context, req ChunkScoreRequest) (float32, float64, error) {
	res, err := ComputeChunkCVVDP(ctx, CVVDPOptions{
		SourcePath: req.SourcePath,
		ProbePath:  req.ProbePath,
		Info:       req.Info,
		Chunk:      req.Chunk,
		CropRect:   req.CropRect,
		Width:      req.Width,
		Height:     req.Height,
		Denoise:    req.Denoise,
		Reference:  req.Reference,
		Processor:  s.proc,
	})
	if err != nil {
		return 0, 0, err
	}
	return res.Score, res.MetricSeconds, nil
}

func (s *cvvdpChunkScorer) Close() error { return s.proc.Close() }

type ssimu2ChunkScorer struct {
	proc *SSIMU2Processor
}

func (s *ssimu2ChunkScorer) ScoreChunk(ctx context.Context, req ChunkScoreRequest) (float32, float64, error) {
	res, err := ComputeChunkSSIMU2(ctx, SSIMU2Options{
		SourcePath: req.SourcePath,
		ProbePath:  req.ProbePath,
		Info:       req.Info,
		Chunk:      req.Chunk,
		CropRect:   req.CropRect,
		Width:      req.Width,
		Height:     req.Height,
		Denoise:    req.Denoise,
		Reference:  req.Reference,
		Processor:  s.proc,
	})
	if err != nil {
		return 0, 0, err
	}
	return float32(res.Mean), res.MetricSeconds, nil
}

func (s *ssimu2ChunkScorer) Close() error { return s.proc.Close() }
