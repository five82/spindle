package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportIncludesSortedWorstChunks(t *testing.T) {
	results := []chunkResult{
		{idx: 2, frames: 48, full: 8, metric: "cvvdp", recorded: 7.8, probes: 2, crf: 30},
		{idx: 1, frames: 24, full: 6, metric: "ssimu2", recorded: 7, probes: 1, crf: 25},
		{idx: 3, frames: 72, full: 9, metric: "cvvdp", recorded: 8.5, probes: 3, crf: 35},
	}
	// report also sorts the results for the worst-chunk table.
	report(results, 8, 0.5)
	if results[0].idx != 1 || results[2].idx != 3 {
		t.Fatalf("worst-chunk order: %+v", results)
	}
	if abs64(-2.5) != 2.5 || abs64(2.5) != 2.5 || mean([]float64{1, 3}) != 2 || pct([]float64{1, 2, 3}, 0.5) != 2 || pct(nil, 0.5) != 0 {
		t.Fatal("report statistics")
	}
}

func TestReadValidationInputs(t *testing.T) {
	dir := t.TempDir()
	if _, err := readManifest(filepath.Join(dir, "absent")); err == nil {
		t.Fatal("missing manifest")
	}
	if _, err := readTQLog(filepath.Join(dir, "absent")); err == nil {
		t.Fatal("missing log")
	}
	path := filepath.Join(dir, "input.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifest(path); err == nil {
		t.Fatal("malformed manifest")
	}
	if _, err := readTQLog(path); err == nil {
		t.Fatal("malformed log")
	}
	if err := os.WriteFile(path, []byte(`{"target_quality":"7:9:8:0.5","target":8,"chunks":[{"chunk_idx":2}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(path)
	if err != nil || !strings.Contains(m.TargetQuality, "7:9") {
		t.Fatalf("manifest: %+v, %v", m, err)
	}
	log, err := readTQLog(path)
	if err != nil || log.Target != 8 || len(log.Chunks) != 1 || log.Chunks[0].ChunkIdx != 2 {
		t.Fatalf("log: %+v, %v", log, err)
	}
}
