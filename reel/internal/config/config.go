// Package config provides configuration types and defaults for reel.
package config

import (
	"fmt"
	"os"

	"github.com/five82/reel/internal/quality"
)

// Default constants
const (
	// DefaultCRFSD is the default fixed-CRF quality setting for SD content (<1920 width).
	DefaultCRFSD float32 = 24

	// DefaultCRFHD is the default fixed-CRF quality setting for HD content (>=1920, <3840 width).
	DefaultCRFHD float32 = 26

	// DefaultCRFUHD is the default fixed-CRF quality setting for UHD content (>=3840 width).
	DefaultCRFUHD float32 = 26

	// Quality mode constants.
	QualityModeTarget = "target"
	QualityModeCRF    = "crf"

	// Grain treatment modes. "auto" lets Reel measure each title and treat
	// only the grainy ones; "off" encodes every title untreated.
	GrainTreatmentAuto = "auto"
	GrainTreatmentOff  = "off"

	// DefaultTargetQuality is the default CVVDP JOD target range (center 9.55,
	// half-width 0.20). The band width is a speed lever, not just an accuracy
	// setting: probe measurement noise is ~0.075 JOD, so a half-width below
	// ~2x that wastes probes landing just outside the band and marching the
	// search to extra probes. At +-0.20 the validated feature converged in
	// roughly 1-2 probes/chunk while staying inside the metric band. Subjective
	// invisibility was not tested. See docs/PERFORMANCE_TESTING.md before
	// changing this quality/speed policy.
	DefaultTargetQuality = "9.35-9.75"

	// DefaultCRFSearchRange is the default target-quality CRF search range.
	DefaultCRFSearchRange = "4.25-63.75"

	// AutoMetricWorkers selects resolution-aware VSHIP/CUDA scoring concurrency.
	// Use MetricWorkersForWidth to get the effective value.
	AutoMetricWorkers = 0

	// DefaultMetricWorkersBelowUHD is the default for content below 4K/UHD.
	// A 2026-06-30 full-scan A/B found 4 workers matched or slightly beat 6 on
	// the HD matrix while using much less VRAM; 8 regressed.
	DefaultMetricWorkersBelowUHD = 4

	// DefaultMetricWorkersUHD is the default for 4K/UHD content. 4K target-quality
	// encodes are encoder-bound under the maxWorkers/6 cap, so extra metric
	// workers add VRAM with little wall-time benefit.
	DefaultMetricWorkersUHD = 4

	// DefaultTargetQualityMaxProbes caps per-chunk target-quality probes.
	DefaultTargetQualityMaxProbes = 6

	// HDWidthThreshold is the minimum width for HD resolution.
	HDWidthThreshold uint32 = 1920

	// UHDWidthThreshold is the minimum width for UHD resolution.
	UHDWidthThreshold uint32 = 3840

	// DefaultSVTAV1Preset is the SVT-AV1 preset (0-13, lower is slower/better).
	// Preset 6 is the measured wall/size knee: faster presets cost too many
	// permanent bits, while slower presets cost too much one-time wall time.
	DefaultSVTAV1Preset uint8 = 6

	// DefaultSVTAV1Tune is the SVT-AV1 tune parameter.
	DefaultSVTAV1Tune uint8 = 0

	// DefaultSVTAV1ACBias is the SVT-AV1 ac-bias parameter.
	DefaultSVTAV1ACBias float32 = 0.1

	// DefaultSVTAV1EnableVarianceBoost is whether variance boost is enabled.
	DefaultSVTAV1EnableVarianceBoost bool = false

	// DefaultSVTAV1VarianceBoostStrength is the variance boost strength.
	DefaultSVTAV1VarianceBoostStrength uint8 = 0

	// DefaultSVTAV1VarianceOctile is the variance octile parameter.
	DefaultSVTAV1VarianceOctile uint8 = 0

	// DefaultCropMode is the crop mode for the main encode.
	DefaultCropMode string = "auto"

	// DefaultEncodeCooldownSecs is the cooldown period between encodes.
	DefaultEncodeCooldownSecs uint64 = 3

	// Chunk duration defaults by resolution.
	// Longer chunks provide better encoder efficiency and reduce concatenation overhead.
	DefaultChunkDurationSD  float64 = 20.0 // SD/720p: faster encode, can use shorter chunks
	DefaultChunkDurationHD  float64 = 30.0 // 1080p: balanced
	DefaultChunkDurationUHD float64 = 45.0 // 4K: slower encode, needs longer warmup

	// DefaultTargetQualityMaxChunkDuration caps chunks in target-quality mode.
	// Each probe scores the whole chunk, so the cap is not about hiding weak
	// segments; it bounds per-probe metric cost and keeps chunk granularity
	// reasonable for parallelism and resume. A 2026-06-30 8-24s sweep found wall
	// flat across the range (metric work ~= total frames x probes/chunk,
	// independent of chunk size) with only small content-dependent size/accuracy
	// effects, so 12s is a balanced default rather than a tuned optimum.
	DefaultTargetQualityMaxChunkDuration float64 = 12.0
)

// Config holds all configuration for video processing.
type Config struct {
	// Input/output paths
	InputDir  string
	OutputDir string
	LogDir    string
	LogFile   string
	TempDir   string // Optional, defaults to OutputDir

	// SVT-AV1 parameters
	SVTAV1Preset                uint8
	SVTAV1Tune                  uint8
	SVTAV1ACBias                float32
	SVTAV1EnableVarianceBoost   bool
	SVTAV1VarianceBoostStrength uint8
	SVTAV1VarianceOctile        uint8
	// SVTAV1LevelOfParallelism overrides SVT-AV1 level_of_parallelism (1-6);
	// 0 lets Reel scale it from the resolution-aware worker target. The value
	// is bitstream-neutral, so it affects throughput only.
	SVTAV1LevelOfParallelism uint32

	// Quality mode and settings.
	QualityMode            string  // "target" or "crf"
	TargetQuality          string  // CVVDP JOD target range, LOW-HIGH
	TargetQualityMin       float32 // Parsed CVVDP target low bound
	TargetQualityMax       float32 // Parsed CVVDP target high bound
	TargetQualityTarget    float32 // Parsed CVVDP target midpoint
	TargetQualityTolerance float32 // Parsed CVVDP tolerance
	CRFSearchRange         string  // Target-quality search bounds, LOW-HIGH
	CRFSearchMin           float32 // Parsed target-quality CRF low bound
	CRFSearchMax           float32 // Parsed target-quality CRF high bound
	CVVDPDisplay           string  // Optional display JSON override
	MetricWorkers          int     // Concurrent VSHIP/CUDA scoring workers; 0 selects resolution-aware default
	TargetQualityMaxProbes int     // Per-chunk target-quality probe cap

	// Fixed-CRF settings by resolution.
	CRFSD  float32 // CRF for SD content (<1920 width)
	CRFHD  float32 // CRF for HD content (>=1920, <3840 width)
	CRFUHD float32 // CRF for UHD content (>=3840 width)

	// Processing options
	CropMode           string // "auto" or "none"
	EncodeCooldownSecs uint64 // Cooldown between batch encodes

	// GrainTreatment is "auto" or "off". In auto, target-quality encodes
	// measure each title's bits at a fixed CRF and encode grainy titles from a
	// denoised source with a film grain table attached, so the decoder
	// re-synthesizes texture instead of the encoder coding noise. Clean titles
	// and fixed-CRF encodes are never treated. See internal/encode/grain.go.
	GrainTreatment string

	// Denoise is an EXPERIMENTAL libavfilter graph string (for example
	// "hqdn3d=2:1.5:3:2.25") applied to every encoder input frame and to every
	// quality-metric reference frame, so target-quality mode scores against the
	// denoised source instead of the original. Empty disables it. Shot-cut and
	// crop detection stay on the unfiltered source.
	Denoise string

	// ProbeMetric is an EXPERIMENTAL override of the automatic target-quality
	// probe metric selection: "" keeps the resolution/HDR-based choice, and
	// "cvvdp" forces CVVDP even for SDR <=1080p sources (for evaluating
	// denoised encodes, where the calibrated SSIMU2 mapping is unproven).
	ProbeMetric string

	// GrainTable is an EXPERIMENTAL path to a libaom "filmgrn1" film grain
	// table attached to every encoded chunk (film grain synthesis at decode
	// time). Encoded pixels, rate, and quality scoring are unaffected: the
	// table only adds frame-header synthesis params, and metric decodes
	// export rather than apply grain. Empty disables it.
	GrainTable string

	// Chunk duration settings by resolution (seconds)
	ChunkDurationSD  float64 // Chunk duration for SD content (<1920 width)
	ChunkDurationHD  float64 // Chunk duration for HD content (>=1920, <3840 width)
	ChunkDurationUHD float64 // Chunk duration for UHD content (>=3840 width)

	// Debug options
	Verbose     bool // Enable verbose output
	KeepWorkDir bool // Keep .reel work directory after successful encodes
}

// NewConfig creates a new Config with default values.
func NewConfig(inputDir, outputDir, logDir string) *Config {
	return &Config{
		InputDir:                    inputDir,
		OutputDir:                   outputDir,
		LogDir:                      logDir,
		SVTAV1Preset:                DefaultSVTAV1Preset,
		SVTAV1Tune:                  DefaultSVTAV1Tune,
		SVTAV1ACBias:                DefaultSVTAV1ACBias,
		SVTAV1EnableVarianceBoost:   DefaultSVTAV1EnableVarianceBoost,
		SVTAV1VarianceBoostStrength: DefaultSVTAV1VarianceBoostStrength,
		SVTAV1VarianceOctile:        DefaultSVTAV1VarianceOctile,
		QualityMode:                 defaultQualityMode(),
		TargetQuality:               DefaultTargetQuality,
		CRFSearchRange:              DefaultCRFSearchRange,
		MetricWorkers:               AutoMetricWorkers,
		TargetQualityMaxProbes:      DefaultTargetQualityMaxProbes,
		CRFSD:                       DefaultCRFSD,
		CRFHD:                       DefaultCRFHD,
		CRFUHD:                      DefaultCRFUHD,
		CropMode:                    DefaultCropMode,
		GrainTreatment:              GrainTreatmentAuto,
		EncodeCooldownSecs:          DefaultEncodeCooldownSecs,
		ChunkDurationSD:             DefaultChunkDurationSD,
		ChunkDurationHD:             DefaultChunkDurationHD,
		ChunkDurationUHD:            DefaultChunkDurationUHD,
	}
}

func defaultQualityMode() string {
	if quality.VshipBuildEnabled() {
		return QualityModeTarget
	}
	return QualityModeCRF
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if c.SVTAV1Preset > 13 {
		return fmt.Errorf("svt_av1_preset must be 0-13, got %d", c.SVTAV1Preset)
	}
	if c.SVTAV1LevelOfParallelism > 6 {
		return fmt.Errorf("level-of-parallelism must be 1-6 when set, got %d", c.SVTAV1LevelOfParallelism)
	}

	switch c.QualityMode {
	case QualityModeTarget, QualityModeCRF:
	case "":
		c.QualityMode = defaultQualityMode()
	default:
		return fmt.Errorf("quality-mode must be %q or %q, got %q", QualityModeTarget, QualityModeCRF, c.QualityMode)
	}
	if c.QualityMode == QualityModeTarget && !quality.VshipBuildEnabled() {
		return fmt.Errorf("quality-mode %q is not available in no_vship builds; rebuild with VSHIP or use %q", QualityModeTarget, QualityModeCRF)
	}

	if err := quality.ValidateCRF(c.CRFSD); err != nil {
		return fmt.Errorf("crf-sd: %w", err)
	}
	if err := quality.ValidateCRF(c.CRFHD); err != nil {
		return fmt.Errorf("crf-hd: %w", err)
	}
	if err := quality.ValidateCRF(c.CRFUHD); err != nil {
		return fmt.Errorf("crf-uhd: %w", err)
	}

	if c.TargetQuality == "" {
		c.TargetQuality = DefaultTargetQuality
	}
	low, high, target, tolerance, err := quality.ParseTargetQualityRange(c.TargetQuality)
	if err != nil {
		return err
	}
	c.TargetQualityMin = low
	c.TargetQualityMax = high
	c.TargetQualityTarget = target
	c.TargetQualityTolerance = tolerance

	if c.CRFSearchRange == "" {
		c.CRFSearchRange = DefaultCRFSearchRange
	}
	searchMin, searchMax, err := quality.ParseCRFSearchRange(c.CRFSearchRange)
	if err != nil {
		return err
	}
	c.CRFSearchMin = searchMin
	c.CRFSearchMax = searchMax

	switch c.GrainTreatment {
	case GrainTreatmentAuto, GrainTreatmentOff:
	case "":
		c.GrainTreatment = GrainTreatmentAuto
	default:
		return fmt.Errorf("grain-treatment must be %q or %q, got %q", GrainTreatmentAuto, GrainTreatmentOff, c.GrainTreatment)
	}

	if c.MetricWorkers < 0 {
		return fmt.Errorf("metric-workers must be >= 1 when set, got %d", c.MetricWorkers)
	}
	if c.TargetQualityMaxProbes < 1 {
		return fmt.Errorf("target-quality-max-probes must be >= 1, got %d", c.TargetQualityMaxProbes)
	}
	if c.CVVDPDisplay != "" {
		if _, err := os.Stat(c.CVVDPDisplay); err != nil {
			return fmt.Errorf("cvvdp-display is not readable: %w", err)
		}
	}

	// Validate chunk durations
	for _, cd := range []struct {
		name  string
		value float64
	}{
		{"chunk_duration_sd", c.ChunkDurationSD},
		{"chunk_duration_hd", c.ChunkDurationHD},
		{"chunk_duration_uhd", c.ChunkDurationUHD},
	} {
		if cd.value < 1 || cd.value > 120 {
			return fmt.Errorf("%s must be between 1 and 120 seconds, got %g", cd.name, cd.value)
		}
	}

	return nil
}

// GetTempDir returns the temp directory, falling back to OutputDir if not set.
func (c *Config) GetTempDir() string {
	if c.TempDir != "" {
		return c.TempDir
	}
	return c.OutputDir
}

// CRFForWidth returns the appropriate CRF value based on video width.
func (c *Config) CRFForWidth(width uint32) float32 {
	if width >= UHDWidthThreshold {
		return c.CRFUHD
	}
	if width >= HDWidthThreshold {
		return c.CRFHD
	}
	return c.CRFSD
}

// DefaultMetricWorkersForWidth returns the default metric worker count based on video width.
func DefaultMetricWorkersForWidth(width uint32) int {
	if width >= UHDWidthThreshold {
		return DefaultMetricWorkersUHD
	}
	return DefaultMetricWorkersBelowUHD
}

// MetricWorkersForWidth returns the effective metric worker count based on video width.
func (c *Config) MetricWorkersForWidth(width uint32) int {
	if c.MetricWorkers > 0 {
		return c.MetricWorkers
	}
	return DefaultMetricWorkersForWidth(width)
}

// ChunkDurationForWidth returns the appropriate chunk duration based on video width.
func (c *Config) ChunkDurationForWidth(width uint32) float64 {
	if width >= UHDWidthThreshold {
		return c.ChunkDurationUHD
	}
	if width >= HDWidthThreshold {
		return c.ChunkDurationHD
	}
	return c.ChunkDurationSD
}

// TargetQualityChunkDurationForWidth returns the chunk duration cap for target-quality mode.
func (c *Config) TargetQualityChunkDurationForWidth(width uint32) float64 {
	return min(c.ChunkDurationForWidth(width), DefaultTargetQualityMaxChunkDuration)
}
