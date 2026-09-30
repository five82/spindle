package reel

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/config"
	"github.com/five82/spindle/reel/internal/processing"
	"github.com/five82/spindle/reel/internal/quality"
)

func TestNewOptions(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	e, err := New(WithQualityMode(config.QualityModeCRF), WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	if e.config.QualityMode != config.QualityModeCRF || e.config.Logger != logger {
		t.Errorf("unexpected config: %+v", e.config)
	}
	_, err = New(WithQualityMode(config.QualityModeTarget))
	if !quality.VshipBuildEnabled() {
		if err == nil || !strings.Contains(err.Error(), "not available in no_vship builds") {
			t.Fatalf("target quality without VSHIP: %v", err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if _, err := New(WithQualityMode("bogus")); err == nil {
		t.Error("invalid quality mode accepted")
	}
}

func TestResultFromEncodeResult(t *testing.T) {
	r := resultFromEncodeResult("out.mkv", processing.EncodeResult{InputSize: 100, OutputSize: 50, InputVideoSize: 80, OutputVideoSize: 20, ValidationPassed: true, EncodingSpeed: 2})
	if r.OutputFile != "out.mkv" || r.SizeReductionPercent != 50 || r.VideoSizeReductionPercent != 75 || !r.ValidationPassed || r.EncodingSpeed != 2 {
		t.Errorf("result = %+v", r)
	}
}
