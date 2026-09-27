package perf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCollectorWritesArtifact(t *testing.T) {
	dir := t.TempDir()
	c := New()
	c.SetWorkDir(dir)
	c.UpdateMeta(func(m *Meta) {
		m.InputFile = "movie.mkv"
		m.Width = 3840
		m.Height = 2160
		m.HDR = true
		m.QualityMode = "target"
		m.TargetQuality = "9.15-9.55"
		m.MetricWorkers = 4
		m.Chunks = 12
	})

	base := time.Now()
	c.RecordPhase("Crop detection", base, base.Add(500*time.Millisecond))
	c.RecordPhase("Video encoding", base.Add(time.Second), base.Add(10*time.Second))
	c.RecordWorkerSample(WorkerSample{Active: 3, Target: 3, Max: 24, InFlight: 6, ChunksComplete: 0})
	c.RecordWorkerSample(WorkerSample{Active: 4, Target: 4, Max: 24, InFlight: 8, ChunksComplete: 2})

	if err := c.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "perf.json"))
	if err != nil {
		t.Fatalf("read perf.json: %v", err)
	}

	var got struct {
		Meta
		TotalSeconds  float64        `json:"total_seconds"`
		Phases        []Phase        `json:"phases"`
		WorkerHistory []WorkerSample `json:"worker_history"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.SchemaVersion != schemaVersion {
		t.Errorf("schema_version = %d, want %d", got.SchemaVersion, schemaVersion)
	}
	if got.InputFile != "movie.mkv" || got.TargetQuality != "9.15-9.55" || got.MetricWorkers != 4 {
		t.Errorf("metadata not round-tripped: %+v", got.Meta)
	}
	if len(got.Phases) != 2 {
		t.Fatalf("phases = %d, want 2", len(got.Phases))
	}
	if got.Phases[0].Name != "Crop detection" {
		t.Errorf("phases not sorted by start: %+v", got.Phases)
	}
	if got.Phases[1].DurationSeconds < 8.9 || got.Phases[1].DurationSeconds > 9.1 {
		t.Errorf("video encoding duration = %.3f, want ~9", got.Phases[1].DurationSeconds)
	}
	if len(got.WorkerHistory) != 2 {
		t.Fatalf("worker_history = %d, want 2", len(got.WorkerHistory))
	}
	if got.WorkerHistory[1].InFlight != 8 {
		t.Errorf("in_flight = %d, want 8", got.WorkerHistory[1].InFlight)
	}
	if got.TotalSeconds < 0 {
		t.Errorf("total_seconds = %.3f, want >= 0", got.TotalSeconds)
	}
}

func TestWorkerSampleThrottle(t *testing.T) {
	c := New()
	// Same worker counts in quick succession: only the first is kept.
	c.RecordWorkerSample(WorkerSample{Active: 3, Target: 3, Max: 8, InFlight: 3, FramesComplete: 10})
	c.RecordWorkerSample(WorkerSample{Active: 3, Target: 3, Max: 8, InFlight: 3, FramesComplete: 20})
	// A worker-count change is always recorded even within the interval.
	c.RecordWorkerSample(WorkerSample{Active: 4, Target: 4, Max: 8, InFlight: 5, FramesComplete: 30})

	c.mu.Lock()
	n := len(c.samples)
	c.mu.Unlock()
	if n != 2 {
		t.Fatalf("samples = %d, want 2 (throttled duplicate dropped, change kept)", n)
	}
}

func TestNilCollectorIsNoOp(t *testing.T) {
	var c *Collector
	// None of these should panic on a nil receiver.
	c.SetWorkDir("/tmp/whatever")
	c.UpdateMeta(func(m *Meta) { m.InputFile = "x" })
	c.RecordPhase("p", time.Now(), time.Now())
	c.RecordWorkerSample(WorkerSample{Active: 1})
	if err := c.Write(); err != nil {
		t.Fatalf("nil Write: %v", err)
	}
}

func TestWriteWithoutWorkDirSkips(t *testing.T) {
	c := New()
	c.RecordPhase("p", time.Now(), time.Now())
	if err := c.Write(); err != nil {
		t.Fatalf("Write without workdir should be a no-op, got %v", err)
	}
}

func TestSummarizeWorkers(t *testing.T) {
	if s := summarizeWorkers(nil); s.MeanActive != 0 || s.PeakActive != 0 {
		t.Fatalf("empty history should summarize to zero: %+v", s)
	}
	samples := []WorkerSample{
		{TSeconds: 0, Active: 2, EncodeSlotWaitSeconds: 0},
		{TSeconds: 10, Active: 4, EncodeSlotWaitSeconds: 5},
		{TSeconds: 30, Active: 3, EncodeSlotWaitSeconds: 12},
	}
	s := summarizeWorkers(samples)
	// Time-weighted: 2 workers for 10s, 4 workers for 20s over a 30s span.
	want := (2.0*10 + 4.0*20) / 30
	if s.MeanActive < want-0.001 || s.MeanActive > want+0.001 {
		t.Errorf("mean active = %.3f, want %.3f", s.MeanActive, want)
	}
	if s.PeakActive != 4 {
		t.Errorf("peak active = %d, want 4", s.PeakActive)
	}
	if s.EncodeSlotWaitSeconds != 12 {
		t.Errorf("slot wait = %.1f, want 12", s.EncodeSlotWaitSeconds)
	}
}

func TestReportCarriesTargetQuality(t *testing.T) {
	c := New()
	c.SetTargetQuality(&TargetQualityStats{Metrics: []TargetQualityMetricStats{{Metric: "cvvdp", Chunks: 3}}})
	r := c.Report()
	if r == nil || r.TargetQualityStats == nil || r.TargetQualityStats.Metrics[0].Chunks != 3 {
		t.Fatalf("report missing target-quality stats: %+v", r)
	}
	var nilC *Collector
	if nilC.Report() != nil {
		t.Error("nil collector Report should return nil")
	}
	nilC.SetTargetQuality(nil) // must not panic
}
