package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/quality"
	"github.com/five82/spindle/reel/internal/video"
)

func TestJobsDispatchMetricsAndReportFailuresWithoutGPU(t *testing.T) {
	src := testY4M(t, 16, 16, 2)
	dir := t.TempDir()
	jobs := filepath.Join(dir, "jobs.json")
	out := filepath.Join(dir, "results.json")
	spec, _ := json.Marshal(jobsFile{Jobs: []jobSpec{{ID: "one", Src: src, Dist: src, Frames: 2}}})
	if err := os.WriteFile(jobs, spec, 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	cv := func(w, h uint32, info *video.Info, path string) (*quality.VshipProcessor, error) {
		calls++
		if w != 16 || h != 16 || info == nil || path == "" {
			t.Errorf("CVVDP setup: %dx%d %v %q", w, h, info, path)
		}
		return nil, nil
	}
	ss := func(w, h uint32, info *video.Info) (*quality.SSIMU2Processor, error) {
		calls++
		if w != 16 || h != 16 || info == nil {
			t.Errorf("SSIMU2 setup: %dx%d %v", w, h, info)
		}
		return nil, nil
	}
	score := func(job jobSpec, _ *video.Info, w, h uint32, _ *quality.VshipProcessor, _ *quality.SSIMU2Processor, m metricSet, perFrame bool) (jobResult, error) {
		calls++
		if job.ID != "one" || w != 16 || h != 16 || !m.cvvdp || !m.ssimu2 || !perFrame {
			t.Errorf("score options: %+v %dx%d %+v %v", job, w, h, m, perFrame)
		}
		return jobResult{ID: job.ID, Frames: job.Frames, CVVDP: &cvvdpOut{Score: 8}, SSIMU2: &ssimu2Out{Mean: 60}}, nil
	}
	both := metricSet{cvvdp: true, ssimu2: true}
	if err := runJobsWithProcessors(jobs, out, "", true, both, cv, ss, score); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d", calls)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var result resultsFile
	if err := json.Unmarshal(data, &result); err != nil || len(result.Jobs) != 1 || result.Jobs[0].CVVDP.Score != 8 || result.Jobs[0].SSIMU2.Mean != 60 {
		t.Fatalf("result %+v: %v", result, err)
	}
	for _, tc := range []struct {
		name  string
		cv    func(uint32, uint32, *video.Info, string) (*quality.VshipProcessor, error)
		ss    func(uint32, uint32, *video.Info) (*quality.SSIMU2Processor, error)
		score func(jobSpec, *video.Info, uint32, uint32, *quality.VshipProcessor, *quality.SSIMU2Processor, metricSet, bool) (jobResult, error)
		want  string
	}{
		{"cv init", func(uint32, uint32, *video.Info, string) (*quality.VshipProcessor, error) {
			return nil, errors.New("cv init")
		}, ss, score, "create CVVDP processor: cv init"},
		{"ss init", cv, func(uint32, uint32, *video.Info) (*quality.SSIMU2Processor, error) { return nil, errors.New("ss init") }, score, "create SSIMU2 processor: ss init"},
		{"score", cv, ss, func(jobSpec, *video.Info, uint32, uint32, *quality.VshipProcessor, *quality.SSIMU2Processor, metricSet, bool) (jobResult, error) {
			return jobResult{}, errors.New("scoring failed")
		}, "job \"one\": scoring failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := runJobsWithProcessors(jobs, out, "", true, both, tc.cv, tc.ss, tc.score); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v", err)
			}
		})
	}
	if err := runJobsWithProcessors(jobs, dir, "", true, both, cv, ss, score); err == nil || !strings.Contains(err.Error(), "write results") {
		t.Fatalf("directory output: %v", err)
	}
}
