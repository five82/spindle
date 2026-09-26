package quality

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/video"
)

func TestChunkCVVDPDecodesReferenceAndProbeWithFakeScorer(t *testing.T) {
	path := writePipelineY4M(t, 3)
	info, err := video.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := CVVDPOptions{SourcePath: path, ProbePath: path, Info: info, Chunk: chunk.Chunk{End: 3}, Width: 16, Height: 16}
	scorer := &fakeCVVDP{}
	result, err := computeChunkCVVDP(context.Background(), opts, scorer)
	if err != nil || scorer.calls != 3 || result.Score != 3 || result.Frames != 3 {
		t.Fatalf("CVVDP = %+v, calls %d: %v", result, scorer.calls, err)
	}
	opts.Reference = pipelineReader(func(int, []byte) error { return errors.New("cached reference lost") })
	if _, err := computeChunkCVVDP(context.Background(), opts, &fakeCVVDP{}); err == nil || !strings.Contains(err.Error(), "cached reference lost") {
		t.Fatalf("cached reference: %v", err)
	}
	opts.Reference = nil
	opts.ProbePath = path + ".missing"
	if _, err := computeChunkCVVDP(context.Background(), opts, &fakeCVVDP{}); err == nil || !strings.Contains(err.Error(), "failed to probe") {
		t.Fatalf("probe error: %v", err)
	}
}

func TestDenoiseCeilingPairsUnfilteredAndFilteredFramesWithFakeScorer(t *testing.T) {
	path := writePipelineY4M(t, 3)
	info, err := video.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	observed := 0
	opts := DenoiseCeilingOptions{
		SourcePath: path, Info: info, Chunk: chunk.Chunk{End: 3}, Width: 16, Height: 16, Denoise: "hflip",
		Observe: func(i int, original, denoised []byte) error {
			if i != observed || len(original) != 16*16*3 || len(denoised) != len(original) {
				t.Errorf("pair %d (want %d), sizes %d/%d", i, observed, len(original), len(denoised))
			}
			observed++
			return nil
		},
	}
	scorer := &fakeCVVDP{}
	result, err := computeChunkDenoiseCeiling(context.Background(), opts, scorer)
	if err != nil || observed != 3 || scorer.calls != 3 || result.Frames != 3 {
		t.Fatalf("ceiling = %+v, observed %d, calls %d: %v", result, observed, scorer.calls, err)
	}
	sentinel := errors.New("observation failed")
	opts.Observe = func(int, []byte, []byte) error { return sentinel }
	if _, err := computeChunkDenoiseCeiling(context.Background(), opts, &fakeCVVDP{}); !errors.Is(err, sentinel) {
		t.Fatalf("observer failure: %v", err)
	}
	opts.SourcePath = "missing.y4m"
	if _, err := computeChunkDenoiseCeiling(context.Background(), opts, &fakeCVVDP{}); err == nil || !strings.Contains(err.Error(), "failed to open source") {
		t.Fatalf("source error: %v", err)
	}
}
