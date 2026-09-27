package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/video"
)

func testY4M(t *testing.T, w, h, frames int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clip.y4m")
	data := []byte(fmt.Sprintf("YUV4MPEG2 W%d H%d F25:1 Ip A1:1 C420\n", w, h))
	for i := 0; i < frames; i++ {
		data = append(data, []byte("FRAME\n")...)
		for j := 0; j < w*h*3/2; j++ {
			data = append(data, byte(64+i))
		}
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestJobsWithoutMetricsChecksInputsAndWritesResults(t *testing.T) {
	src := testY4M(t, 16, 16, 3)
	dist := testY4M(t, 16, 16, 3)
	dir := t.TempDir()
	jobs := filepath.Join(dir, "jobs.json")
	out := filepath.Join(dir, "out.json")
	spec := jobsFile{Jobs: []jobSpec{{ID: "a", Src: src, Dist: dist, Frames: 2}, {ID: "b", Src: src, Dist: dist, Frames: 1}}}
	data, _ := json.Marshal(spec)
	if err := os.WriteFile(jobs, data, 0600); err != nil {
		t.Fatal(err)
	}
	// A zero metric set isolates job validation and serialization from GPU scoring.
	if err := runJobs(jobs, out, "", false, metricSet{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var result resultsFile
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Jobs) != 2 || result.Jobs[0].ID != "a" || result.Jobs[0].Frames != 2 || result.Jobs[0].CVVDP != nil {
		t.Fatalf("result: %+v", result)
	}
	spec.Jobs[1].Dist = testY4M(t, 32, 16, 1)
	data, _ = json.Marshal(spec)
	if err := os.WriteFile(jobs, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runJobs(jobs, out, "", false, metricSet{}); err == nil || !strings.Contains(err.Error(), "distorted dimensions") {
		t.Fatalf("dimensions: %v", err)
	}
	spec.Jobs[1].Dist = dist
	spec.Jobs[1].Crop = "8:8:0:0"
	data, _ = json.Marshal(spec)
	if err := os.WriteFile(jobs, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runJobs(jobs, out, "", false, metricSet{}); err == nil || !strings.Contains(err.Error(), "consistent dimensions") {
		t.Fatalf("crop dimensions: %v", err)
	}
}

func TestGPUBenchDecodeWithoutMetrics(t *testing.T) {
	src := testY4M(t, 16, 16, 3)
	dist := testY4M(t, 16, 16, 3)
	if err := runGPUBench(src, dist, 1, 2, 1, "", "", metricSet{}); err != nil {
		t.Fatal(err)
	}
	si, err := video.Probe(src)
	if err != nil {
		t.Fatal(err)
	}
	di, err := video.Probe(dist)
	if err != nil {
		t.Fatal(err)
	}
	a, b, err := decodeFramesToRAM(src, dist, si, di, nil, 0, 2, 16, 16)
	if err != nil || len(a) != 2 || len(b) != 2 {
		t.Fatalf("decoded: %d %d %v", len(a), len(b), err)
	}
	if _, _, err := decodeFramesToRAM(src, dist, si, di, nil, 10, 1, 16, 16); err == nil || !strings.Contains(err.Error(), "source frame") {
		t.Fatalf("source eof: %v", err)
	}
	shortDist := testY4M(t, 16, 16, 1)
	shortInfo, err := video.Probe(shortDist)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeFramesToRAM(src, shortDist, si, shortInfo, nil, 0, 2, 16, 16); err == nil || !strings.Contains(err.Error(), "distorted frame") {
		t.Fatalf("distorted EOF: %v", err)
	}
	if err := runGPUBench(src, dist, 0, 1, 1, "bad", "", metricSet{}); err == nil {
		t.Fatal("expected invalid crop")
	}
	if err := runGPUBench(src, testY4M(t, 32, 16, 1), 0, 1, 1, "", "", metricSet{}); err == nil {
		t.Fatal("expected dimension mismatch")
	}
}
