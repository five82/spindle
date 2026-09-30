package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/config"
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

func TestSetupLoggingWritesStructuredRunLog(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if got, want := defaultLogDir(), filepath.Join(os.Getenv("XDG_STATE_HOME"), "reel", "logs"); got != want {
		t.Errorf("defaultLogDir() = %q, want %q", got, want)
	}
	if _, path, closeLog, err := setupLogging(t.TempDir(), false, true); err != nil || path != "" {
		t.Fatalf("no-log setup: path %q, err %v", path, err)
	} else {
		closeLog()
	}
	dir := filepath.Join(t.TempDir(), "nested")
	logger, path, closeLog, err := setupLogging(dir, false, false)
	if err != nil || filepath.Dir(path) != dir {
		t.Fatalf("setup: path %q, err %v", path, err)
	}
	logger.Debug("hidden without verbose")
	logger.Info("grain treatment decided", "decision_type", "grain_treatment")
	closeLog()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if !strings.Contains(log, `msg="reel encoder starting"`) || !strings.Contains(log, "decision_type=grain_treatment") || strings.Contains(log, "hidden") {
		t.Fatalf("run log:\n%s", log)
	}
}
