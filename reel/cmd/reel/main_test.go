package main

import (
	"testing"

	"github.com/five82/reel/internal/config"
)

func TestParseCRFQuarterSteps(t *testing.T) {
	cfg := config.NewConfig("/input", "/output", "/log")
	if err := parseCRF("24.25,26.5,27.75", cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.CRFSD != 24.25 || cfg.CRFHD != 26.5 || cfg.CRFUHD != 27.75 {
		t.Fatalf("unexpected CRFs: %g %g %g", cfg.CRFSD, cfg.CRFHD, cfg.CRFUHD)
	}
}

func TestParseCRFRejectsNonQuarter(t *testing.T) {
	cfg := config.NewConfig("/input", "/output", "/log")
	if err := parseCRF("24.3", cfg); err == nil {
		t.Fatal("parseCRF accepted non-quarter CRF")
	}
}
