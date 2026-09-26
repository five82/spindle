package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/five82/reel/internal/quality"
	"github.com/five82/reel/internal/video"
)

type benchCVVDP struct {
	resets, computes, closes int
	resetErr, computeErr     error
}

func (p *benchCVVDP) ResetCVVDP() error { p.resets++; return p.resetErr }
func (p *benchCVVDP) ComputeCVVDP(a, b quality.FramePlanes) (float32, error) {
	p.computes++
	if a.Strides[0] != 32 || b.Strides[0] != 32 {
		panic("invalid planes")
	}
	return 8, p.computeErr
}
func (p *benchCVVDP) Close() error { p.closes++; return nil }

type benchSSIMU2 struct {
	computes, closes int
	computeErr       error
}

func (p *benchSSIMU2) ComputeSSIMU2(a, b quality.FramePlanes) (float64, error) {
	p.computes++
	if a.Strides[0] != 32 || b.Strides[0] != 32 {
		panic("invalid planes")
	}
	return 70, p.computeErr
}
func (p *benchSSIMU2) Close() error { p.closes++; return nil }

func TestBenchMetricPassesAndFailurePropagation(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	src := testY4M(t, 16, 16, 2)
	dist := testY4M(t, 16, 16, 2)
	for _, tc := range []struct {
		name                                        string
		cv, ss                                      bool
		cvInit, ssInit, reset, cvCompute, ssCompute error
		want                                        string
	}{
		{name: "both", cv: true, ss: true},
		{name: "CVVDP init", cv: true, cvInit: errors.New("CVVDP setup failed"), want: "CVVDP setup failed"},
		{name: "CVVDP reset", cv: true, reset: errors.New("reset failed"), want: "reset failed"},
		{name: "CVVDP compute", cv: true, cvCompute: errors.New("CVVDP score failed"), want: "CVVDP score failed"},
		{name: "SSIMU2 init", ss: true, ssInit: errors.New("SSIMU2 setup failed"), want: "SSIMU2 setup failed"},
		{name: "SSIMU2 compute", ss: true, ssCompute: errors.New("SSIMU2 score failed"), want: "SSIMU2 score failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cv := &benchCVVDP{resetErr: tc.reset, computeErr: tc.cvCompute}
			ss := &benchSSIMU2{computeErr: tc.ssCompute}
			err := benchWithProcessors(src, dist, 0, 2, 2, "", "", metricSet{cvvdp: tc.cv, ssimu2: tc.ss},
				func(w, h uint32, info *video.Info, display string) (cvvdpBenchProcessor, error) {
					if w != 16 || h != 16 || info == nil || display == "" {
						t.Error("bad CVVDP configuration")
					}
					if tc.cvInit != nil {
						return nil, tc.cvInit
					}
					return cv, nil
				}, func(w, h uint32, info *video.Info) (ssimu2BenchProcessor, error) {
					if w != 16 || h != 16 || info == nil {
						t.Error("bad SSIMU2 configuration")
					}
					if tc.ssInit != nil {
						return nil, tc.ssInit
					}
					return ss, nil
				})
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error %v, want %q", err, tc.want)
				}
				return
			}
			if err != nil || cv.resets != 2 || cv.computes != 4 || cv.closes != 1 || ss.computes != 4 || ss.closes != 1 {
				t.Fatalf("err=%v cv=%+v ss=%+v", err, cv, ss)
			}
		})
	}
}
