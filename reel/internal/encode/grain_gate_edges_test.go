package encode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/config"
	"github.com/five82/spindle/reel/internal/video"
)

func TestGrainGateWithoutEncodeSamples(t *testing.T) {
	dir := t.TempDir()
	cfg := &EncodeConfig{}
	for _, tc := range []struct {
		name   string
		width  uint32
		chunks []chunk.Chunk
		reason string
	}{
		{"SD", 640, []chunk.Chunk{{Idx: 0, Start: 0, End: 100}}, "SD sources are never treated"},
		{"no samples", 1920, nil, "no chunk long enough to measure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stats, err := runGrainGate(context.Background(), cfg, GrainGateInput{Info: &video.Info{Width: tc.width, Height: 1080, Frames: 100}, Chunks: tc.chunks, WorkDir: dir})
			if err != nil || stats == nil || stats.Reason != tc.reason || stats.Treated {
				t.Fatalf("gate: %+v, %v", stats, err)
			}
		})
	}
	// A blocked gate work directory fails before the first measurement.
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	in := GrainGateInput{Info: &video.Info{Width: 1920, Height: 1080, Frames: 200}, Chunks: []chunk.Chunk{{Idx: 0, Start: 0, End: 200}}, WorkDir: blocked}
	if _, err := runGrainGate(context.Background(), cfg, in); err == nil || !strings.Contains(err.Error(), "grain gate directory") {
		t.Fatalf("blocked gate: %v", err)
	}
	in.WorkDir = dir
	in.InputPath = filepath.Join(dir, "missing.y4m")
	if _, err := runGrainGate(context.Background(), cfg, in); err == nil || !strings.Contains(err.Error(), "open source") {
		t.Fatalf("missing source: %v", err)
	}
	if _, err := measureChunkBPP(context.Background(), cfg, in, in.Chunks[0], filepath.Join(dir, "test.ivf"), 1920, 1080); err == nil {
		t.Fatal("missing source must fail")
	}
	if _, err := ResolveGrainTreatment(context.Background(), config.GrainTreatmentAuto, cfg, in); err == nil {
		t.Fatal("grain gate error must propagate")
	}
}
