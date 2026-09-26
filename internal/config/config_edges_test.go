package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAdditionalInvalidSettings(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*Config)
	}{
		{"parallelism", "level-of-parallelism", func(c *Config) { c.SVTAV1LevelOfParallelism = 7 }},
		{"quality mode", "quality-mode", func(c *Config) { c.QualityMode = "unknown" }},
		{"SD CRF", "crf-sd", func(c *Config) { c.CRFSD = 0 }},
		{"missing quality display", "cvvdp-display", func(c *Config) { c.CVVDPDisplay = filepath.Join(t.TempDir(), "missing.json") }},
		{"zero probes", "target-quality-max-probes", func(c *Config) { c.TargetQualityMaxProbes = 0 }},
		{"UHD chunk duration", "chunk_duration_uhd", func(c *Config) { c.ChunkDurationUHD = 121 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := NewConfig("/in", "/out", "/log")
			tc.change(cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want %q error", err, tc.want)
			}
		})
	}
}

func TestValidateDefaultsAndExplicitDisplay(t *testing.T) {
	cfg := NewConfig("/in", "/out", "/log")
	cfg.QualityMode = ""
	cfg.TargetQuality = ""
	cfg.CRFSearchRange = ""
	cfg.TempDir = "/tmp/work"
	if got := cfg.GetTempDir(); got != "/tmp/work" {
		t.Fatalf("explicit temp dir = %q", got)
	}
	cfg.TempDir = ""
	if got := cfg.GetTempDir(); got != "/out" {
		t.Fatalf("fallback temp dir = %q", got)
	}
	cfg.CVVDPDisplay = filepath.Join(t.TempDir(), "display.json")
	if err := os.WriteFile(cfg.CVVDPDisplay, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.QualityMode == "" || cfg.TargetQuality != DefaultTargetQuality || cfg.CRFSearchRange != DefaultCRFSearchRange {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}
