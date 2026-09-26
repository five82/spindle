package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/config"
	"github.com/five82/reel/internal/quality"
)

func TestRunEncodeArgumentErrors(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "missing.mkv")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no input", nil, "input path is required"},
		{"no output", []string{"-i", input}, "output directory is required"},
		{"missing input", []string{"-i", input, "-o", filepath.Join(dir, "out")}, "input path does not exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := runEncode(tt.args); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("runEncode(%v) = %v, want %q", tt.args, err, tt.want)
			}
		})
	}
}

func TestExecuteEncodeRejectsInvalidSettingsBeforeProcessing(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "source.mkv")
	if err := os.WriteFile(input, nil, 0600); err != nil {
		t.Fatal(err)
	}
	base := encodeArgs{inputPath: input, outputDir: filepath.Join(dir, "out"), logDir: filepath.Join(dir, "logs"), noLog: true, grainTreatment: config.GrainTreatmentAuto}
	tests := []struct {
		name   string
		change func(*encodeArgs)
		want   string
	}{
		{"probe metric", func(e *encodeArgs) { e.probeMetric = "invalid" }, "invalid --probe-metric"},
		{"crf", func(e *encodeArgs) { e.crf = "not-a-crf" }, "invalid CRF"},
		{"preset", func(e *encodeArgs) { e.preset = 14 }, "invalid configuration"},
		{"parallelism", func(e *encodeArgs) { e.parallelism = 7 }, "invalid configuration"},
		{"quality mode", func(e *encodeArgs) { e.qualityModeSet = true; e.qualityMode = "not-a-mode" }, "invalid configuration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ea := base
			tt.change(&ea)
			if err := executeEncode(ea); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("executeEncode() = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestTargetQualityOptionsRequested(t *testing.T) {
	for _, ea := range []encodeArgs{{targetQuality: "8-9"}, {crfSearchRange: "20-40"}, {cvvdpDisplay: "display.json"}, {metricWorkers: 2}, {maxProbes: 2}, {qualityModeSet: true, qualityMode: " TARGET "}} {
		if !targetQualityOptionsRequested(ea) {
			t.Fatalf("missed target quality option: %+v", ea)
		}
	}
	for _, ea := range []encodeArgs{{}, {qualityMode: "target"}, {qualityModeSet: true, qualityMode: "crf"}} {
		if targetQualityOptionsRequested(ea) {
			t.Fatalf("unexpected target quality option: %+v", ea)
		}
	}
	if quality.VshipBuildEnabled() && qualityModeDefaultHelp() != "target" {
		t.Fatal("default help should advertise target mode")
	}
}

func TestResolveOutputPath(t *testing.T) {
	dir := t.TempDir()
	for _, tt := range []struct {
		path              string
		isDir             bool
		wantDir, wantFile string
	}{
		{filepath.Join(dir, "movie.mkv"), false, dir, "movie.mkv"},
		{filepath.Join(dir, "output"), false, filepath.Join(dir, "output"), ""},
		{filepath.Join(dir, "movie.mkv"), true, filepath.Join(dir, "movie.mkv"), ""},
	} {
		gotDir, gotFile, err := resolveOutputPath("", tt.path, tt.isDir)
		if err != nil || gotDir != tt.wantDir || gotFile != tt.wantFile {
			t.Fatalf("resolveOutputPath(%q, %v) = %q, %q, %v", tt.path, tt.isDir, gotDir, gotFile, err)
		}
	}
}

func TestParseCRFFormsAndErrors(t *testing.T) {
	for _, tt := range []struct {
		value   string
		want    [3]float32
		errPart string
	}{
		{"25.25", [3]float32{25.25, 25.25, 25.25}, ""},
		{"24, 26.25, 28", [3]float32{24, 26.25, 28}, ""},
		{"24,invalid,28", [3]float32{}, "position 2"},
		{"24,26", [3]float32{}, "single value or comma-separated triple"},
	} {
		cfg := config.NewConfig("/input", "/output", "/log")
		err := parseCRF(tt.value, cfg)
		if tt.errPart != "" {
			if err == nil || !strings.Contains(err.Error(), tt.errPart) {
				t.Fatalf("parseCRF(%q) = %v", tt.value, err)
			}
			continue
		}
		if err != nil || [3]float32{cfg.CRFSD, cfg.CRFHD, cfg.CRFUHD} != tt.want {
			t.Fatalf("parseCRF(%q) = %v, %g/%g/%g", tt.value, err, cfg.CRFSD, cfg.CRFHD, cfg.CRFUHD)
		}
	}
}
