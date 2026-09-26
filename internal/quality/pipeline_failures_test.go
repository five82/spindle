package quality

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/video"
)

func writePipelineY4M(t *testing.T, frames int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.y4m")
	data := []byte("YUV4MPEG2 W16 H16 F25:1 Ip A1:1 C420\n")
	for i := 0; i < frames; i++ {
		data = append(data, "FRAME\n"...)
		for j := 0; j < 16*16*3/2; j++ {
			data = append(data, byte(32+i))
		}
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type fakeCVVDP struct {
	resetErr, computeErr error
	calls                int
}

func (f *fakeCVVDP) ResetCVVDP() error { return f.resetErr }
func (f *fakeCVVDP) ComputeCVVDP(src, dist FramePlanes) (float32, error) {
	f.calls++
	if src.Strides[0] != 32 || dist.Strides[0] != 32 {
		panic("unexpected plane layout")
	}
	if f.computeErr != nil {
		return 0, f.computeErr
	}
	return float32(f.calls), nil
}

type fakeSSIMU2 struct {
	scores []float64
	err    error
	calls  int
}

func (f *fakeSSIMU2) ComputeSSIMU2(src, dist FramePlanes) (float64, error) {
	if src.Strides[0] != 32 || dist.Strides[0] != 32 {
		panic("unexpected plane layout")
	}
	if f.err != nil {
		return 0, f.err
	}
	score := f.scores[f.calls]
	f.calls++
	return score, nil
}

type pipelineReader func(int, []byte) error

func (f pipelineReader) ReadFrame(i int, b []byte) error { return f(i, b) }

func goMetricBuffer(size int) ([]byte, func(), error) {
	return make([]byte, size), func() {}, nil
}

func TestSSIMU2PipelineWithFakeScorer(t *testing.T) {
	path := writePipelineY4M(t, 3)
	info, err := video.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	read := pipelineReader(func(i int, b []byte) error {
		for j := range b {
			b[j] = byte(i)
		}
		return nil
	})
	opts := SSIMU2Options{Info: info, ProbePath: path, Reference: read, Chunk: chunk.Chunk{End: 3}, Width: 16, Height: 16}
	proc := &fakeSSIMU2{scores: []float64{0.9, 0.2, 0.7}}
	res, err := computeChunkSSIMU2(context.Background(), opts, proc, goMetricBuffer)
	if err != nil || proc.calls != 3 || res.Frames != 3 || res.Mean != 0.6 || res.Min != 0.2 || len(res.PerFrame) != 3 {
		t.Fatalf("result %+v calls %d: %v", res, proc.calls, err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*SSIMU2Options)
		proc   *fakeSSIMU2
		want   string
	}{
		{"reference error", func(o *SSIMU2Options) {
			o.Reference = pipelineReader(func(int, []byte) error { return errors.New("reference failed") })
		}, &fakeSSIMU2{}, "reference failed"},
		{"probe EOF", func(o *SSIMU2Options) { o.Chunk.End = 4 }, &fakeSSIMU2{scores: []float64{1, 1, 1, 1}}, "probe frame 3"},
		{"compute error", func(*SSIMU2Options) {}, &fakeSSIMU2{err: errors.New("scoring failed")}, "SSIMU2 failed on frame 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := opts
			tc.mutate(&o)
			_, err := computeChunkSSIMU2(context.Background(), o, tc.proc, goMetricBuffer)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCVVDPPipelineWithFakeScorer(t *testing.T) {
	read := func(i int, b []byte) error {
		for j := range b {
			b[j] = byte(i + j)
		}
		return nil
	}
	var observed []int
	observe := func(i int, a, b []byte) error {
		if a[0] != byte(i) || b[0] != byte(i) {
			return fmt.Errorf("misaligned frame %d", i)
		}
		observed = append(observed, i)
		return nil
	}
	proc := &fakeCVVDP{}
	res, err := computeCVVDPFramesWithBuffer(context.Background(), proc, 16, 16, 4, read, read, observe, goMetricBuffer)
	if err != nil || res.Score != 4 || res.Frames != 4 || proc.calls != 4 || len(observed) != 4 {
		t.Fatalf("result %+v, calls %d, observed %v: %v", res, proc.calls, observed, err)
	}
	for _, tc := range []struct {
		name      string
		proc      *fakeCVVDP
		ref, dist func(int, []byte) error
		observe   func(int, []byte, []byte) error
		want      string
	}{
		{"reset", &fakeCVVDP{resetErr: errors.New("reset failed")}, read, read, nil, "reset failed"},
		{"compute", &fakeCVVDP{computeErr: errors.New("compute failed")}, read, read, nil, "CVVDP failed on frame 0"},
		{"reference", &fakeCVVDP{}, func(int, []byte) error { return errors.New("read reference") }, read, nil, "read reference"},
		{"distorted", &fakeCVVDP{}, read, func(int, []byte) error { return errors.New("read distorted") }, nil, "read distorted"},
		{"observer", &fakeCVVDP{}, read, read, func(int, []byte, []byte) error { return errors.New("analysis failed") }, "analysis failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := computeCVVDPFramesWithBuffer(context.Background(), tc.proc, 16, 16, 3, tc.ref, tc.dist, tc.observe, goMetricBuffer)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCVVDPPairProducerFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ref, dist func(int, []byte) error
		observe   func(int, []byte, []byte) error
		want      string
	}{
		{name: "reference", ref: func(i int, _ []byte) error { return fmt.Errorf("reference %d", i) }, want: "reference 0"},
		{name: "distorted", dist: func(i int, _ []byte) error { return fmt.Errorf("distorted %d", i) }, want: "distorted 0"},
		{name: "observer", observe: func(int, []byte, []byte) error { return errors.New("observer failed") }, want: "observer failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := func(int, []byte) error { return nil }
			if tc.ref != nil {
				read = tc.ref
			}
			dist := func(int, []byte) error { return nil }
			if tc.dist != nil {
				dist = tc.dist
			}
			err := readCVVDPPair(context.Background(), 0, make([]byte, 768), make([]byte, 768), read, dist, tc.observe)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}
