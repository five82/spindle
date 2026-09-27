package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/reel/internal/chunk"
)

func TestScoreStatistics(t *testing.T) {
	chunks := []chunk.Chunk{{Idx: 1}, {Idx: 2}}
	scores := map[int]float32{1: 7, 2: 9}
	avg, min, below := stats(chunks, scores, tqLog{Target: 8, Tolerance: 0.5})
	if avg != 8 || min != 7 || below != 1 {
		t.Fatalf("stats = %v, %v, %d", avg, min, below)
	}
	delta, worst := maxDeltaVsTruth(chunks, scores, map[int]float32{1: 7.25, 2: 8})
	if delta != 1 || worst != 2 {
		t.Fatalf("delta = %v, %d", delta, worst)
	}
	if abs64(-2) != 2 || abs64(2) != 2 || mustAtoi("12") != 12 {
		t.Fatal("numeric helpers")
	}
}

func TestReadHandlerInputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.json")
	if _, err := readManifest(path); err == nil {
		t.Fatal("missing manifest")
	}
	if _, err := readTQLog(path); err == nil {
		t.Fatal("missing log")
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifest(path); err == nil {
		t.Fatal("malformed manifest")
	}
	if _, err := readTQLog(path); err == nil {
		t.Fatal("malformed log")
	}
	if err := os.WriteFile(path, []byte(`{"target":8,"chunks":[{"chunk_idx":3}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifest(path); err != nil {
		t.Fatal(err)
	}
	log, err := readTQLog(path)
	if err != nil || log.Target != 8 || len(log.Chunks) != 1 {
		t.Fatalf("log = %+v, %v", log, err)
	}
}
