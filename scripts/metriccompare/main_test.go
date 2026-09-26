package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMetricsAndCrop(t *testing.T) {
	m, err := parseMetrics(", cvvdp, ssimu2,")
	if err != nil || !m.cvvdp || !m.ssimu2 {
		t.Fatalf("metrics: %+v, %v", m, err)
	}
	for _, input := range []string{"", ",", "cvvdp,other"} {
		if _, err := parseMetrics(input); err == nil {
			t.Errorf("expected metrics error: %q", input)
		}
	}
	if crop, err := parseCrop(""); err != nil || crop != nil {
		t.Fatalf("empty crop: %+v, %v", crop, err)
	}
	for _, input := range []string{"10:20:0", "10:20:x:0", "-1:20:0:0"} {
		if _, err := parseCrop(input); err == nil {
			t.Errorf("expected crop error: %q", input)
		}
	}
	crop, err := parseCrop("10:20:2:4")
	if err != nil || crop.Width != 10 || crop.Height != 20 || crop.X != 2 || crop.Y != 4 {
		t.Fatalf("crop: %+v, %v", crop, err)
	}
}

func TestJobInputErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	for _, tc := range []struct{ contents, want string }{
		{"{", "parse jobs file"},
		{`{"jobs":[]}`, "no jobs"},
		{`{"jobs":[{"id":"first","src":"/does/not/exist"}]}`, "probe job"},
	} {
		if err := os.WriteFile(path, []byte(tc.contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runJobs(path, filepath.Join(dir, "out.json"), "", false, metricSet{cvvdp: true}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("runJobs(%s) = %v", tc.contents, err)
		}
	}
	if err := runJobs(filepath.Join(dir, "absent"), "", "", false, metricSet{}); err == nil || !strings.Contains(err.Error(), "read jobs file") {
		t.Fatalf("missing jobs: %v", err)
	}
	if _, err := scoreJob(jobSpec{Src: filepath.Join(dir, "absent")}, nil, 0, 0, nil, nil, metricSet{}, false); err == nil || !strings.Contains(err.Error(), "probe source") {
		t.Fatalf("missing source: %v", err)
	}
}

func TestGPUBenchInputErrors(t *testing.T) {
	for _, tc := range []struct {
		src, dist    string
		frames, reps int
		want         string
	}{
		{"", "", 1, 1, "requires --src"},
		{"source", "dist", 0, 1, "must be positive"},
		{"source", "dist", 1, 0, "must be positive"},
		{"/does/not/exist", "dist", 1, 1, "probe source"},
	} {
		err := runGPUBench(tc.src, tc.dist, 0, tc.frames, tc.reps, "", "", metricSet{cvvdp: true})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("bench error = %v, want %s", err, tc.want)
		}
	}
}
