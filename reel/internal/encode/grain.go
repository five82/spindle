package encode

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/config"
	"github.com/five82/spindle/reel/internal/encoder"
	"github.com/five82/spindle/reel/internal/perf"
	"github.com/five82/spindle/reel/internal/quality"
	"github.com/five82/spindle/reel/internal/video"
)

// Grain treatment: grainy titles are encoded from a denoised source with a
// film grain table attached, so the decoder re-synthesizes texture instead of
// the encoder spending bits coding noise. Clean titles are left alone, because
// denoising them only costs quality (measured: 3% bytes for 0.13 JOD).
//
// Which titles are grainy is decided by bits-at-CRF, not by any grain
// estimator: a fixed-CRF probe encode of a few sample chunks separates heavy
// grain from clean content by roughly an order of magnitude (a grainy 4K disc
// title costs ~30 Mbps at CRF 26 where clean titles cost 1-5 Mbps). See
// docs/PERFORMANCE_TESTING.md, "Denoise and film grain".
const (
	// grainGateCRF is the fixed quality the sample chunks are probed at. It is
	// a measuring stick, not a quality decision: the same CRF for every title
	// is what makes the measured bitrates comparable across titles.
	grainGateCRF float32 = 22

	// grainGateSamples is how many chunks are probed. Within-title spread is
	// large (the 2026-08-31 calibration measured 2x-24x between a title's own
	// samples), and at five samples the median misclassified two of twelve
	// ground-truth titles purely from sampling noise; twelve samples cut the
	// false-positive rate from ~7% to ~1% while still costing only a few
	// minutes (~2-3% of a 4K feature encode). Variance is shot-to-shot, so
	// more short samples beat fewer long ones.
	grainGateSamples = 12

	// grainGateMinChunkFrames skips chunks too short for a stable measurement
	// (keyframe overhead dominates a very short chunk's bitrate).
	grainGateMinChunkFrames = 100

	// grainGateMiddleFraction restricts sampling to the middle 60% of the
	// title, avoiding titles/credits/logos, which are not representative.
	grainGateMiddleFraction = 0.6
)

// Bits per pixel per frame thresholds, measured from the coded (post-crop)
// geometry so letterbox bars cannot deflate them. Calibrated 2026-08-31
// against 15 UHD rips with known target-quality outcomes (RESULTS.md section
// 8 in the denoise study): the accepted and complained-about classes separate
// 2x at a stable estimator (0.055 vs 0.111 median bpp), so anything in
// 0.060-0.090 classifies identically. For intuition at 3840x2160@23.976:
//
//	0.0703 bpp = 14 Mbps.
//
// These cutoffs decide treatment, not synthesis strength. The historical
// LightBPP names remain because they are also part of the stats contract.
const (
	uhdLightBPP float64 = 0.0703
	// HD remains provisional: five sources and only one with target-quality
	// ground truth. Revisit after actual 1080p viewing verdicts.
	hdLightBPP float64 = 0.22
)

// Ambiguous band: a fixed-CRF median at or above these values but below the
// treat cutoff decides nothing on its own, and the samples are re-measured at
// the quality target instead (stage 2).
//
// Bits at a fixed CRF are a grain/detail proxy, not a prediction of what the
// quality target spends: over the twelve ground-truth titles in the
// 2026-08-31 calibration the ratio of delivered target-quality bitrate to
// CRF 22 bitrate spans 0.38x-4.34x. American Hustle measures 0.048 bpp here -
// below the cutoff under any resampling - yet is delivered at 18 Mbps and was
// complained about, while Meet the Parents measures 0.055 and was accepted at
// 9.9 Mbps. The two are indistinguishable at CRF 22 and 1.9x apart at the
// target, so lowering the cutoff to catch the first would misclassify the
// second; only measuring at the target separates them.
//
// The floor is where the extra probes stop paying: every measured title below
// 0.030 bpp is delivered at 8.6 Mbps or less (Alien, the largest ratio in the
// corpus at 4.34x, is 0.0099 bpp and 8.6 Mbps), well under the 14 Mbps treat
// line. The HD floor carries the UHD floor/treat ratio (0.43) across and is
// as provisional as the HD cutoffs above.
const (
	uhdAmbiguousBPP float64 = 0.030
	hdAmbiguousBPP  float64 = 0.10
)

// Gate stages, recorded in the verdict so a treat/no-treat decision can be
// read back to the measurement that made it.
const (
	grainStageBPP     = "bpp"
	grainStageTQProbe = "tq_probe"
)

// Stage 2 probes a short fixed-CRF ladder per sample chunk to find what the
// chunk costs at the quality target. It is deliberately separate from the
// target-quality search: it needs a size at the target, not a converged CRF,
// and it must run before the encode starts.
const (
	// grainStage2StartCRF opens the ladder. Target-quality encodes of this
	// library settle in the mid-20s, so the first probe usually lands in or
	// beside the band and one or two more finish the bracket.
	grainStage2StartCRF float32 = 26

	// grainStage2MaxProbes caps the ladder per sample chunk. A bracketing
	// pair interpolates the delivered size well enough to compare against
	// cutoffs the two classes straddle by 1.9x, and the gate runs this on
	// every sample of an ambiguous title.
	grainStage2MaxProbes = 3

	// grainStage2MaxCRFStep bounds one ladder step. At the default
	// 0.025 JOD/CRF slope this is 0.25 JOD; jumping further on a slope that
	// has not been measured yet tends to overshoot and waste a probe.
	grainStage2MaxCRFStep float32 = 10
)

// grainStage2Metric is CVVDP for every title, including the SDR 1080p ones
// whose encode search probes with SSIMULACRA2: an SSIMU2 target is only
// meaningful after the per-title calibration warmup (see
// quality.ProbeMetricForSource and tq_calibration.go), which has not run when
// the gate decides. CVVDP needs no per-title calibration and the gate scores
// only a handful of samples.
const grainStage2Metric = quality.MetricCVVDP

// grainDenoiseFilter is the denoiser every treated title runs through.
// fftdnoiz at defaults was the only denoiser that survived the honest control
// (re-running the undenoised baseline at the denoised run's source-referenced
// JOD): -34% and -13% bytes at matched honest quality on two coarse-grain 4K
// titles, where hqdn3d lost to simply lowering the quality target. It costs
// about 0.3 CPU-seconds per 4K frame, which the reference cache amortizes.
const grainDenoiseFilter = "fftdnoiz"

// grainModeOverride is the recorded mode when the experimental
// --denoise/--fgs-table flags decide the treatment instead of the gate.
const grainModeOverride = "override"

// GrainTreatment is the resolved per-title treatment plus the record of how it
// was decided.
type GrainTreatment struct {
	// Denoise is the libavfilter graph to run every encoder input and metric
	// reference frame through; empty means untreated.
	Denoise string
	// TablePath is the film grain table to attach, materialized in the work
	// directory; empty means none.
	TablePath string
	Stats     *perf.GrainTreatmentStats
}

// GrainGateInput describes the title the gate measures.
type GrainGateInput struct {
	InputPath string
	WorkDir   string
	Info      *video.Info
	Chunks    []chunk.Chunk
	CropRect  *video.CropRect
	// DisplayPath is the CVVDP display model for the paired-frame ceiling
	// pass. Treated titles require it; that pass also samples the grain.
	DisplayPath string
	// BandTopJOD is the top of the configured target-quality band, recorded
	// in the stats for consumers judging the measured ceiling.
	BandTopJOD float64
	// BandCenterJOD is the target the band is centered on; stage 2 measures
	// what the sample chunks cost there. Zero disables stage 2.
	BandCenterJOD float64
	Verbose       func(string)
}

// RecordedGrainTreatment returns the treatment this work directory has
// already settled on, without running the gate: an explicit override, the
// verdict a previous run recorded, or nothing decided yet. The chunked
// pipeline builds the resume manifest from it, so a work directory can never
// be resumed under a treatment its finished chunks were not encoded with.
func RecordedGrainTreatment(mode string, cfg *EncodeConfig, in GrainGateInput) (GrainTreatment, error) {
	return resolveGrainTreatment(context.Background(), mode, cfg, in, false)
}

// ResolveGrainTreatment decides how a title is treated and materializes what
// the encode needs. mode is config.GrainTreatmentAuto or GrainTreatmentOff.
// Explicit experimental cfg.Denoise/cfg.GrainTable win over the gate.
//
// A verdict recorded by an earlier run of the same work directory is reused
// verbatim: re-running the gate could disagree with the treatment the already
// encoded chunks were produced under. The resume manifest discards the verdict
// along with the rest of the state when the input or encode settings change.
func ResolveGrainTreatment(ctx context.Context, mode string, cfg *EncodeConfig, in GrainGateInput) (GrainTreatment, error) {
	return resolveGrainTreatment(ctx, mode, cfg, in, true)
}

// resolveGrainTreatment is the shared decision. With gate false it reports
// only what is already known and materializes nothing.
func resolveGrainTreatment(ctx context.Context, mode string, cfg *EncodeConfig, in GrainGateInput, gate bool) (GrainTreatment, error) {
	if cfg.Denoise != "" || (cfg.GrainTable != nil && *cfg.GrainTable != "") {
		table := ""
		if cfg.GrainTable != nil {
			table = *cfg.GrainTable
		}
		return GrainTreatment{
			Denoise:   cfg.Denoise,
			TablePath: table,
			Stats: &perf.GrainTreatmentStats{
				Mode:            grainModeOverride,
				Treated:         cfg.Denoise != "" || table != "",
				ResolutionClass: resolutionClass(in.Info.Width),
				Denoise:         cfg.Denoise,
				GrainTable:      table,
				Reason:          "explicit --denoise/--fgs-table overrides the grain gate",
			},
		}, nil
	}
	if mode != config.GrainTreatmentAuto {
		return GrainTreatment{Stats: &perf.GrainTreatmentStats{
			Mode:            config.GrainTreatmentOff,
			ResolutionClass: resolutionClass(in.Info.Width),
			Reason:          "grain treatment disabled",
		}}, nil
	}

	if err := ctx.Err(); err != nil {
		return GrainTreatment{}, err
	}
	verdict, err := loadGrainVerdict(in.WorkDir)
	if err != nil {
		return GrainTreatment{}, err
	}
	if verdict == nil {
		if !gate {
			return GrainTreatment{}, nil
		}
		stats, err := runGrainGate(ctx, cfg, in)
		if err != nil {
			return GrainTreatment{}, err
		}
		stats.BandTopJOD = in.BandTopJOD
		verdict = &grainVerdict{GrainTreatmentStats: *stats}
		if stats.Treated {
			if !encoder.FGSTableSupported() {
				return GrainTreatment{}, fmt.Errorf("grain treatment requires SVT-AV1 film grain table support (>= 2.3.0)")
			}
			estimate, err := estimateGrainAndCeiling(ctx, in, &verdict.GrainTreatmentStats)
			if err != nil {
				return GrainTreatment{}, fmt.Errorf("grain estimation failed: %w", err)
			}
			verdict.setEstimate(estimate)
		}
		if err := ctx.Err(); err != nil {
			return GrainTreatment{}, err
		}
		// No chunks can start before this returns. Publish the verdict AND
		// exact model together only after estimation succeeds. An interruption
		// before publication repeats the gate, never resumes a half-decision.
		if err := saveGrainVerdict(in.WorkDir, verdict); err != nil {
			return GrainTreatment{}, err
		}
	} else {
		verdict.Reused = true
		if gate && in.Verbose != nil {
			in.Verbose("Grain gate: reusing the recorded verdict and exact grain model")
		}
	}

	treatment := GrainTreatment{Stats: &verdict.GrainTreatmentStats}
	if verdict.Treated {
		treatment.Denoise = verdict.Denoise
		if gate {
			if !encoder.FGSTableSupported() {
				return GrainTreatment{}, fmt.Errorf("grain treatment requires SVT-AV1 film grain table support (>= 2.3.0)")
			}
			path := filepath.Join(in.WorkDir, "grain-estimated.tbl")
			if err := writeGrainFile(path, []byte(verdict.Table)); err != nil {
				return GrainTreatment{}, err
			}
			treatment.TablePath = path
		}
	}
	return treatment, nil
}

// runGrainGate probes sample chunks at a fixed CRF and converts the bits they
// cost into a treatment verdict.
func runGrainGate(ctx context.Context, cfg *EncodeConfig, in GrainGateInput) (*perf.GrainTreatmentStats, error) {
	return runGrainGateWithMeasure(ctx, cfg, in, measureChunkBPP)
}

// grainBPPMeasure is the sample encode boundary; tests substitute measured bit costs.
type grainBPPMeasure func(context.Context, *EncodeConfig, GrainGateInput, chunk.Chunk, string, uint32, uint32) (float64, error)

func runGrainGateWithMeasure(ctx context.Context, cfg *EncodeConfig, in GrainGateInput, measure grainBPPMeasure) (*perf.GrainTreatmentStats, error) {
	width, height := video.OutputDimensions(in.Info, in.CropRect)
	// Classify by SOURCE width, not the coded (post-crop) width: a
	// pillarboxed 1080p film (Mary Poppins crops 1920 -> 1792) is not an SD
	// source, and quality.ProbeMetricForSource uses the same convention so
	// the choice stays stable across crop detection. The bpp MEASUREMENT
	// below still uses coded geometry, so bars cannot deflate it.
	stats := &perf.GrainTreatmentStats{
		Mode:            config.GrainTreatmentAuto,
		ResolutionClass: resolutionClass(in.Info.Width),
		GateCRF:         float64(grainGateCRF),
	}
	if stats.ResolutionClass == "sd" {
		// SD sources are too small to spend bits on grain synthesis and too
		// far from the measured corpus to trust a cutoff on.
		stats.Reason = "SD sources are never treated"
		return stats, nil
	}
	stats.AmbiguousBPPCutoff, stats.LightBPPCutoff = bppCutoffs(in.Info.Width)

	samples := selectGrainSampleChunks(in.Chunks, in.Info.Frames)
	if len(samples) == 0 {
		stats.Reason = "no chunk long enough to measure"
		return stats, nil
	}

	gateDir := filepath.Join(in.WorkDir, "gate")
	if err := os.MkdirAll(gateDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create grain gate directory: %w", err)
	}
	// The sample encodes are measurements, not output: remove them (and the
	// directory) as soon as their bitrates are read.
	defer func() { _ = os.RemoveAll(gateDir) }()

	gateCfg := *cfg
	gateCfg.CRF = grainGateCRF
	gateCfg.Denoise = ""
	gateCfg.GrainTable = nil

	start := time.Now()
	for _, ch := range samples {
		bpp, err := measure(ctx, &gateCfg, in, ch, filepath.Join(gateDir, fmt.Sprintf("%04d.ivf", ch.Idx)), width, height)
		if err != nil {
			return nil, err
		}
		stats.SampleChunks = append(stats.SampleChunks, ch.Idx)
		stats.SampleBPP = append(stats.SampleBPP, bpp)
		if in.Verbose != nil {
			in.Verbose(fmt.Sprintf("Grain gate sample chunk=%04d frames=%d crf=%s bpp=%.4f (%.1f Mbps)", ch.Idx, ch.Frames(), quality.FormatCRF(grainGateCRF), bpp, mbpsFromBPP(bpp, width, height, in.Info)))
		}
	}
	stats.GateSeconds = time.Since(start).Seconds()
	stats.MedianBPP = median(stats.SampleBPP)
	stats.GateStage = grainStageBPP
	stats.Treated = grainTreats(stats.MedianBPP, stats.LightBPPCutoff)

	if grainStage2Applies(stats.MedianBPP, stats.AmbiguousBPPCutoff, stats.LightBPPCutoff) {
		measure, closeScorer, err := newGrainStage2Measure(&gateCfg, in, gateDir, width, height)
		if err != nil {
			// A refinement that cannot run leaves the stage 1 verdict
			// (untreated, since stage 2 only runs below the treat line)
			// rather than failing the encode.
			stats.Stage2Error = err.Error()
			if in.Verbose != nil {
				in.Verbose(fmt.Sprintf("Grain gate: target-quality re-measurement not run: %v", err))
			}
		} else {
			applyGrainStage2(ctx, in, stats, samples, measure)
			closeScorer()
		}
	}

	if stats.Treated {
		stats.Denoise = grainDenoiseFilter
	}
	return stats, nil
}

// measureChunkBPP encodes one sample chunk and returns its bits per pixel per
// frame over the coded (post-crop) geometry.
func measureChunkBPP(ctx context.Context, gateCfg *EncodeConfig, in GrainGateInput, ch chunk.Chunk, outputPath string, width, height uint32) (float64, error) {
	return measureChunkBPPWithEncode(in, ch, outputPath, width, height, func(reader video.FrameReader) error {
		return encodeChunkStreaming(ctx, reader, ch, in.Info, gateCfg, outputPath, gateCfg.CRF, width, height, nil).Error
	})
}

func measureChunkBPPWithEncode(in GrainGateInput, ch chunk.Chunk, outputPath string, width, height uint32, encode func(video.FrameReader) error) (float64, error) {
	src, err := video.Open(in.InputPath, 1)
	if err != nil {
		return 0, fmt.Errorf("failed to open source for grain gate: %w", err)
	}
	defer src.Close()

	if err := encode(src.FrameReader(in.Info, in.CropRect)); err != nil {
		return 0, fmt.Errorf("grain gate sample chunk %04d failed: %w", ch.Idx, err)
	}
	f, err := os.Open(outputPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open grain gate sample: %w", err)
	}
	defer func() { _ = f.Close() }()
	videoBytes, err := encoder.IVFVideoBytes(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		return 0, fmt.Errorf("failed to measure grain gate sample: %w", err)
	}
	pixels := float64(width) * float64(height) * float64(ch.Frames())
	if pixels == 0 {
		return 0, fmt.Errorf("grain gate sample chunk %04d has no pixels", ch.Idx)
	}
	return float64(videoBytes) * 8 / pixels, nil
}

// grainStage2Applies reports whether the fixed-CRF median is ambiguous: high
// enough that the target could still cost treat-level bits, but below the
// cutoff that decides on its own. At or above the cutoff stage 1 is definitive
// (both complained-about titles clear it under every resampling), so the
// probes would only cost time.
func grainStage2Applies(medianBPP, ambiguousCutoff, lightCutoff float64) bool {
	return ambiguousCutoff > 0 && medianBPP >= ambiguousCutoff && medianBPP < lightCutoff
}

// grainStage2Measure returns what one sample chunk costs at the quality
// target, in bits per pixel per frame, plus the probes it took. Injected so
// the stage 2 decision is testable without an encoder or a GPU.
type grainStage2Measure func(ctx context.Context, ch chunk.Chunk) (deliveredBPP float64, probes int, err error)

// stage2Probe is one rung of the ladder: what a fixed CRF scored and cost.
type stage2Probe struct {
	crf   float32
	score float32
	bpp   float64
}

// applyGrainStage2 replaces the fixed-CRF verdict with one taken from the bits
// the samples actually cost at the quality target, compared against the same
// cutoffs. A measurement failure leaves the stage 1 verdict standing: stage 2
// is a refinement, and it only runs below the treat line, so falling back is
// always the untreated answer the gate would have given anyway.
func applyGrainStage2(ctx context.Context, in GrainGateInput, stats *perf.GrainTreatmentStats, samples []chunk.Chunk, measure grainStage2Measure) {
	start := time.Now()
	// Recorded on the failure path too: the probes still cost the time.
	defer func() { stats.Stage2Seconds = time.Since(start).Seconds() }()
	delivered := make([]float64, 0, len(samples))
	for _, ch := range samples {
		bpp, probes, err := measure(ctx, ch)
		stats.Stage2Probes += probes
		if err != nil {
			stats.Stage2Error = err.Error()
			if in.Verbose != nil {
				in.Verbose(fmt.Sprintf("Grain gate: target-quality re-measurement failed: %v", err))
			}
			return
		}
		delivered = append(delivered, bpp)
		if in.Verbose != nil {
			in.Verbose(fmt.Sprintf("Grain gate stage 2 chunk=%04d probes=%d delivered_bpp=%.4f", ch.Idx, probes, bpp))
		}
	}
	if len(delivered) == 0 {
		stats.Stage2Error = "no sample chunks measured"
		return
	}
	stats.Stage2DeliveredBPP = delivered
	stats.Stage2MedianBPP = median(delivered)
	stats.GateStage = grainStageTQProbe
	stats.Treated = grainTreats(stats.Stage2MedianBPP, stats.LightBPPCutoff)
}

// newGrainStage2Measure builds the production measurement: a CVVDP scorer plus
// the per-chunk probe ladder. The returned close function releases the scorer's
// GPU handler.
func newGrainStage2Measure(gateCfg *EncodeConfig, in GrainGateInput, dir string, width, height uint32) (grainStage2Measure, func(), error) {
	if in.DisplayPath == "" {
		return nil, nil, fmt.Errorf("no display model")
	}
	target := float32(in.BandCenterJOD)
	tolerance := float32(in.BandTopJOD - in.BandCenterJOD)
	if target <= 0 || tolerance <= 0 {
		return nil, nil, fmt.Errorf("no target-quality band to measure against")
	}
	scorer, err := quality.NewChunkScorer(grainStage2Metric, width, height, in.Info, in.DisplayPath)
	if err != nil {
		return nil, nil, err
	}
	measure := func(ctx context.Context, ch chunk.Chunk) (float64, int, error) {
		return measureTargetDeliveredBPP(ctx, gateCfg, in, ch, dir, width, height, target, tolerance, scorer)
	}
	return measure, func() { _ = scorer.Close() }, nil
}

// measureTargetDeliveredBPP walks a short fixed-CRF ladder until a probe scores
// inside the target band, or until two probes bracket the target and the size
// at it can be interpolated. Probes run through the same encoder as the real
// chunks, level cap included, so the bits they report are bits that would
// actually be delivered.
func measureTargetDeliveredBPP(ctx context.Context, gateCfg *EncodeConfig, in GrainGateInput, ch chunk.Chunk, dir string, width, height uint32, target, tolerance float32, scorer quality.ChunkScorer) (float64, int, error) {
	return measureTargetDeliveredBPPWithMeasure(ctx, gateCfg, in, ch, dir, width, height, target, tolerance, scorer, measureChunkBPP)
}

func measureTargetDeliveredBPPWithMeasure(ctx context.Context, gateCfg *EncodeConfig, in GrainGateInput, ch chunk.Chunk, dir string, width, height uint32, target, tolerance float32, scorer quality.ChunkScorer, measure grainBPPMeasure) (float64, int, error) {
	probeCfg := *gateCfg
	crf := grainStage2StartCRF
	var probes []stage2Probe
	for len(probes) < grainStage2MaxProbes {
		probeCfg.CRF = crf
		path := filepath.Join(dir, fmt.Sprintf("tq-%04d-%s.ivf", ch.Idx, quality.FormatCRF(crf)))
		bpp, err := measure(ctx, &probeCfg, in, ch, path, width, height)
		if err != nil {
			return 0, len(probes), err
		}
		// The reference is the unfiltered source: stage 2 measures what the
		// untreated encode delivers, which is the thing being classified.
		score, _, scoreErr := scorer.ScoreChunk(ctx, quality.ChunkScoreRequest{
			SourcePath: in.InputPath,
			ProbePath:  path,
			Info:       in.Info,
			Chunk:      ch,
			CropRect:   in.CropRect,
			Width:      width,
			Height:     height,
		})
		// The probe was a measurement, not output; its bits are already read.
		_ = os.Remove(path)
		if scoreErr != nil {
			return 0, len(probes), scoreErr
		}
		probes = append(probes, stage2Probe{crf: crf, score: score, bpp: bpp})
		if in.Verbose != nil {
			in.Verbose(fmt.Sprintf("Grain gate stage 2 probe chunk=%04d crf=%s score=%.4f bpp=%.4f", ch.Idx, quality.FormatCRF(crf), score, bpp))
		}
		if abs32(score-target) <= tolerance {
			return bpp, len(probes), nil
		}
		next, ok := nextGrainStage2CRF(probes, target)
		if !ok {
			break
		}
		crf = next
	}
	if bpp, ok := interpolateDeliveredBPP(probes, target); ok {
		return bpp, len(probes), nil
	}
	// Out of probes without reaching or bracketing the target: the closest
	// probe is the honest answer available, and the median over twelve samples
	// absorbs a rung or two of slack.
	return closestStage2Probe(probes, target).bpp, len(probes), nil
}

// nextGrainStage2CRF steps toward the target on the metric's slope: the
// default until two probes measure one, then the measured slope when it is
// credible enough for the search to admit it. Reports false when the step
// lands on a CRF already probed or outside the search range, which ends the
// ladder early rather than spending a probe on a repeat.
func nextGrainStage2CRF(probes []stage2Probe, target float32) (float32, bool) {
	last := probes[len(probes)-1]
	step := (last.score - target) / grainStage2Slope(probes)
	if step > grainStage2MaxCRFStep {
		step = grainStage2MaxCRFStep
	}
	if step < -grainStage2MaxCRFStep {
		step = -grainStage2MaxCRFStep
	}
	next := quality.RoundCRFToQuarter(last.crf + step)
	if next < quality.DefaultSearchMin {
		next = quality.DefaultSearchMin
	}
	if next > quality.DefaultSearchMax {
		next = quality.DefaultSearchMax
	}
	for _, p := range probes {
		if p.crf == next {
			return 0, false
		}
	}
	return next, true
}

func grainStage2Slope(probes []stage2Probe) float32 {
	slope := grainStage2Metric.DefaultSlopePerCRF()
	if len(probes) < 2 {
		return slope
	}
	prev, last := probes[len(probes)-2], probes[len(probes)-1]
	crfDelta := last.crf - prev.crf
	if crfDelta == 0 {
		return slope
	}
	measured := (prev.score - last.score) / crfDelta
	if minSlope, maxSlope := grainStage2Metric.SlopeClamp(); measured >= minSlope && measured <= maxSlope {
		return measured
	}
	return slope
}

// interpolateDeliveredBPP returns the bits the chunk would cost at the target
// from the adjacent probe pair whose scores bracket it: the CRF at the target
// is linear in score between two nearby probes, and size is linear in CRF over
// so short a span. Reports false when no pair brackets the target.
func interpolateDeliveredBPP(probes []stage2Probe, target float32) (float64, bool) {
	sorted := append([]stage2Probe(nil), probes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].score < sorted[j].score })
	for i := 0; i+1 < len(sorted); i++ {
		lo, hi := sorted[i], sorted[i+1]
		if lo.score > target || hi.score < target || hi.score == lo.score {
			continue
		}
		t := float64((target - lo.score) / (hi.score - lo.score))
		return lo.bpp + t*(hi.bpp-lo.bpp), true
	}
	return 0, false
}

func closestStage2Probe(probes []stage2Probe, target float32) stage2Probe {
	best := probes[0]
	for _, p := range probes[1:] {
		if abs32(p.score-target) < abs32(best.score-target) {
			best = p
		}
	}
	return best
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// selectGrainSampleChunks picks evenly spaced chunks that lie entirely within
// the title's middle and are long enough to measure. Short titles fall back to
// any long-enough chunk, and give up rather than measure noise.
func selectGrainSampleChunks(chunks []chunk.Chunk, frames int) []chunk.Chunk {
	margin := int(float64(frames) * (1 - grainGateMiddleFraction) / 2)
	low, high := margin, frames-margin
	eligible := longChunksWithin(chunks, low, high)
	if len(eligible) == 0 {
		eligible = longChunksWithin(chunks, 0, frames)
	}
	if len(eligible) == 0 {
		return nil
	}
	count := min(grainGateSamples, len(eligible))
	if count == 1 {
		return []chunk.Chunk{eligible[len(eligible)/2]}
	}
	samples := make([]chunk.Chunk, 0, count)
	for i := 0; i < count; i++ {
		samples = append(samples, eligible[i*(len(eligible)-1)/(count-1)])
	}
	return samples
}

func longChunksWithin(chunks []chunk.Chunk, low, high int) []chunk.Chunk {
	var out []chunk.Chunk
	for _, ch := range chunks {
		if ch.Start >= low && ch.End <= high && ch.Frames() >= grainGateMinChunkFrames {
			out = append(out, ch)
		}
	}
	return out
}

func grainTreats(medianBPP, cutoff float64) bool {
	return cutoff > 0 && medianBPP >= cutoff
}

func bppCutoffs(width uint32) (ambiguous, treat float64) {
	switch resolutionClass(width) {
	case "uhd":
		return uhdAmbiguousBPP, uhdLightBPP
	case "hd":
		return hdAmbiguousBPP, hdLightBPP
	default:
		return 0, 0
	}
}

func resolutionClass(width uint32) string {
	switch {
	case width >= config.UHDWidthThreshold:
		return "uhd"
	case width >= config.HDWidthThreshold:
		return "hd"
	default:
		return "sd"
	}
}

// GrainTreatmentSummary is the human-readable verdict for the Encoding
// section: what was measured and what it bought.
func GrainTreatmentSummary(stats *perf.GrainTreatmentStats) []string {
	if stats == nil {
		return nil
	}
	switch stats.Mode {
	case grainModeOverride:
		return []string{"Grain treatment is off because an explicit denoise or film grain table was given."}
	case config.GrainTreatmentOff:
		return []string{"Grain treatment is off."}
	}
	if len(stats.SampleBPP) == 0 {
		return []string{fmt.Sprintf("Grain gate did not run (%s); encoding the source untreated.", stats.Reason)}
	}

	lines := []string{fmt.Sprintf("Grain gate measured %.4f bits per pixel across %d sample chunks at CRF %s (treat above %.4f).",
		stats.MedianBPP, len(stats.SampleBPP), quality.FormatCRF(float32(stats.GateCRF)), stats.LightBPPCutoff)}
	switch {
	case stats.GateStage == grainStageTQProbe:
		lines = append(lines, fmt.Sprintf("That is inside the ambiguous band (%.4f to %.4f), where the bits a fixed CRF spends say as much about how dark a title is as how grainy, so the same samples were re-encoded to the quality target: there they cost %.4f bits per pixel.",
			stats.AmbiguousBPPCutoff, stats.LightBPPCutoff, stats.Stage2MedianBPP))
	case stats.Stage2Error != "":
		lines = append(lines, fmt.Sprintf("Measuring what the samples cost at the quality target would have decided this one, but it did not run (%s), so the fixed-CRF measurement stands.", stats.Stage2Error))
	}
	if !stats.Treated {
		lines[len(lines)-1] += " This title is clean, so it encodes untreated."
		return lines
	}
	treated := fmt.Sprintf("This title is grainy, so it encodes denoised with %s and source-matched film grain.", stats.Denoise)
	if e := stats.Estimation; e != nil {
		treated += fmt.Sprintf(" Grain was estimated from %d patches across %d frames in %.2fs.", e.Patches, len(e.Frames), e.Seconds)
	}
	if stats.DenoiseCeilingJODMean != nil && stats.DenoiseCeilingJODMin != nil {
		treated += fmt.Sprintf(" Denoising itself costs quality: measured against the real source the samples top out at %.2f JOD on average and %.2f at worst.",
			*stats.DenoiseCeilingJODMean, *stats.DenoiseCeilingJODMin)
	}
	return append(lines, treated)
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// mbpsFromBPP restates a bits-per-pixel measurement as the bitrate it would be
// at this title's geometry and frame rate, which is the unit the study and the
// level cap are quoted in.
func mbpsFromBPP(bpp float64, width, height uint32, inf *video.Info) float64 {
	if inf == nil || inf.FPSDen == 0 {
		return 0
	}
	fps := float64(inf.FPSNum) / float64(inf.FPSDen)
	return bpp * float64(width) * float64(height) * fps / 1e6
}
