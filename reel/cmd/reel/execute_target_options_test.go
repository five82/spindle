package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fatih/color"
	"github.com/five82/spindle/reel/internal/config"
	"github.com/five82/spindle/reel/internal/processing"
	"github.com/five82/spindle/reel/internal/quality"
	"github.com/five82/spindle/reel/internal/reporter"
)

func TestExecuteEncodeForwardsTargetQualityOptions(t *testing.T) {
	if !quality.VshipBuildEnabled() {
		t.Skip("target quality is unavailable in no_vship builds")
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "clip.mkv")
	if err := os.WriteFile(input, []byte("not decoded by this test"), 0600); err != nil {
		t.Fatal(err)
	}
	display := filepath.Join(dir, "display.json")
	if err := os.WriteFile(display, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	oldColor := color.NoColor
	t.Cleanup(func() { color.NoColor = oldColor })
	called := false
	ea := encodeArgs{
		inputPath: input, outputDir: filepath.Join(dir, "out"), logDir: filepath.Join(dir, "logs"),
		qualityModeSet: true, qualityMode: " TARGET ", targetQuality: "8.0-9.0", crfSearchRange: "20-40",
		cvvdpDisplay: display, metricWorkers: 3, maxProbes: 4, probeMetric: " CVVDP ",
		grainTreatment: " OFF ", verbose: true, colorMode: "always",
	}
	err := executeEncodeWithProcess(ea, func(ctx context.Context, cfg *config.Config, files []string, name string, rep reporter.Reporter) ([]processing.EncodeResult, error) {
		called = true
		if cfg.QualityMode != config.QualityModeTarget || cfg.MetricWorkers != 3 || cfg.TargetQualityMaxProbes != 4 || cfg.TargetQuality != "8.0-9.0" || cfg.CRFSearchRange != "20-40" || cfg.CVVDPDisplay != display || cfg.ProbeMetric != "cvvdp" || cfg.GrainTreatment != config.GrainTreatmentOff || !cfg.Verbose || cfg.LogFile == "" {
			t.Errorf("target options not forwarded: %+v", cfg)
		}
		if len(files) != 1 || files[0] != input || name != "" || rep == nil || ctx.Err() != nil || color.NoColor {
			t.Errorf("dispatch files=%v name=%q rep=%v ctx=%v color=%v", files, name, rep, ctx.Err(), color.NoColor)
		}
		return nil, nil
	})
	if err != nil || !called {
		t.Fatalf("dispatch called=%v err=%v", called, err)
	}
}

func TestExecuteEncodeDirectoryDiscoversFiles(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input")
	if err := os.Mkdir(input, 0700); err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(input, "movie.mkv")
	if err := os.WriteFile(clip, nil, 0600); err != nil {
		t.Fatal(err)
	}
	ea := encodeArgs{inputPath: input, outputDir: filepath.Join(dir, "out"), logDir: filepath.Join(dir, "logs"), noLog: true, grainTreatment: "auto", qualityModeSet: true, qualityMode: "crf"}
	err := executeEncodeWithProcess(ea, func(_ context.Context, _ *config.Config, files []string, target string, _ reporter.Reporter) ([]processing.EncodeResult, error) {
		if len(files) != 1 || files[0] != clip || target != "" {
			t.Errorf("discovery = %v, target %q", files, target)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
