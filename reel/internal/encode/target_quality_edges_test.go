package encode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/quality"
	"github.com/five82/spindle/reel/internal/video"
)

func TestTargetQualitySetupFailuresAndEmptyRun(t *testing.T) {
	tq := TargetQualityConfig{Metric: quality.MetricCVVDP, CRFMin: 10, CRFMax: 50}
	cfg := &EncodeConfig{}
	inf := testVideoInfo()
	ch := []chunk.Chunk{{Idx: 0, Start: 0, End: 2}}
	dir := t.TempDir()
	bad := filepath.Join(dir, "file")
	if err := os.WriteFile(bad, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EncodeTargetQuality(context.Background(), ch, "", inf, cfg, bad, nil, nil, tq); err == nil || !strings.Contains(err.Error(), "encode directory") {
		t.Fatalf("encode dir error: %v", err)
	}
	// No chunks left to encode requires neither a GPU nor an input file.
	workers, stats, err := EncodeTargetQuality(context.Background(), nil, "", inf, cfg, dir, nil, nil, tq)
	if err != nil || stats != nil || workers < 1 {
		t.Fatalf("empty run: workers=%d stats=%v err=%v", workers, stats, err)
	}
	if _, _, err := EncodeTargetQuality(context.Background(), ch, "", inf, cfg, dir, &video.CropRect{X: 1, Y: 0, Width: 100, Height: 100}, nil, tq); err == nil || !strings.Contains(err.Error(), "crop") {
		t.Fatalf("invalid crop: %v", err)
	}
}

func TestTargetRunPlanCancellation(t *testing.T) {
	r := newSSIMU2TestRun(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Once the warmup slots are taken, cancellation of the calibration wait
	// must return an error without attempting to encode.
	exhaustWarmupClaims(r.calibration)
	if got := r.processChunk(ctx, chunk.Chunk{Idx: 8}); !errors.Is(got.Error, context.Canceled) {
		t.Fatalf("canceled plan: %+v", got)
	}
}

func TestTargetProbeInvalidInputsAndCopy(t *testing.T) {
	dir := t.TempDir()
	r := newTargetQualityRun(TargetQualityConfig{Metric: quality.MetricCVVDP, CRFMin: 10, CRFMax: 50, InitialCRF: 30, MaxProbes: 1}, &EncodeConfig{}, filepath.Join(dir, "absent"), dir, testVideoInfo(), nil, 1920, 1080, newAdaptiveLimiter(2, 1, 2, 0, nil, nil), 1, nil, nil)
	if _, err := r.encodeAndScoreProbe(context.Background(), chunk.Chunk{Idx: 2}, 30, nil, nil); err == nil || !strings.Contains(err.Error(), "no frames") {
		t.Fatalf("empty chunk: %v", err)
	}
	ch := chunk.Chunk{Idx: 2, Start: 0, End: 1}
	if got := r.encodeProbe(context.Background(), ch, "", 30, nil); got.Error == nil || !strings.Contains(got.Error.Error(), "open source") {
		t.Fatalf("missing source: %+v", got)
	}
	if _, err := r.encodeAndScoreProbe(context.Background(), ch, 30, nil, nil); err == nil || !strings.Contains(err.Error(), "open source") {
		t.Fatalf("missing source probe: %v", err)
	}
	if _, err := probePeakSecondBps(filepath.Join(dir, "absent.ivf"), testVideoInfo()); err == nil || !strings.Contains(err.Error(), "open probe") {
		t.Fatalf("missing IVF: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "invalid.ivf"), []byte("not an IVF"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := probePeakSecondBps(filepath.Join(dir, "invalid.ivf"), testVideoInfo()); err == nil || !strings.Contains(err.Error(), "scan probe") {
		t.Fatalf("invalid IVF: %v", err)
	}
	src := filepath.Join(dir, "source")
	dst := filepath.Join(dir, "nested", "copy")
	if err := os.WriteFile(src, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "content" {
		t.Fatalf("copy: %q, %v", data, err)
	}
	if err := copyFile(filepath.Join(dir, "absent"), filepath.Join(dir, "other")); err == nil {
		t.Fatal("missing copy source")
	}
	if got := probeIVFPath(dir, 2, 30); !strings.HasSuffix(got, "0002_30.ivf") {
		t.Fatalf("probe path: %s", got)
	}
	if cache := r.newRefCache(ch); cache != nil {
		t.Fatal("cache without denoise")
	}
	r.cfg.Denoise = "hqdn3d"
	if cache := r.newRefCache(ch); cache == nil {
		t.Fatal("expected filtered reference cache")
	} else {
		cache.remove()
	}
}

func TestTargetWorkerPropagatesMissingSource(t *testing.T) {
	r := newTargetQualityRun(TargetQualityConfig{Metric: quality.MetricCVVDP, Target: 9, Tolerance: 0.1, CRFMin: 10, CRFMax: 50, InitialCRF: 30, MaxProbes: 2, MetricWorkers: 1}, &EncodeConfig{}, "absent.y4m", t.TempDir(), testVideoInfo(), nil, 1920, 1080, newAdaptiveLimiter(1, 1, 1, 0, nil, nil), 1, nil, nil)
	ch := chunk.Chunk{Idx: 4, Start: 0, End: 2}
	chunks := make(chan chunk.Chunk, 1)
	results := make(chan targetQualityResult, 1)
	chunks <- ch
	close(chunks)
	r.runWorker(context.Background(), chunks, results)
	got := <-results
	if got.ChunkIdx != ch.Idx || got.Error == nil || !strings.Contains(got.Error.Error(), "open source") || got.Log.ChunkIdx != ch.Idx {
		t.Fatalf("failed probe result: %+v", got)
	}
	if active, _, _ := r.limiter.stats(); active != 0 {
		t.Fatalf("slot leaked: %d", active)
	}
}

func TestTargetScoreProbeCancellation(t *testing.T) {
	r := &targetQualityRun{cfg: &EncodeConfig{}, inf: testVideoInfo()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pool := make(chan quality.ChunkScorer)
	_, _, err := r.scoreProbe(ctx, pool, "", chunk.Chunk{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("score canceled: %v", err)
	}
}
