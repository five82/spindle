package encode

import (
	"testing"

	"github.com/five82/reel/internal/quality"
)

func TestTargetQualityStats(t *testing.T) {
	if targetQualityStats(nil, nil) != nil {
		t.Fatal("empty logs should produce nil stats")
	}

	logs := []chunkTargetLog{
		{
			ChunkIdx: 0, Metric: string(quality.MetricCVVDP),
			Target: 9.55, Tolerance: 0.20,
			Probes:   []quality.Probe{{CRF: 30}, {CRF: 28}},
			FinalCRF: 28, FinalScore: 9.50,
			StopReason:       quality.StopConverged,
			InitialCRFSource: "default",
		},
		{
			ChunkIdx: 1, Metric: string(quality.MetricCVVDP),
			Target: 9.55, Tolerance: 0.20,
			Probes:   []quality.Probe{{CRF: 28}},
			FinalCRF: 32, FinalScore: 9.70,
			StopReason:       quality.StopMaxProbes,
			InitialCRFSource: "neighbor",
		},
		{
			ChunkIdx: 2, Metric: string(quality.MetricSSIMU2),
			Target: 67.4, Tolerance: 7.5,
			Probes:   []quality.Probe{{CRF: 26}},
			FinalCRF: 26, FinalScore: 68.0,
			StopReason:       quality.StopConverged,
			InitialCRFSource: "median",
		},
	}

	stats := targetQualityStats(logs, nil)
	if stats == nil {
		t.Fatal("stats is nil")
	}
	if stats.SSIMU2CalibrationOffset != nil {
		t.Error("no calibration passed, offset should be nil")
	}
	if len(stats.Metrics) != 2 {
		t.Fatalf("metric groups = %d, want 2", len(stats.Metrics))
	}
	// Groups are sorted by metric name: cvvdp before ssimulacra2.
	cvvdp := stats.Metrics[0]
	if cvvdp.Metric != string(quality.MetricCVVDP) {
		t.Fatalf("first group metric = %q", cvvdp.Metric)
	}
	if cvvdp.Chunks != 2 || cvvdp.Probes != 3 || cvvdp.ProbesPerChunk != 1.5 {
		t.Errorf("chunk/probe counts wrong: %+v", cvvdp)
	}
	if cvvdp.ScoreMin < 9.49 || cvvdp.ScoreMin > 9.51 || cvvdp.ScoreMax < 9.69 || cvvdp.ScoreMax > 9.71 {
		t.Errorf("score range wrong: min=%.3f max=%.3f", cvvdp.ScoreMin, cvvdp.ScoreMax)
	}
	if cvvdp.FinalCRFMin != 28 || cvvdp.FinalCRFMax != 32 {
		t.Errorf("crf range wrong: %+v", cvvdp)
	}
	if cvvdp.StopReasons["converged"] != 1 || cvvdp.StopReasons["max_probes"] != 1 {
		t.Errorf("stop reasons wrong: %+v", cvvdp.StopReasons)
	}
	if cvvdp.InitialCRFSources["default"] != 1 || cvvdp.InitialCRFSources["neighbor"] != 1 {
		t.Errorf("initial sources wrong: %+v", cvvdp.InitialCRFSources)
	}
}

func TestTargetQualityStatsDefaultsEmptyMetricToCVVDP(t *testing.T) {
	logs := []chunkTargetLog{{
		ChunkIdx: 0,
		Probes:   []quality.Probe{{CRF: 30}},
		FinalCRF: 30, FinalScore: 9.5, Target: 9.55,
	}}
	stats := targetQualityStats(logs, nil)
	if len(stats.Metrics) != 1 || stats.Metrics[0].Metric != string(quality.MetricCVVDP) {
		t.Fatalf("empty metric should group as CVVDP: %+v", stats.Metrics)
	}
	if stats.Metrics[0].StopReasons["none"] != 1 {
		t.Errorf("empty stop reason should count as none: %+v", stats.Metrics[0].StopReasons)
	}
}
