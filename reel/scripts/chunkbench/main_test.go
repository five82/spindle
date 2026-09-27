package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/chunkplan"
)

func TestWriteScores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scores.txt")
	if err := writeScores(path, []float64{0.25, 1.5}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "0.250000\n1.500000\n" {
		t.Fatalf("scores = %q, %v", b, err)
	}
	if err := writeScores(t.TempDir(), nil); err == nil {
		t.Fatal("expected create error for directory")
	}
}

func TestChunkStats(t *testing.T) {
	// The summary should accept both an empty plan and a plan with natural and synthetic boundaries.
	original := os.Stdout
	out, err := os.CreateTemp(t.TempDir(), "stats")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = out
	t.Cleanup(func() { os.Stdout = original; _ = out.Close() })
	printChunkStats(chunkplan.Result{}, 24)
	printChunkStats(chunkplan.Result{Frames: 240, Boundaries: []int{0, 24, 120}, BoundaryKinds: []chunkplan.BoundaryKind{chunkplan.BoundaryKindNaturalShotCut, chunkplan.BoundaryKindSyntheticSplit}}, 24)
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Total frames:         240", "Final chunks:         3", "Under 2s: 1", "Natural shot cuts:    1", "Synthetic splits:     1"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in summary:\n%s", want, b)
		}
	}
}

func TestChunkBenchNumbers(t *testing.T) {
	if got := formatDuration(125); got != "02:05" {
		t.Fatalf("duration = %s", got)
	}
	if sumFloat64([]float64{1, 2, 3}) != 6 || meanFloat64(nil) != 0 || meanFloat64([]float64{1, 3}) != 2 {
		t.Fatal("float aggregate")
	}
	if meanInt(nil) != 0 || meanInt([]int{1, 3}) != 2 {
		t.Fatal("integer mean")
	}
	if minFloat64(nil) != 0 || minFloat64([]float64{3, 1, 2}) != 1 || maxFloat64(nil) != 0 || maxFloat64([]float64{3, 1, 2}) != 3 {
		t.Fatal("float extrema")
	}
	if minInt(nil) != 0 || minInt([]int{3, 1, 2}) != 1 || maxInt(nil) != 0 || maxInt([]int{3, 1, 2}) != 3 {
		t.Fatal("integer extrema")
	}
	if countUnder([]float64{1, 2, 3}, 2) != 1 {
		t.Fatal("strict threshold")
	}
	values := []float64{3, 1, 2}
	float64Sort(values)
	if percentileFloat64(nil, 0.5) != 0 || percentileFloat64(values, -1) != 1 || percentileFloat64(values, 0.5) != 2 || percentileFloat64(values, 2) != 3 {
		t.Fatalf("percentiles: %v", values)
	}
	if boundaryHash([]int{1, 2}) == boundaryHash([]int{2, 1}) || boundaryHash([]int{1, 2}) != "2fc275f10bd7c7fe" {
		t.Fatal("boundary hash must be stable and order sensitive")
	}
}
