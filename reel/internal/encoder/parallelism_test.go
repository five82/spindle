package encoder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/reel/internal/video"
)

// TestLevelOfParallelismBitstreamIdentical verifies that SVT-AV1 produces a
// byte-identical bitstream regardless of the level_of_parallelism value. Reel
// relies on this property to pick lp purely for scheduling/throughput without
// affecting encode results; if it ever stops holding, lp can no longer be
// treated as a free knob.
func TestLevelOfParallelismBitstreamIdentical(t *testing.T) {
	const (
		width  = 320
		height = 240
		frames = 24
	)

	inf := &video.Info{
		FPSNum:  24000,
		FPSDen:  1001,
		Frames:  frames,
		Is10Bit: true,
	}

	// Deterministic synthetic frames: a moving 10-bit gradient with per-frame
	// motion so the encoder exercises inter prediction, not a degenerate
	// all-flat input.
	frameSize := video.Calc10BitSize(width, height)
	makeReadFrame := func() func([]byte) error {
		frameIdx := 0
		return func(buf []byte) error {
			if len(buf) < frameSize {
				t.Fatalf("frame buffer too small: %d < %d", len(buf), frameSize)
			}
			for i := 0; i+1 < frameSize; i += 2 {
				// 10-bit sample in [0,1023), little-endian.
				v := uint16((i/2 + frameIdx*37) % 1000)
				buf[i] = byte(v & 0xff)
				buf[i+1] = byte((v >> 8) & 0x03)
			}
			frameIdx++
			return nil
		}
	}

	// Include every value the auto-scaler can emit (lp 2/3/4, with 3 the 4K
	// default this guards) plus the flag's range endpoints (1 and 6).
	lpValues := []uint32{1, 2, 3, 4, 6}
	dir := t.TempDir()

	var ref string
	var refLP uint32
	for _, lp := range lpValues {
		out := filepath.Join(dir, fmt.Sprintf("lp%d.ivf", lp))
		cfg := &EncConfig{
			Inf:                inf,
			CRF:                30,
			Preset:             8,
			Tune:               0,
			Output:             out,
			Width:              width,
			Height:             height,
			Frames:             frames,
			LevelOfParallelism: lp,
		}
		if err := EncodeChunkToIVF(context.Background(), cfg, makeReadFrame(), nil); err != nil {
			t.Fatalf("encode at lp=%d failed: %v", lp, err)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("read output for lp=%d: %v", lp, err)
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		t.Logf("lp=%d: %d bytes sha256=%s", lp, len(data), got)
		if ref == "" {
			ref, refLP = got, lp
		} else if got != ref {
			t.Errorf("bitstream differs at lp=%d: %s != lp=%d %s", lp, got, refLP, ref)
		}
	}
}

// TestEncodeDeterministic verifies that encoding the same frames twice produces
// byte-identical output. This is the invariant that lets target-quality reuse a
// converged whole-chunk probe verbatim as the final chunk.
func TestEncodeDeterministic(t *testing.T) {
	const (
		width  = 320
		height = 240
		frames = 24
	)

	inf := &video.Info{
		FPSNum:  24000,
		FPSDen:  1001,
		Frames:  frames,
		Is10Bit: true,
	}

	frameSize := video.Calc10BitSize(width, height)
	makeReadFrame := func() func([]byte) error {
		frameIdx := 0
		return func(buf []byte) error {
			if len(buf) < frameSize {
				t.Fatalf("frame buffer too small: %d < %d", len(buf), frameSize)
			}
			for i := 0; i+1 < frameSize; i += 2 {
				v := uint16((i/2 + frameIdx*37) % 1000)
				buf[i] = byte(v & 0xff)
				buf[i+1] = byte((v >> 8) & 0x03)
			}
			frameIdx++
			return nil
		}
	}

	dir := t.TempDir()
	var ref string
	for _, run := range []int{0, 1} {
		out := filepath.Join(dir, fmt.Sprintf("run%d.ivf", run))
		cfg := &EncConfig{
			Inf:    inf,
			CRF:    30,
			Preset: 8,
			Tune:   0,
			Output: out,
			Width:  width,
			Height: height,
			Frames: frames,
		}
		if err := EncodeChunkToIVF(context.Background(), cfg, makeReadFrame(), nil); err != nil {
			t.Fatalf("encode run %d failed: %v", run, err)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("read output for run %d: %v", run, err)
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		t.Logf("run %d: %d bytes sha256=%s", run, len(data), got)
		if ref == "" {
			ref = got
		} else if got != ref {
			t.Errorf("bitstream differs between identical encodes: %s != %s", got, ref)
		}
	}
}
