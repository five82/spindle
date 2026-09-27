package encode

import (
	"math"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/quality"
)

func TestFormatMultiProbeChunksShowsFirstAndLastProbe(t *testing.T) {
	logs := []chunkTargetLog{
		{
			ChunkIdx:   2,
			StopReason: quality.StopMaxProbes,
			Probes: []quality.Probe{
				{CRF: 27, Score: 9.9255},
				{CRF: 41.5, Score: 9.8751},
				{CRF: 58.5, Score: 9.7},
			},
		},
	}
	got := formatMultiProbeChunks(logs, 8)
	want := "[0002:3 probes crf 27->58.5 score 9.9255->9.7000 stop=max_probes]"
	if got != want {
		t.Fatalf("formatMultiProbeChunks() = %q, want %q", got, want)
	}
}

func TestOrderTargetQualityChunksSortsLargestFirstWithinTimelineBlocks(t *testing.T) {
	chunks := []chunk.Chunk{
		{Idx: 3, Start: 0, End: 10},
		{Idx: 0, Start: 0, End: 20},
		{Idx: 1, Start: 0, End: 40},
		{Idx: 2, Start: 0, End: 30},
		{Idx: 4, Start: 0, End: 60},
		{Idx: 5, Start: 0, End: 50},
	}
	ordered := orderTargetQualityChunks(chunks, 3)
	got := make([]int, len(ordered))
	for i, ch := range ordered {
		got[i] = ch.Idx
	}
	want := []int{1, 2, 0, 4, 5, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestTargetQualityPriorUsesDefaultWithoutHistory(t *testing.T) {
	prior := newTargetQualityPrior(26, 4.25, 63.75, 9.5, targetQualityDefaultJODPerCRF, quality.MetricCVVDP)
	crf, source := prior.InitialCRF(100)
	if crf != 26 || source != "default" {
		t.Fatalf("InitialCRF = %g %q, want 26 default", crf, source)
	}
}

func TestTargetQualityPriorUsesWeightedNeighborCRF(t *testing.T) {
	prior := newTargetQualityPrior(26, 4.25, 63.75, 9.5, targetQualityDefaultJODPerCRF, quality.MetricCVVDP)
	prior.AddResult(96, 20, nil)
	prior.AddResult(104, 28, nil)
	crf, source := prior.InitialCRF(100)
	if crf != 24 || source != "neighbor" {
		t.Fatalf("InitialCRF = %g %q, want 24 neighbor", crf, source)
	}
}

func TestTargetQualityPriorFallsBackToMedianForDistantHistory(t *testing.T) {
	prior := newTargetQualityPrior(26, 4.25, 63.75, 9.5, targetQualityDefaultJODPerCRF, quality.MetricCVVDP)
	prior.AddResult(10, 20, nil)
	prior.AddResult(20, 30, nil)
	prior.AddResult(30, 40, nil)
	crf, source := prior.InitialCRF(100)
	if crf != 30 || source != "median" {
		t.Fatalf("InitialCRF = %g %q, want 30 median", crf, source)
	}
}

func TestTargetQualityPriorLearnsJODPerCRF(t *testing.T) {
	prior := newTargetQualityPrior(26, 4.25, 63.75, 9.5, targetQualityDefaultJODPerCRF, quality.MetricCVVDP)
	prior.AddResult(10, 25, []quality.Probe{
		{CRF: 20, Score: 9.7},
		{CRF: 30, Score: 9.3},
	})
	if got := prior.JODPerCRF(); math.Abs(float64(got-0.04)) > 0.0001 {
		t.Fatalf("JODPerCRF = %g, want 0.04", got)
	}
}

func TestTargetQualityPriorNormalizesCRFToTarget(t *testing.T) {
	prior := newTargetQualityPrior(26, 4.25, 63.75, 9.5, targetQualityDefaultJODPerCRF, quality.MetricCVVDP)
	prior.AddResult(10, 25, []quality.Probe{
		{CRF: 20, Score: 9.7},
		{CRF: 25, Score: 9.58},
		{CRF: 30, Score: 9.3},
	})
	crf, source := prior.InitialCRF(11)
	if crf != 27 || source != "neighbor" {
		t.Fatalf("InitialCRF = %g %q, want 27 neighbor", crf, source)
	}
}

func TestTargetQualityPriorNormalizesCRFWithAdjustmentCap(t *testing.T) {
	prior := newTargetQualityPrior(26, 4.25, 63.75, 9.5, targetQualityDefaultJODPerCRF, quality.MetricCVVDP)
	prior.AddResult(10, 25, []quality.Probe{{CRF: 25, Score: 9.9}})
	crf, source := prior.InitialCRF(11)
	if crf != 28 || source != "neighbor" {
		t.Fatalf("InitialCRF = %g %q, want 28 neighbor", crf, source)
	}
}
