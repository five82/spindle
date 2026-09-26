//go:build cgo && !no_vship

package quality

import (
	"context"
	"strings"
	"testing"

	"github.com/five82/reel/internal/video"
)

func TestChunkScorerRejectsInvalidInputs(t *testing.T) {
	if _, err := NewChunkScorer(MetricKind("unknown"), 2, 2, nil, ""); err == nil || !strings.Contains(err.Error(), "unknown probe metric") {
		t.Fatalf("invalid metric: %v", err)
	}
	cv := &cvvdpChunkScorer{proc: &VshipProcessor{closed: true}}
	if _, _, err := cv.ScoreChunk(context.Background(), ChunkScoreRequest{}); err == nil {
		t.Fatal("missing video info")
	}
	if err := cv.Close(); err != nil {
		t.Fatal(err)
	}
	ss := &ssimu2ChunkScorer{proc: &SSIMU2Processor{closed: true}}
	if _, _, err := ss.ScoreChunk(context.Background(), ChunkScoreRequest{}); err == nil {
		t.Fatal("missing video info")
	}
	if err := ss.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMetricEntryRejectsInvalidInputs(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		cv   CVVDPOptions
		want string
	}{
		{"nil processor", CVVDPOptions{}, "nil VSHIP processor"},
		{"nil info", CVVDPOptions{Processor: &VshipProcessor{}}, "nil video info"},
		{"zero width", CVVDPOptions{Processor: &VshipProcessor{}, Info: &video.Info{}, Height: 2}, "invalid CVVDP dimensions"},
		{"missing source", CVVDPOptions{Processor: &VshipProcessor{}, Info: &video.Info{}, Width: 2, Height: 2, SourcePath: "/nonexistent-source"}, "failed to open source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ComputeChunkCVVDP(ctx, tc.cv)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CVVDP: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		opts SSIMU2Options
		want string
	}{
		{SSIMU2Options{}, "nil SSIMU2 processor"},
		{SSIMU2Options{Processor: &SSIMU2Processor{}}, "nil video info"},
		{SSIMU2Options{Processor: &SSIMU2Processor{}, Info: &video.Info{}}, "invalid SSIMU2 dimensions"},
		{SSIMU2Options{Processor: &SSIMU2Processor{}, Info: &video.Info{}, Width: 2, Height: 2, SourcePath: "/nonexistent-source"}, "failed to open source"},
	} {
		_, err := ComputeChunkSSIMU2(ctx, tc.opts)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("SSIMU2: %v", err)
		}
	}
	for _, tc := range []struct {
		opts DenoiseCeilingOptions
		want string
	}{
		{DenoiseCeilingOptions{}, "nil VSHIP processor"},
		{DenoiseCeilingOptions{Processor: &VshipProcessor{}}, "nil video info"},
		{DenoiseCeilingOptions{Processor: &VshipProcessor{}, Info: &video.Info{}}, "requires a denoise filter"},
		{DenoiseCeilingOptions{Processor: &VshipProcessor{}, Info: &video.Info{}, Denoise: "hqdn3d", SourcePath: "/nonexistent-source"}, "failed to open source"},
	} {
		_, err := ComputeChunkDenoiseCeiling(ctx, tc.opts)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("denoise ceiling: %v", err)
		}
	}
}
