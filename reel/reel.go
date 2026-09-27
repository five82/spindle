// Package reel provides a Go library for AV1 video encoding with SVT-AV1.
//
// Reel is an opinionated AV1 encoder that handles the complexity of
// video encoding with sensible defaults, automatic crop detection, HDR metadata
// preservation, and post-encode validation.
//
// Basic usage:
//
//	encoder, err := reel.New(
//	    reel.WithCRF(26),
//	)
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

	"github.com/five82/reel/internal/config"
	"github.com/five82/reel/internal/discovery"
	"github.com/five82/reel/internal/perf"
	"github.com/five82/reel/internal/processing"
	"github.com/five82/reel/internal/reporter"
	"github.com/five82/reel/internal/util"
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

// BatchResult contains the result of a batch encode.
type BatchResult struct {
	Results               []Result
	SuccessfulCount       int
	TotalFiles            int
	TotalSizeReduction    float64
	ValidationPassedCount int
}

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

type crfValue interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64
}

// WithCRF sets a single fixed CRF value for all resolutions (1-70 in 0.25 steps, lower is better quality).
func WithCRF[T crfValue](crf T) Option {
	return func(c *config.Config) {
		value := float32(crf)
		c.QualityMode = config.QualityModeCRF
		c.CRFSD = value
		c.CRFHD = value
		c.CRFUHD = value
	}
}

// WithCRFByResolution sets resolution-specific fixed CRF values (1-70 in 0.25 steps, lower is better quality).
// SD applies to videos <1920 width, HD to >=1920 and <3840, UHD to >=3840.
func WithCRFByResolution[SD crfValue, HD crfValue, UHD crfValue](sd SD, hd HD, uhd UHD) Option {
	return func(c *config.Config) {
		c.QualityMode = config.QualityModeCRF
		c.CRFSD = float32(sd)
		c.CRFHD = float32(hd)
		c.CRFUHD = float32(uhd)
	}
}

// WithQualityMode selects target-quality (default) or fixed-CRF mode.
func WithQualityMode(mode string) Option {
	return func(c *config.Config) {
		c.QualityMode = mode
	}
}

// WithTargetQuality sets the CVVDP JOD target range for target-quality mode.
func WithTargetQuality(targetRange string) Option {
	return func(c *config.Config) {
		c.TargetQuality = targetRange
	}
}

// WithCVVDPDisplay sets a VSHIP/CVVDP display JSON override for target-quality mode.
func WithCVVDPDisplay(path string) Option {
	return func(c *config.Config) {
		c.CVVDPDisplay = path
	}
}

// WithDisableAutocrop disables automatic black bar detection.
func WithDisableAutocrop() Option {
	return func(c *config.Config) {
		c.CropMode = "none"
	}
}

// EncodeWithReporter encodes a single video file using a custom Reporter.
// This provides direct access to all encoding events, unlike Encode which
// uses the EventHandler abstraction.
func (e *Encoder) EncodeWithReporter(ctx context.Context, input, outputDir string, rep Reporter) (*Result, error) {
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

// Encode encodes a single video file.
func (e *Encoder) Encode(ctx context.Context, input, outputDir string, handler EventHandler) (*Result, error) {
	// Update config paths
	cfg := *e.config
	cfg.OutputDir = outputDir

	// Ensure output directory exists
	if err := util.EnsureDirectory(outputDir); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create reporter
	var rep reporter.Reporter = reporter.NullReporter{}
	if handler != nil {
		rep = newEventReporter(handler)
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

// EncodeBatch encodes multiple video files.
func (e *Encoder) EncodeBatch(ctx context.Context, inputs []string, outputDir string, handler EventHandler) (*BatchResult, error) {
	// Update config paths
	cfg := *e.config
	cfg.OutputDir = outputDir

	// Ensure output directory exists
	if err := util.EnsureDirectory(outputDir); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create reporter
	var rep reporter.Reporter = reporter.NullReporter{}
	if handler != nil {
		rep = newEventReporter(handler)
	}

	// Process files
	results, err := processing.ProcessVideos(ctx, &cfg, inputs, "", rep)
	if err != nil {
		return nil, err
	}

	batch := &BatchResult{
		TotalFiles: len(inputs),
	}

	var totalInputSize, totalOutputSize uint64
	for _, r := range results {
		batch.Results = append(batch.Results, resultFromEncodeResult(util.ResolveOutputPath(r.Filename, outputDir, ""), r))
		batch.SuccessfulCount++
		totalInputSize += r.InputSize
		totalOutputSize += r.OutputSize
		if r.ValidationPassed {
			batch.ValidationPassedCount++
		}
	}

	batch.TotalSizeReduction = util.CalculateSizeReduction(totalInputSize, totalOutputSize)

	return batch, nil
}

// FindVideos finds video files in a directory.
func FindVideos(dir string) ([]string, error) {
	return discovery.FindVideoFiles(dir)
}

// eventReporter adapts EventHandler to the Reporter interface.
type eventReporter struct {
	handler EventHandler
}

func newEventReporter(handler EventHandler) *eventReporter {
	return &eventReporter{handler: handler}
}

func (r *eventReporter) Hardware(reporter.HardwareSummary)             {}
func (r *eventReporter) Initialization(reporter.InitializationSummary) {}
func (r *eventReporter) StageProgress(reporter.StageProgress)          {}
func (r *eventReporter) CropResult(reporter.CropSummary)               {}
func (r *eventReporter) EncodingConfig(reporter.EncodingConfigSummary) {}
func (r *eventReporter) EncodingStarted(uint64)                        {}

func (r *eventReporter) EncodingProgress(p reporter.ProgressSnapshot) {
	_ = r.handler(EncodingProgressEvent{
		BaseEvent:   BaseEvent{EventType: EventTypeEncodingProgress, Time: NewTimestamp()},
		Percent:     p.Percent,
		Speed:       p.Speed,
		RecentSpeed: p.RecentSpeed,
		FPS:         p.FPS,
		ETASeconds:  int64(p.ETA.Seconds()),
	})
}

func (r *eventReporter) ValidationComplete(s reporter.ValidationSummary) {
	steps := make([]ValidationStep, len(s.Steps))
	for i, step := range s.Steps {
		steps[i] = ValidationStep{
			Step:    step.Name,
			Passed:  step.Passed,
			Details: step.Details,
		}
	}
	_ = r.handler(ValidationCompleteEvent{
		BaseEvent:        BaseEvent{EventType: EventTypeValidationComplete, Time: NewTimestamp()},
		ValidationPassed: s.Passed,
		ValidationSteps:  steps,
	})
}

func (r *eventReporter) EncodingComplete(s reporter.EncodingOutcome) {
	_ = r.handler(EncodingCompleteEvent{
		BaseEvent:                 BaseEvent{EventType: EventTypeEncodingComplete, Time: NewTimestamp()},
		OutputFile:                s.OutputFile,
		OriginalSize:              s.OriginalSize,
		EncodedSize:               s.EncodedSize,
		SizeReductionPercent:      util.CalculateSizeReduction(s.OriginalSize, s.EncodedSize),
		VideoOriginalSize:         s.VideoOriginalSize,
		VideoEncodedSize:          s.VideoEncodedSize,
		VideoSizeReductionPercent: util.CalculateSizeReduction(s.VideoOriginalSize, s.VideoEncodedSize),
	})
}

func (r *eventReporter) Warning(message string) {
	_ = r.handler(WarningEvent{
		BaseEvent: BaseEvent{EventType: EventTypeWarning, Time: NewTimestamp()},
		Message:   message,
	})
}

func (r *eventReporter) Error(e reporter.ReporterError) {
	_ = r.handler(ErrorEvent{
		BaseEvent:  BaseEvent{EventType: EventTypeError, Time: NewTimestamp()},
		Title:      e.Title,
		Message:    e.Message,
		Context:    e.Context,
		Suggestion: e.Suggestion,
	})
}

func (r *eventReporter) OperationComplete(string)                  {}
func (r *eventReporter) BatchStarted(reporter.BatchStartInfo)      {}
func (r *eventReporter) FileProgress(reporter.FileProgressContext) {}

func (r *eventReporter) BatchComplete(s reporter.BatchSummary) {
	_ = r.handler(BatchCompleteEvent{
		BaseEvent:                 BaseEvent{EventType: EventTypeBatchComplete, Time: NewTimestamp()},
		SuccessfulCount:           s.SuccessfulCount,
		TotalFiles:                s.TotalFiles,
		TotalSizeReductionPercent: util.CalculateSizeReduction(s.TotalOriginalSize, s.TotalEncodedSize),
	})
}

func (r *eventReporter) Verbose(string) {}
