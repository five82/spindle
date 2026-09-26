package encoder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/video"
)

func TestSVTReportsLinkedCapabilitiesAndRejectsBadSettings(t *testing.T) {
	if SVTVersion() == "" {
		t.Fatal("empty SVT library version")
	}
	_ = FGSTableSupported()
	if MaxBitRateBps() == 0 {
		t.Fatal("encoder bitrate ceiling is zero")
	}
	cfg := &EncConfig{Inf: &video.Info{FPSNum: 25, FPSDen: 1}, Width: 32, Height: 32, Frames: 1, CRF: 30, Preset: 12}
	for _, tc := range []struct {
		name   string
		change func(*EncConfig)
		want   string
	}{
		{"preset", func(c *EncConfig) { c.Preset = 255 }, "preset"},
		{"grain table", func(c *EncConfig) { path := filepath.Join(t.TempDir(), "missing.tbl"); c.GrainTable = &path }, "film grain table"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := *cfg
			tc.change(&invalid)
			enc, err := newSvtEncoder(&invalid)
			if enc != nil {
				enc.close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("bad config: %v", err)
			}
		})
	}
}

func TestEncodeChunkRejectsBadInputsWithoutLeavingPartialOutput(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "bad.ivf")
	cfg := &EncConfig{Inf: &video.Info{FPSNum: 25, FPSDen: 1}, Output: output, Width: 32, Height: 32, CRF: 30, Preset: 12}
	if err := EncodeChunkToIVF(context.Background(), cfg, nil, nil); err == nil || !strings.Contains(err.Error(), "no frames") {
		t.Fatalf("empty encode: %v", err)
	}
	cfg.Frames = 1
	badInput := errors.New("failed to supply frame")
	err := EncodeChunkToIVF(context.Background(), cfg, func([]byte) error { return badInput }, nil)
	if !errors.Is(err, badInput) {
		t.Fatalf("frame reader: %v", err)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial file retained: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := EncodeChunkToIVF(ctx, cfg, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled encode: %v", err)
	}
	cfg.Output = filepath.Join(dir, "nonexistent", "bad.ivf")
	if err := EncodeChunkToIVF(context.Background(), cfg, nil, nil); err == nil || !strings.Contains(err.Error(), "create output") {
		t.Fatalf("missing directory: %v", err)
	}
}
