package encode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/quality"
)

func TestTargetLogFormatting(t *testing.T) {
	if got := formatIntCounts(nil); got != "{}" {
		t.Fatal(got)
	}
	if got := formatIntCounts(map[int]int{3: 2, 1: 4}); got != "{1:4,3:2}" {
		t.Fatal(got)
	}
	if got := formatStringCounts(map[string]int{"z": 2, "a": 1}); got != "{a:1,z:2}" {
		t.Fatal(got)
	}
	if got := formatStopCounts(map[quality.StopReason]int{"": 2, quality.StopMaxProbes: 1}); got != "{max_probes:1,none:2}" {
		t.Fatal(got)
	}
	if got := formatChunkList(nil, 2); got != "[]" {
		t.Fatal(got)
	}
	chunks := []int{9, 1, 5}
	if got := formatChunkList(chunks, 2); got != "[0001,0005,+1 more]" {
		t.Fatal(got)
	}
	if !reflect.DeepEqual(chunks, []int{1, 5, 9}) {
		t.Fatal(chunks)
	}
	if got := formatChunkList(chunks, 0); got != "[0001,0005,0009]" {
		t.Fatal(got)
	}
}

func TestTargetAggregateReportsMixedMetricsAndOutliers(t *testing.T) {
	logs := []chunkTargetLog{
		{ChunkIdx: 9, Metric: string(quality.MetricCVVDP), Target: 9, FinalScore: 8, FinalCRF: 32, Probes: []quality.Probe{{CRF: 30, Score: 7}, {CRF: 31, Score: 8}, {CRF: 32, Score: 8}}, StopReason: quality.StopMaxProbes, InitialCRFSource: "neighbor"},
		{ChunkIdx: 2, Metric: string(quality.MetricCVVDP), Target: 9, FinalScore: 10, FinalCRF: 32, Probes: []quality.Probe{{CRF: 32, Score: 10}}, StopReason: quality.StopRateCapped},
		{ChunkIdx: 3, Metric: string(quality.MetricSSIMU2), Target: 80, FinalScore: 81, FinalCRF: 25, Probes: []quality.Probe{{CRF: 25, Score: 81}}},
	}
	var messages []string
	logTargetAggregate(logs, func(s string) { messages = append(messages, s) })
	joined := strings.Join(messages, "\n")
	for _, want := range []string{"cvvdp-scored chunks", "ssimulacra2-scored chunks", "chunks=2 probes=4", "multi-probe chunks", "0009:3 probes", "max-probe chunks: [0009]", "rate-capped chunks", "initial_sources={neighbor:1}"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
	if len(logs) != 3 {
		t.Fatal("logs mutated")
	}
	logTargetAggregate(logs, nil)
	logTargetAggregate(nil, func(string) { t.Fatal("empty aggregate emitted") })
}

func TestTargetLogFiles(t *testing.T) {
	dir := t.TempDir()
	log := chunkTargetLog{ChunkIdx: 7, Metric: string(quality.MetricCVVDP), FinalCRF: 30}
	if err := writeChunkTargetLog(dir, log); err == nil {
		t.Fatal("missing tq directory accepted")
	}
	if err := os.Mkdir(filepath.Join(dir, "tq"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeChunkTargetLog(dir, log); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tq", "0007.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved chunkTargetLog
	if err := json.Unmarshal(data, &saved); err != nil || saved.ChunkIdx != 7 || saved.FinalCRF != 30 || data[len(data)-1] != '\n' {
		t.Fatalf("saved log: %+v, %v", saved, err)
	}
	logs := []chunkTargetLog{log, {ChunkIdx: 2}}
	writeAggregateTargetLog(dir, logs, TargetQualityConfig{Target: 9, Metric: quality.MetricCVVDP}, nil)
	if logs[0].ChunkIdx != 2 {
		t.Fatal("aggregate did not sort chunks")
	}
	data, err = os.ReadFile(filepath.Join(dir, "target-quality.json"))
	if err != nil {
		t.Fatal(err)
	}
	var aggregate struct {
		Metric string           `json:"metric"`
		Target float32          `json:"target"`
		Chunks []chunkTargetLog `json:"chunks"`
	}
	if err := json.Unmarshal(data, &aggregate); err != nil || aggregate.Metric != string(quality.MetricCVVDP) || aggregate.Target != 9 || len(aggregate.Chunks) != 2 {
		t.Fatalf("aggregate: %+v, %v", aggregate, err)
	}
}
