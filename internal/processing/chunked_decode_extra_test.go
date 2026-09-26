package processing

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/config"
	"github.com/five82/reel/internal/media"
	"github.com/five82/reel/internal/perf"
	"github.com/five82/reel/internal/reporter"
	"github.com/five82/reel/internal/video"
)

func shortY4M(t *testing.T, dir string) string {
	t.Helper()
	input := filepath.Join(dir, "input.y4m")
	data := []byte("YUV4MPEG2 W32 H32 F25:1 Ip A1:1 C420\n")
	for i := 0; i < 6; i++ {
		data = append(data, []byte("FRAME\n")...)
		for j := 0; j < 32*32*3/2; j++ {
			data = append(data, byte(40+i))
		}
	}
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	return input
}

func TestProcessVideosReportsBadFilter(t *testing.T) {
	dir := t.TempDir()
	input := shortY4M(t, dir)
	cfg := config.NewConfig("", "", "")
	cfg.TempDir = dir
	cfg.OutputDir = dir
	cfg.QualityMode = config.QualityModeCRF
	cfg.CropMode = "none"
	cfg.Denoise = "nonexistent_filter"
	cfg.KeepWorkDir = true
	results, err := ProcessVideos(context.Background(), cfg, []string{input}, filepath.Join(dir, "output.mkv"), reporter.NullReporter{})
	if err != nil || len(results) != 0 {
		t.Fatalf("failed encode: %+v, %v", results, err)
	}
}

func TestProcessChunkedBadFilterPreservesResumeState(t *testing.T) {
	dir := t.TempDir()
	input := shortY4M(t, dir)
	info, err := video.Probe(input)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.NewConfig("", "", "")
	cfg.TempDir = dir
	cfg.QualityMode = config.QualityModeCRF
	cfg.CropMode = "none"
	cfg.Denoise = "nonexistent_filter"
	cfg.KeepWorkDir = true
	cfg.MetricWorkers = 1
	props := &media.VideoProperties{Width: 32, Height: 32, DurationSecs: 0.24}
	output := filepath.Join(dir, "output.mkv")
	_, err = ProcessChunked(context.Background(), cfg, input, output, props, info, nil, 30, reporter.NullReporter{}, perf.New())
	if err == nil || !strings.Contains(err.Error(), "filter") {
		t.Fatalf("expected bad filter error, got %v", err)
	}
	if _, err := os.Stat(output); err == nil {
		t.Fatal("unexpected output on encode failure")
	}
}
