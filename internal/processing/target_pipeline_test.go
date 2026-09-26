package processing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/config"
	"github.com/five82/reel/internal/encode"
	"github.com/five82/reel/internal/media"
	"github.com/five82/reel/internal/perf"
	"github.com/five82/reel/internal/reporter"
	"github.com/five82/reel/internal/video"
	"github.com/five82/reel/internal/worker"
)

func TestTargetQualityPipelinePreparesGateAndPlanWithoutGPU(t *testing.T) {
	dir := t.TempDir()
	input := shortY4M(t, dir)
	info, err := video.Probe(input)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.NewConfig(input, dir, dir)
	cfg.TempDir = dir
	cfg.QualityMode = config.QualityModeTarget
	cfg.CropMode = "none"
	cfg.KeepWorkDir = true
	props := &media.VideoProperties{Width: 32, Height: 32, DurationSecs: 0.24}
	sentinel := errors.New("synthetic video failure")
	called := false
	output := filepath.Join(dir, "result.mkv")
	_, err = processChunkedWithEncoder(context.Background(), cfg, input, output, props, info, nil, 30, reporter.NullReporter{}, perf.New(),
		func(_ context.Context, chunks []chunk.Chunk, path string, inf *video.Info, enc *encode.EncodeConfig, work string, crop *video.CropRect, progress encode.ProgressCallback, tq encode.TargetQualityConfig) (int, *perf.TargetQualityStats, error) {
			called = true
			if path != input || inf != info || work == "" || crop != nil || len(chunks) == 0 || enc.Denoise != "" || tq.Metric == "" || tq.DisplayPath == "" {
				t.Errorf("target setup: chunks %+v, encode %+v, target %+v", chunks, enc, tq)
			}
			if _, err := os.Stat(tq.DisplayPath); err != nil {
				t.Errorf("display model: %v", err)
			}
			progress(worker.Progress{FramesTotal: inf.Frames, ChunksTotal: len(chunks), FramesComplete: 1, ActiveWorkers: 1})
			return 1, &perf.TargetQualityStats{}, sentinel
		})
	if !called || !errors.Is(err, sentinel) {
		t.Fatalf("target encoder called %v, error %v", called, err)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output on failed encode: %v", err)
	}
}

func TestTargetQualityPipelineReportsMergeFailureAfterSyntheticEncode(t *testing.T) {
	dir := t.TempDir()
	input := shortY4M(t, dir)
	info, err := video.Probe(input)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.NewConfig(input, dir, dir)
	cfg.TempDir = dir
	cfg.QualityMode = config.QualityModeTarget
	cfg.CropMode = "none"
	_, err = processChunkedWithEncoder(context.Background(), cfg, input, filepath.Join(dir, "output.mkv"), &media.VideoProperties{Width: 32, Height: 32, DurationSecs: 0.24}, info, nil, 30, reporter.NullReporter{}, perf.New(),
		func(_ context.Context, chunks []chunk.Chunk, _ string, _ *video.Info, _ *encode.EncodeConfig, _ string, _ *video.CropRect, _ encode.ProgressCallback, _ encode.TargetQualityConfig) (int, *perf.TargetQualityStats, error) {
			if len(chunks) == 0 {
				t.Fatal("missing chunk plan")
			}
			return 1, &perf.TargetQualityStats{}, nil
		})
	if err == nil || !strings.Contains(err.Error(), "video merge failed") {
		t.Fatalf("missing encoded chunks: %v", err)
	}
}

func TestTargetQualityPipelineStopsBeforeScoringOnBadDisplay(t *testing.T) {
	dir := t.TempDir()
	input := shortY4M(t, dir)
	info, err := video.Probe(input)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.NewConfig(input, dir, dir)
	cfg.TempDir = dir
	cfg.QualityMode = config.QualityModeTarget
	cfg.CropMode = "none"
	cfg.CVVDPDisplay = filepath.Join(dir, "missing-display.json")
	_, err = processChunkedWithEncoder(context.Background(), cfg, input, filepath.Join(dir, "output.mkv"), &media.VideoProperties{Width: 32, Height: 32, DurationSecs: 0.24}, info, nil, 30, reporter.NullReporter{}, perf.New(),
		func(context.Context, []chunk.Chunk, string, *video.Info, *encode.EncodeConfig, string, *video.CropRect, encode.ProgressCallback, encode.TargetQualityConfig) (int, *perf.TargetQualityStats, error) {
			t.Fatal("encoded without display")
			return 0, nil, nil
		})
	if err == nil || !strings.Contains(err.Error(), "display") {
		t.Fatalf("display failure: %v", err)
	}
}
