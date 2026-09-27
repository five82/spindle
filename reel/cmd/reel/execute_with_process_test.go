package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/config"
	"github.com/five82/reel/internal/processing"
	"github.com/five82/reel/internal/reporter"
)

func TestExecuteEncodeAppliesOptionsAndDispatchesWithoutVideoEncode(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "source.y4m")
	if err := os.WriteFile(input, []byte("source identity"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "output.mkv")
	sentinel := errors.New("synthetic process error")
	ea := encodeArgs{
		inputPath: input, outputDir: target, logDir: filepath.Join(dir, "logs"), noLog: true,
		grainTreatment: config.GrainTreatmentOff, qualityModeSet: true, qualityMode: " CRF ",
		crf: "27", preset: 12, parallelism: 2, disableAutocrop: true, probeMetric: " auto ",
		fgsTable: "  /model.tbl  ", denoise: "  hflip  ", keepWorkDir: true, colorMode: "never",
	}
	called := false
	err := executeEncodeWithProcess(ea, func(ctx context.Context, cfg *config.Config, files []string, filename string, rep reporter.Reporter) ([]processing.EncodeResult, error) {
		called = true
		if ctx.Err() != nil || rep == nil || len(files) != 1 || files[0] != input || filename != "output.mkv" {
			t.Errorf("dispatch files %v, filename %q, ctx %v", files, filename, ctx.Err())
		}
		if cfg.QualityMode != config.QualityModeCRF || cfg.CRFSD != 27 || cfg.SVTAV1Preset != 12 || cfg.SVTAV1LevelOfParallelism != 2 || cfg.CropMode != "none" || cfg.GrainTable != "/model.tbl" || cfg.Denoise != "hflip" || !cfg.KeepWorkDir {
			t.Errorf("CLI settings: %+v", cfg)
		}
		return nil, sentinel
	})
	if !called || !errors.Is(err, sentinel) {
		t.Fatalf("dispatch called=%v error=%v", called, err)
	}
}

func TestExecuteEncodeDiscoversDirectoryBeforeDispatch(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "empty")
	if err := os.Mkdir(input, 0700); err != nil {
		t.Fatal(err)
	}
	ea := encodeArgs{inputPath: input, outputDir: filepath.Join(dir, "out"), logDir: filepath.Join(dir, "logs"), noLog: true, grainTreatment: config.GrainTreatmentAuto}
	err := executeEncodeWithProcess(ea, func(context.Context, *config.Config, []string, string, reporter.Reporter) ([]processing.EncodeResult, error) {
		t.Fatal("dispatched empty input directory")
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "no video files") {
		t.Fatalf("empty directory error: %v", err)
	}
}
