package encode

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/perf"
	"github.com/five82/reel/internal/video"
)

func TestGrainPairEstimatorMeasuresCeilingWithoutGPU(t *testing.T) {
	const w, h = 512, 256
	chunks := []chunk.Chunk{{Idx: 2, Start: 0, End: 8}, {Idx: 4, Start: 8, End: 16}}
	in := GrainGateInput{Info: &video.Info{Width: w, Height: h}, Chunks: chunks}
	var verbose []string
	in.Verbose = func(s string) { verbose = append(verbose, s) }
	stats := &perf.GrainTreatmentStats{SampleChunks: []int{2, 4}}
	rng := rand.New(rand.NewSource(82))
	observed := 0
	measure := func(_ context.Context, ch chunk.Chunk, observe func(int, []byte, []byte) error) (float32, error) {
		src, dst := make([]byte, w*h*3), make([]byte, w*h*3)
		for frame := range ch.Frames() {
			for i := range len(src) / 2 {
				clean := 512
				if i < w*h {
					clean = 160 + 640*(i%w)/w
				}
				n := rng.NormFloat64() * 8
				binary.LittleEndian.PutUint16(dst[2*i:], uint16(clean))
				binary.LittleEndian.PutUint16(src[2*i:], uint16(math.Round(float64(clean)+n)))
			}
			if err := observe(frame, src, dst); err != nil {
				return 0, err
			}
			observed++
		}
		return float32(ch.Idx), nil
	}
	estimate, err := estimateGrainFromPairs(context.Background(), in, stats, measure)
	if err != nil {
		t.Fatal(err)
	}
	if !stats.CeilingMeasured || *stats.DenoiseCeilingJODMean != 3 || *stats.DenoiseCeilingJODMin != 2 || len(verbose) != 2 || observed != 16 || len(estimate.Frames) != 8 || estimate.Table == "" {
		t.Fatalf("grain estimate %+v, stats %+v, pairs %d, messages %v", estimate, stats, observed, verbose)
	}
}

func TestGrainPairEstimatorFailsClosedOnIncompletePairs(t *testing.T) {
	in := GrainGateInput{Info: &video.Info{Width: 512, Height: 256}, Chunks: []chunk.Chunk{{Idx: 2, Start: 0, End: 8}}}
	stats := &perf.GrainTreatmentStats{SampleChunks: []int{3}}
	measure := func(context.Context, chunk.Chunk, func(int, []byte, []byte) error) (float32, error) {
		t.Fatal("measured missing chunk")
		return 0, nil
	}
	if _, err := estimateGrainFromPairs(context.Background(), in, stats, measure); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing chunk: %v", err)
	}
	stats.SampleChunks[0] = 2
	sentinel := errors.New("pair failed")
	if _, err := estimateGrainFromPairs(context.Background(), in, stats, func(context.Context, chunk.Chunk, func(int, []byte, []byte) error) (float32, error) {
		return 0, sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("pair failure: %v", err)
	}
	if stats.CeilingMeasured {
		t.Fatal("failed analysis published ceiling")
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := estimateGrainFromPairs(ctx, in, stats, func(context.Context, chunk.Chunk, func(int, []byte, []byte) error) (float32, error) {
		cancel()
		return 9, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := estimateGrainFromPairs(context.Background(), in, stats, func(context.Context, chunk.Chunk, func(int, []byte, []byte) error) (float32, error) { return 9, nil }); err == nil {
		t.Fatal("model without frames succeeded")
	}
	in.Info.Width = 0
	if _, err := estimateGrainFromPairs(context.Background(), in, stats, measure); err == nil {
		t.Fatal("invalid dimensions accepted")
	}
}
