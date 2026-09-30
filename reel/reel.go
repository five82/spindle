// Package reel provides a Go library for AV1 video encoding with SVT-AV1.
//
// Reel is an opinionated AV1 encoder that handles the complexity of
// video encoding with sensible defaults, automatic crop detection, HDR metadata
// preservation, and post-encode validation.
//
// Basic usage:
//
//	encoder, err := reel.New(reel.WithLogger(slog.Default()))
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	result, err := encoder.Encode(ctx, "input.mkv", "output/", nil)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	fmt.Printf("Encoded: %s, reduction: %.1f%%\n",
//	    result.OutputFile, result.SizeReductionPercent)
package reel

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/five82/spindle/reel/internal/config"
	"github.com/five82/spindle/reel/internal/perf"
	"github.com/five82/spindle/reel/internal/processing"
	"github.com/five82/spindle/reel/internal/reporter"
	"github.com/five82/spindle/reel/internal/util"
)

// Encoder is the main entry point for video encoding.
type Encoder struct {
	config *config.Config
}

// Result contains the result of a single file encode.
type Result struct {
	OutputFile                string
	OriginalSize              uint64
	EncodedSize               uint64
	SizeReductionPercent      float64
	VideoOriginalSize         uint64
	VideoEncodedSize          uint64
	VideoSizeReductionPercent float64
	ValidationPassed          bool
	EncodingSpeed             float32
	Stats                     *EncodeStats
}

// EncodeStats is the structured performance summary of one encode: source
// facts (dimensions, HDR, duration, frames, chunks), total and per-phase wall
// times, a worker-saturation summary, and (in target-quality mode) the
// aggregate CRF search outcome per metric.
type EncodeStats = perf.Report

// TargetQualityStats summarizes a target-quality run's CRF search.
type TargetQualityStats = perf.TargetQualityStats

// GrainTreatmentStats records the grain-treatment gate's verdict: what the
// title's bits-at-CRF measured, what the sample chunks cost at the quality
// target when that measurement was ambiguous, whether the title was denoised
// with a film grain table, and the honest denoise ceiling the reported scores
// sit under.
type GrainTreatmentStats = perf.GrainTreatmentStats

// GrainEstimationStats describes the sampled source-matched film grain model.
type GrainEstimationStats = perf.GrainEstimationStats

// TargetQualityMetricStats aggregates the CRF search outcomes for the chunks
// scored with one metric.
type TargetQualityMetricStats = perf.TargetQualityMetricStats

// EncodePhase is the wall-clock window of one pipeline phase.
type EncodePhase = perf.Phase

// WorkerSummary condenses the adaptive encode scheduler's worker history.
type WorkerSummary = perf.WorkerSummary

// Option configures the encoder.
type Option func(*config.Config)

func resultFromEncodeResult(outputFile string, r processing.EncodeResult) Result {
	return Result{
		OutputFile:                outputFile,
		OriginalSize:              r.InputSize,
		EncodedSize:               r.OutputSize,
		SizeReductionPercent:      util.CalculateSizeReduction(r.InputSize, r.OutputSize),
		VideoOriginalSize:         r.InputVideoSize,
		VideoEncodedSize:          r.OutputVideoSize,
		VideoSizeReductionPercent: util.CalculateSizeReduction(r.InputVideoSize, r.OutputVideoSize),
		ValidationPassed:          r.ValidationPassed,
		EncodingSpeed:             r.EncodingSpeed,
		Stats:                     r.Stats,
	}
}

// New creates a new Encoder with the given options.
func New(opts ...Option) (*Encoder, error) {
	cfg := config.NewConfig(".", ".", ".")

	for _, opt := range opts {
		opt(cfg)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &Encoder{config: cfg}, nil
}

// WithQualityMode selects target-quality (default) or fixed-CRF mode.
func WithQualityMode(mode string) Option {
	return func(c *config.Config) {
		c.QualityMode = mode
	}
}

// WithLogger routes Reel's diagnostics and decisions to logger as
// structured records; without it they are discarded.
func WithLogger(logger *slog.Logger) Option {
	return func(c *config.Config) {
		c.Logger = logger
	}
}

// Encode encodes a single video file, reporting progress to rep (nil
// discards it).
func (e *Encoder) Encode(ctx context.Context, input, outputDir string, rep Reporter) (*Result, error) {
	// Update config paths
	cfg := *e.config
	cfg.OutputDir = outputDir

	// Ensure output directory exists
	if err := util.EnsureDirectory(outputDir); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Use provided reporter or null reporter
	if rep == nil {
		rep = reporter.NullReporter{}
	}

	// Process single file
	results, err := processing.ProcessVideos(ctx, &cfg, []string{input}, "", rep)
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no files were encoded")
	}

	r := resultFromEncodeResult(util.ResolveOutputPath(input, outputDir, ""), results[0])
	return &r, nil
}
