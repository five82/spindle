package encoder

import (
	"context"
	"encoding/binary"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/reel/internal/video"
)

// encodeNoiseIVF encodes deterministic high-entropy 10-bit noise frames and
// returns the resulting IVF bytes. Fresh noise per frame leaves the encoder
// nothing to predict, so bitrate demand at CRF 1 far exceeds the cap.
func encodeNoiseIVF(t *testing.T, width, height uint32, frames int) []byte {
	t.Helper()

	out := filepath.Join(t.TempDir(), "noise.ivf")
	cfg := &EncConfig{
		Inf:    &video.Info{FPSNum: 60, FPSDen: 1},
		CRF:    1,
		Preset: 12,
		Output: out,
		Width:  width,
		Height: height,
		Frames: frames,
	}

	// Mid-gray with moderate noise: demanding at CRF 1 (well above the cap)
	// yet compressible at high QP (well below it), so a working cap lands the
	// bitrate near the cap value itself rather than at a QP-ceiling floor.
	rng := rand.New(rand.NewSource(1))
	readFrame := func(buf []byte) error {
		for i := 0; i+1 < len(buf); i += 2 {
			binary.LittleEndian.PutUint16(buf[i:], uint16(448+rng.Intn(128)))
		}
		return nil
	}

	if err := EncodeChunkToIVF(context.Background(), cfg, readFrame, nil); err != nil {
		t.Fatalf("EncodeChunkToIVF: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read IVF: %v", err)
	}
	return data
}

// seqLevelIdx extracts seq_level_idx of the first operating point from the
// first sequence header OBU in an IVF file.
func seqLevelIdx(t *testing.T, ivf []byte) int {
	t.Helper()

	const ivfHeaderSize, ivfFrameHeaderSize = 32, 12
	if len(ivf) < ivfHeaderSize+ivfFrameHeaderSize {
		t.Fatal("IVF too short")
	}
	frameSize := binary.LittleEndian.Uint32(ivf[ivfHeaderSize:])
	frame := ivf[ivfHeaderSize+ivfFrameHeaderSize:]
	if uint32(len(frame)) < frameSize {
		t.Fatal("truncated IVF frame")
	}
	frame = frame[:frameSize]

	// Walk OBUs to the sequence header (type 1).
	for len(frame) > 0 {
		obuType := (frame[0] >> 3) & 0xF
		hasSize := frame[0]&0x02 != 0
		if !hasSize {
			t.Fatal("OBU without size field")
		}
		// LEB128 size.
		size, n := 0, 1
		for shift := 0; ; shift += 7 {
			b := frame[n]
			n++
			size |= int(b&0x7F) << shift
			if b&0x80 == 0 {
				break
			}
		}
		payload := frame[n : n+size]
		if obuType == 1 {
			return parseSeqLevel(t, payload)
		}
		frame = frame[n+size:]
	}
	t.Fatal("no sequence header OBU found")
	return -1
}

func parseSeqLevel(t *testing.T, payload []byte) int {
	t.Helper()

	pos := 0
	read := func(n int) int {
		v := 0
		for range n {
			v = v<<1 | int(payload[pos>>3]>>(7-pos&7))&1
			pos++
		}
		return v
	}
	read(3) // seq_profile
	read(1) // still_picture
	if read(1) == 1 {
		t.Fatal("unexpected reduced_still_picture_header")
	}
	if read(1) == 1 {
		t.Fatal("unexpected timing_info_present_flag")
	}
	read(1)        // initial_display_delay_present_flag
	read(5)        // operating_points_cnt_minus_1
	read(12)       // operating_point_idc[0]
	return read(5) // seq_level_idx[0]
}

func TestEncodeSignalsLevel52(t *testing.T) {
	ivf := encodeNoiseIVF(t, 64, 64, 8)
	if got := seqLevelIdx(t, ivf); got != 14 {
		t.Errorf("seq_level_idx = %d, want 14 (level 5.2)", got)
	}
}

func TestMaxBitRateCapsBitstream(t *testing.T) {
	const width, height, frames = 512, 512, 120 // 2s at 60fps

	capped := len(encodeNoiseIVF(t, width, height, frames))

	orig := maxBitRateBps
	maxBitRateBps = 0
	defer func() { maxBitRateBps = orig }()
	uncapped := len(encodeNoiseIVF(t, width, height, frames))

	cappedMbps := float64(capped) * 8 / 2 / 1e6
	uncappedMbps := float64(uncapped) * 8 / 2 / 1e6
	t.Logf("capped %.1f Mbps, uncapped %.1f Mbps", cappedMbps, uncappedMbps)

	// Noise at CRF 1 must overwhelm the 60 Mbps cap for this test to mean
	// anything; if this fires, make the content more demanding.
	if uncappedMbps < 120 {
		t.Fatalf("uncapped baseline only %.1f Mbps; test content not demanding enough", uncappedMbps)
	}
	if capped >= uncapped/2 {
		t.Errorf("capped encode (%.1f Mbps) not meaningfully below uncapped (%.1f Mbps)", cappedMbps, uncappedMbps)
	}
	// The achieved rate must track the cap value, not a QP-ceiling floor:
	// this pins max_bit_rate's units as bits/second (a kbps misread would
	// crush the output to single-digit Mbps). The regulator lands anywhere
	// from ~50% to ~130% of the cap depending on platform and threading
	// (observed at the old 40 Mbps cap: ~20 Mbps on an ARM LOP-3 CI runner,
	// ~39 Mbps locally), so the floor only needs to clear the single-digit
	// kbps-misread case.
	if cappedMbps < 18 || cappedMbps > 80 {
		t.Errorf("capped encode %.1f Mbps not tracking the 60 Mbps cap", cappedMbps)
	}
}
