package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/five82/reel/internal/quality"
	"github.com/five82/reel/internal/video"
)

func TestScoreJobPassesAlignedChunkToMetrics(t *testing.T) {
	src := testY4M(t, 16, 16, 4)
	dist := testY4M(t, 8, 8, 3)
	info, err := video.Probe(src)
	if err != nil {
		t.Fatal(err)
	}
	job := jobSpec{ID: "sample", Src: src, Dist: dist, Start: 1, Frames: 2, Crop: "8:8:2:2"}
	cvCalls, ssCalls := 0, 0
	cv := func(opts quality.CVVDPOptions) (quality.CVVDPResult, error) {
		cvCalls++
		if opts.Chunk.Start != 1 || opts.Chunk.Frames() != 2 || opts.Width != 8 || opts.Height != 8 || opts.CropRect.X != 2 || opts.ProbePath != dist {
			t.Errorf("cvvdp options: %+v", opts)
		}
		return quality.CVVDPResult{Score: 8.1, MetricSeconds: 1.25}, nil
	}
	ss := func(opts quality.SSIMU2Options) (quality.SSIMU2Result, error) {
		ssCalls++
		if opts.Chunk.Start != 1 || opts.CropRect.Y != 2 {
			t.Errorf("ssimu2 options: %+v", opts)
		}
		return quality.SSIMU2Result{Mean: 6, Min: 5, P5: 5.1, P10: 5.2, MetricSeconds: 2, PerFrame: []float64{5, 7}}, nil
	}
	for _, perFrame := range []bool{false, true} {
		res, err := scoreJobWithMetrics(job, info, 8, 8, metricSet{cvvdp: true, ssimu2: true}, perFrame, cv, ss)
		if err != nil || res.ID != "sample" || res.Frames != 2 || res.CVVDP.Score != 8.1 || res.SSIMU2.Mean != 6 || (len(res.SSIMU2.PerFrame) == 2) != perFrame {
			t.Fatalf("result %+v: %v", res, err)
		}
	}
	if cvCalls != 2 || ssCalls != 2 {
		t.Fatalf("calls: %d/%d", cvCalls, ssCalls)
	}
	for _, tc := range []struct {
		name    string
		metrics metricSet
		cv      func(quality.CVVDPOptions) (quality.CVVDPResult, error)
		ss      func(quality.SSIMU2Options) (quality.SSIMU2Result, error)
		want    string
	}{
		{"cvvdp error", metricSet{cvvdp: true}, func(quality.CVVDPOptions) (quality.CVVDPResult, error) {
			return quality.CVVDPResult{}, errors.New("cv failed")
		}, ss, "cvvdp: cv failed"},
		{"ssimu2 error", metricSet{ssimu2: true}, cv, func(quality.SSIMU2Options) (quality.SSIMU2Result, error) {
			return quality.SSIMU2Result{}, errors.New("ss failed")
		}, "ssimu2: ss failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := scoreJobWithMetrics(job, info, 8, 8, tc.metrics, false, tc.cv, tc.ss)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error: %v", err)
			}
		})
	}
}
