package config

import (
	"testing"

	"github.com/five82/reel/internal/quality"
)

func TestNewConfig(t *testing.T) {
	cfg := NewConfig("/input", "/output", "/log")

	if cfg.InputDir != "/input" {
		t.Errorf("expected InputDir=/input, got %s", cfg.InputDir)
	}
	if cfg.OutputDir != "/output" {
		t.Errorf("expected OutputDir=/output, got %s", cfg.OutputDir)
	}
	if cfg.LogDir != "/log" {
		t.Errorf("expected LogDir=/log, got %s", cfg.LogDir)
	}

	// Check defaults
	if cfg.SVTAV1Preset != DefaultSVTAV1Preset {
		t.Errorf("expected SVTAV1Preset=%d, got %d", DefaultSVTAV1Preset, cfg.SVTAV1Preset)
	}
	if cfg.CRFSD != DefaultCRFSD {
		t.Errorf("expected CRFSD=%g, got %g", DefaultCRFSD, cfg.CRFSD)
	}
	if cfg.CRFHD != DefaultCRFHD {
		t.Errorf("expected CRFHD=%g, got %g", DefaultCRFHD, cfg.CRFHD)
	}
	if cfg.CRFUHD != DefaultCRFUHD {
		t.Errorf("expected CRFUHD=%g, got %g", DefaultCRFUHD, cfg.CRFUHD)
	}
	wantMode := QualityModeCRF
	if quality.VshipBuildEnabled() {
		wantMode = QualityModeTarget
	}
	if cfg.QualityMode != wantMode {
		t.Errorf("expected default quality mode %q, got %q", wantMode, cfg.QualityMode)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name:    "default config is valid",
			modify:  func(c *Config) {},
			wantErr: false,
		},
		{
			name:    "preset 14 is invalid",
			modify:  func(c *Config) { c.SVTAV1Preset = 14 },
			wantErr: true,
		},
		{
			name:    "preset 13 is valid",
			modify:  func(c *Config) { c.SVTAV1Preset = 13 },
			wantErr: false,
		},
		{
			name:    "crf-sd 70 is valid",
			modify:  func(c *Config) { c.CRFSD = 70 },
			wantErr: false,
		},
		{
			name:    "crf-hd 70.25 is invalid",
			modify:  func(c *Config) { c.CRFHD = 70.25 },
			wantErr: true,
		},
		{
			name:    "crf-uhd non-quarter is invalid",
			modify:  func(c *Config) { c.CRFUHD = 26.1 },
			wantErr: true,
		},
		{
			name:    "quality-mode crf is valid",
			modify:  func(c *Config) { c.QualityMode = QualityModeCRF },
			wantErr: false,
		},
		{
			name:    "invalid target quality is invalid",
			modify:  func(c *Config) { c.TargetQuality = "9-11" },
			wantErr: true,
		},
		{
			name: "target mode is valid only when VSHIP is enabled",
			modify: func(c *Config) {
				c.QualityMode = QualityModeTarget
			},
			wantErr: !quality.VshipBuildEnabled(),
		},
		{
			name:    "invalid search range is invalid",
			modify:  func(c *Config) { c.CRFSearchRange = "4.1-63.75" },
			wantErr: true,
		},
		{
			name:    "metric-workers automatic default is valid",
			modify:  func(c *Config) { c.MetricWorkers = 0 },
			wantErr: false,
		},
		{
			name:    "negative metric-workers is invalid",
			modify:  func(c *Config) { c.MetricWorkers = -1 },
			wantErr: true,
		},
		{
			name:    "chunk_duration_sd 0 is invalid",
			modify:  func(c *Config) { c.ChunkDurationSD = 0 },
			wantErr: true,
		},
		{
			name:    "chunk_duration_hd 121 is invalid",
			modify:  func(c *Config) { c.ChunkDurationHD = 121 },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig("/input", "/output", "/log")
			tt.modify(cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCRFForWidth(t *testing.T) {
	cfg := NewConfig("/input", "/output", "/log")
	cfg.CRFSD = 25
	cfg.CRFHD = 27
	cfg.CRFUHD = 29

	tests := []struct {
		width    uint32
		expected float32
	}{
		{width: 720, expected: 25},  // SD
		{width: 1280, expected: 25}, // SD (720p)
		{width: 1919, expected: 25}, // Just below HD threshold
		{width: 1920, expected: 27}, // HD threshold
		{width: 2560, expected: 27}, // 1440p
		{width: 3839, expected: 27}, // Just below UHD threshold
		{width: 3840, expected: 29}, // UHD threshold
		{width: 7680, expected: 29}, // 8K
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := cfg.CRFForWidth(tt.width)
			if got != tt.expected {
				t.Errorf("CRFForWidth(%d) = %g, want %g", tt.width, got, tt.expected)
			}
		})
	}
}

func TestMetricWorkersForWidth(t *testing.T) {
	cfg := NewConfig("/input", "/output", "/log")

	tests := []struct {
		width    uint32
		expected int
	}{
		{width: 720, expected: DefaultMetricWorkersBelowUHD},
		{width: 1280, expected: DefaultMetricWorkersBelowUHD},
		{width: 1920, expected: DefaultMetricWorkersBelowUHD},
		{width: 3839, expected: DefaultMetricWorkersBelowUHD},
		{width: 3840, expected: DefaultMetricWorkersUHD},
		{width: 7680, expected: DefaultMetricWorkersUHD},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := cfg.MetricWorkersForWidth(tt.width)
			if got != tt.expected {
				t.Errorf("MetricWorkersForWidth(%d) = %d, want %d", tt.width, got, tt.expected)
			}
		})
	}
}

func TestMetricWorkersForWidthUsesExplicitOverride(t *testing.T) {
	cfg := NewConfig("/input", "/output", "/log")
	cfg.MetricWorkers = 5

	for _, width := range []uint32{720, 1920, 3840, 7680} {
		if got := cfg.MetricWorkersForWidth(width); got != 5 {
			t.Errorf("MetricWorkersForWidth(%d) = %d, want explicit override 5", width, got)
		}
	}
}

func TestChunkDurationForWidth(t *testing.T) {
	cfg := NewConfig("/input", "/output", "/log")
	cfg.ChunkDurationSD = 20.0
	cfg.ChunkDurationHD = 30.0
	cfg.ChunkDurationUHD = 45.0

	tests := []struct {
		width    uint32
		expected float64
	}{
		{width: 720, expected: 20.0},  // SD
		{width: 1280, expected: 20.0}, // SD (720p)
		{width: 1919, expected: 20.0}, // Just below HD threshold
		{width: 1920, expected: 30.0}, // HD threshold
		{width: 2560, expected: 30.0}, // 1440p
		{width: 3839, expected: 30.0}, // Just below UHD threshold
		{width: 3840, expected: 45.0}, // UHD threshold
		{width: 7680, expected: 45.0}, // 8K
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := cfg.ChunkDurationForWidth(tt.width)
			if got != tt.expected {
				t.Errorf("ChunkDurationForWidth(%d) = %f, want %f", tt.width, got, tt.expected)
			}
		})
	}
}

func TestTargetQualityChunkDurationForWidthCapsDefaults(t *testing.T) {
	cfg := NewConfig("/input", "/output", "/log")

	tests := []struct {
		width    uint32
		expected float64
	}{
		{width: 1280, expected: DefaultTargetQualityMaxChunkDuration},
		{width: 1920, expected: DefaultTargetQualityMaxChunkDuration},
		{width: 3840, expected: DefaultTargetQualityMaxChunkDuration},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := cfg.TargetQualityChunkDurationForWidth(tt.width)
			if got != tt.expected {
				t.Errorf("TargetQualityChunkDurationForWidth(%d) = %f, want %f", tt.width, got, tt.expected)
			}
		})
	}
}

func TestTargetQualityChunkDurationForWidthKeepsShorterConfig(t *testing.T) {
	cfg := NewConfig("/input", "/output", "/log")
	cfg.ChunkDurationSD = 8.0

	got := cfg.TargetQualityChunkDurationForWidth(1280)
	if got != 8.0 {
		t.Errorf("TargetQualityChunkDurationForWidth() = %f, want 8.0", got)
	}
}

func TestGrainTreatmentValidation(t *testing.T) {
	if cfg := NewConfig("/in", "/out", "/log"); cfg.GrainTreatment != GrainTreatmentAuto {
		t.Errorf("default grain treatment = %q, want %q", cfg.GrainTreatment, GrainTreatmentAuto)
	}

	cfg := NewConfig("/in", "/out", "/log")
	cfg.QualityMode = QualityModeCRF
	cfg.GrainTreatment = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("empty grain treatment should default: %v", err)
	}
	if cfg.GrainTreatment != GrainTreatmentAuto {
		t.Errorf("empty grain treatment = %q, want %q", cfg.GrainTreatment, GrainTreatmentAuto)
	}

	cfg.GrainTreatment = GrainTreatmentOff
	if err := cfg.Validate(); err != nil {
		t.Fatalf("off should validate: %v", err)
	}

	cfg.GrainTreatment = "strong"
	if err := cfg.Validate(); err == nil {
		t.Error("an unknown grain treatment mode should not validate")
	}
}
