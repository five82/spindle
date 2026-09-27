package quality

import (
	"context"
	"strings"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/video"
)

func TestChunkMetricRejectsInvalidInputsBeforeDecode(t *testing.T) {
	ctx := context.Background()
	info := &video.Info{Width: 16, Height: 16}
	cv := CVVDPOptions{Processor: &VshipProcessor{}, Info: info, Width: 16, Height: 16, SourcePath: "missing.y4m", ProbePath: "missing.ivf", Chunk: chunk.Chunk{End: 1}}
	ss := SSIMU2Options{Processor: &SSIMU2Processor{}, Info: info, Width: 16, Height: 16, SourcePath: "missing.y4m", ProbePath: "missing.ivf", Chunk: chunk.Chunk{End: 1}}
	for _, tc := range []struct {
		name, part string
		cv         func() error
		ss         func() error
	}{
		{"nil processor", "nil", func() error { o := cv; o.Processor = nil; _, e := ComputeChunkCVVDP(ctx, o); return e }, func() error { o := ss; o.Processor = nil; _, e := ComputeChunkSSIMU2(ctx, o); return e }},
		{"nil info", "nil video info", func() error { o := cv; o.Info = nil; _, e := ComputeChunkCVVDP(ctx, o); return e }, func() error { o := ss; o.Info = nil; _, e := ComputeChunkSSIMU2(ctx, o); return e }},
		{"invalid width", "dimensions", func() error { o := cv; o.Width = 0; _, e := ComputeChunkCVVDP(ctx, o); return e }, func() error { o := ss; o.Width = 0; _, e := ComputeChunkSSIMU2(ctx, o); return e }},
		{"missing reference", "failed to open source", func() error { _, e := ComputeChunkCVVDP(ctx, cv); return e }, func() error { _, e := ComputeChunkSSIMU2(ctx, ss); return e }},
		{"missing probe", "failed to probe", func() error { o := cv; o.Reference = errorReader{}; _, e := ComputeChunkCVVDP(ctx, o); return e }, func() error { o := ss; o.Reference = errorReader{}; _, e := ComputeChunkSSIMU2(ctx, o); return e }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, f := range []func() error{tc.cv, tc.ss} {
				if err := f(); err == nil || !strings.Contains(err.Error(), tc.part) {
					t.Fatalf("error = %v, want %q", err, tc.part)
				}
			}
		})
	}
	for _, tc := range []struct {
		name   string
		change func(*DenoiseCeilingOptions)
		want   string
	}{
		{"nil processor", func(o *DenoiseCeilingOptions) { o.Processor = nil }, "nil VSHIP"},
		{"nil info", func(o *DenoiseCeilingOptions) { o.Info = nil }, "nil video info"},
		{"no filter", func(o *DenoiseCeilingOptions) { o.Denoise = "" }, "denoise filter"},
		{"missing source", func(*DenoiseCeilingOptions) {}, "failed to open source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := DenoiseCeilingOptions{Processor: &VshipProcessor{}, Info: info, Denoise: "null", SourcePath: "missing.y4m"}
			tc.change(&o)
			_, err := ComputeChunkDenoiseCeiling(ctx, o)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

type errorReader struct{}

func (errorReader) ReadFrame(int, []byte) error { return nil }
