package processing

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/config"
	"github.com/five82/spindle/reel/internal/media"
	"github.com/five82/spindle/reel/internal/perf"
	"github.com/five82/spindle/reel/internal/reporter"
	"github.com/five82/spindle/reel/internal/video"
)

func TestProcessVideosSkipsMissingAndExistingOutput(t *testing.T) {
	dir := t.TempDir()
	cfg := config.NewConfig("", "", "")
	missing := filepath.Join(dir, "missing.mkv")
	results, err := ProcessVideos(context.Background(), cfg, []string{missing, missing}, "", nil)
	if err != nil || len(results) != 0 {
		t.Fatalf("missing input: %v, %+v", err, results)
	}
	output := filepath.Join(dir, "already.mkv")
	if err := os.WriteFile(output, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	results, err = ProcessVideos(context.Background(), cfg, []string{missing}, output, nil)
	if err != nil || len(results) != 0 {
		t.Fatalf("existing output: %v, %+v", err, results)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err = ProcessVideos(ctx, cfg, []string{missing}, "", nil)
	if err != nil || len(results) != 0 {
		t.Fatalf("cancelled: %v, %+v", err, results)
	}
}

func TestProcessChunkedReportsShotDetectionFailure(t *testing.T) {
	cfg := config.NewConfig("", "", "")
	cfg.QualityMode = config.QualityModeCRF
	cfg.CropMode = "none"
	cfg.KeepWorkDir = true
	cfg.MetricWorkers = 1
	dir := t.TempDir()
	input := filepath.Join(dir, "missing.mkv")
	_, err := ProcessChunked(context.Background(), cfg, input, filepath.Join(dir, "output.mkv"), &media.VideoProperties{Width: 16, Height: 16}, &video.Info{Width: 16, Height: 16, Frames: 24, FPSNum: 24, FPSDen: 1}, nil, 30, reporter.NullReporter{}, perf.New())
	if err == nil || !strings.Contains(err.Error(), "shot cut detection failed") {
		t.Fatalf("pipeline error = %v", err)
	}
}

func TestDetectCropWithUnavailableInput(t *testing.T) {
	if r := DetectCrop("missing", nil, true); r.Required || r.Message != "Skipped" {
		t.Fatalf("disabled crop = %+v", r)
	}
	if r := DetectCrop("", nil, false); r.Required || r.Message != "No crop detected" {
		t.Fatalf("missing info = %+v", r)
	}
	inf := &video.Info{Width: 16, Height: 16, Frames: 100}
	r := DetectCrop("/does/not/exist", inf, false)
	if r.Required || !strings.Contains(r.Message, "samples") {
		t.Fatalf("missing source = %+v", r)
	}
	for _, tc := range []struct {
		filter string
		w, h   uint32
	}{
		{"", 1920, 1080}, {"crop=1280:720:0:0", 1280, 720}, {"crop=bad:720", 1920, 1080}, {"crop=1280:bad", 1920, 1080},
	} {
		w, h := GetOutputDimensions(1920, 1080, tc.filter)
		if w != tc.w || h != tc.h {
			t.Errorf("dimensions for %q = %dx%d", tc.filter, w, h)
		}
	}
}
