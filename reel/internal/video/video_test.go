package video

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFrameTo10BitApplies8BitCropOffsets(t *testing.T) {
	inf := &Info{Width: 6, Height: 4, Is10Bit: false}
	crop := &CropCalc{NewW: 4, NewH: 2, CropX: 2, CropY: 2}

	y := make8BitPlane(6, 4, 8, 0)
	u := make8BitPlane(3, 2, 5, 100)
	v := make8BitPlane(3, 2, 6, 200)
	out := make([]byte, Calc10BitSize(crop.NewW, crop.NewH))

	err := copyFrameTo10Bit(out, [3][]byte{y, u, v}, [3]int{8, 5, 6}, inf, crop, false)
	if err != nil {
		t.Fatalf("copyFrameTo10Bit returned error: %v", err)
	}

	got := readU16Samples(out)
	want8Bit := []uint16{
		22, 23, 24, 25,
		32, 33, 34, 35,
		111, 112,
		211, 212,
	}
	want := make([]uint16, len(want8Bit))
	for i, v := range want8Bit {
		want[i] = v << 2
	}
	assertSamples(t, got, want)
}

func TestCopyFrameTo10BitApplies10BitCropOffsets(t *testing.T) {
	inf := &Info{Width: 6, Height: 4, Is10Bit: true}
	crop := &CropCalc{NewW: 4, NewH: 2, CropX: 2, CropY: 2}

	y := make10BitPlane(6, 4, 16, 0)
	u := make10BitPlane(3, 2, 10, 100)
	v := make10BitPlane(3, 2, 12, 200)
	out := make([]byte, Calc10BitSize(crop.NewW, crop.NewH))

	err := copyFrameTo10Bit(out, [3][]byte{y, u, v}, [3]int{16, 10, 12}, inf, crop, true)
	if err != nil {
		t.Fatalf("copyFrameTo10Bit returned error: %v", err)
	}

	want := []uint16{
		22, 23, 24, 25,
		32, 33, 34, 35,
		111, 112,
		211, 212,
	}
	assertSamples(t, readU16Samples(out), want)
}

func TestValidateCropRectRejectsInvalidYUV420Crop(t *testing.T) {
	inf := &Info{Width: 1920, Height: 1080}
	err := ValidateCropRect(inf, CropRect{X: 1, Y: 0, Width: 1918, Height: 1080})
	if err == nil {
		t.Fatal("expected odd crop offset to be rejected")
	}
}

func TestCropCalcForRectSupportsAsymmetricCrop(t *testing.T) {
	inf := &Info{Width: 1920, Height: 1080, Is10Bit: true}
	crop := cropCalcForRect(inf, &CropRect{X: 4, Y: 8, Width: 1900, Height: 1060})
	if crop == nil {
		t.Fatal("expected crop calculation")
	}
	if crop.NewW != 1900 || crop.NewH != 1060 || crop.CropX != 4 || crop.CropY != 8 {
		t.Fatalf("unexpected crop calc: %+v", crop)
	}
}

func TestFrameCountFromDurations(t *testing.T) {
	streamFrames := framesFromStreamDuration(8044036, 1, 1000, 24000, 1001)
	if streamFrames != 192864 {
		t.Fatalf("framesFromStreamDuration() = %d, want 192864", streamFrames)
	}

	formatFrames := framesFromAVDuration(8044036000, 24000, 1001)
	if formatFrames != 192864 {
		t.Fatalf("framesFromAVDuration() = %d, want 192864", formatFrames)
	}

	if got := framesFromAVDuration(0, 24000, 1001); got != 0 {
		t.Fatalf("framesFromAVDuration() with unknown duration = %d, want 0", got)
	}
}

func TestSVTMetadataMapping(t *testing.T) {
	assertInt32Ptr(t, svtColorValue(1), 1)
	assertInt32Ptr(t, svtColorValue(2), 1)
	assertInt32Ptr(t, svtColorValue(0), 1)
	assertInt32Ptr(t, svtColorValue(2, 9), 9)
	assertInt32Ptr(t, svtColorValue(3, 9), 9)

	assertInt32Ptr(t, svtColorRange(1), 0)
	assertInt32Ptr(t, svtColorRange(2), 1)
	assertInt32Ptr(t, svtColorRange(0), 0)
	assertInt32Ptr(t, svtColorRange(0, 2), 1)

	assertInt32Ptr(t, svtChromaSamplePosition(1), 1)
	assertInt32Ptr(t, svtChromaSamplePosition(3), 2)
	assertInt32Ptr(t, svtChromaSamplePosition(0), 1)
	assertInt32Ptr(t, svtChromaSamplePosition(0, 3), 2)
	assertNil(t, svtChromaSamplePosition(2))
}

func make8BitPlane(width, height, stride int, base byte) []byte {
	plane := make([]byte, stride*height)
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			plane[row*stride+col] = base + byte(row*10+col)
		}
	}
	return plane
}

func make10BitPlane(width, height, stride int, base uint16) []byte {
	plane := make([]byte, stride*height)
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			binary.LittleEndian.PutUint16(plane[row*stride+col*2:], base+uint16(row*10+col))
		}
	}
	return plane
}

func readU16Samples(buf []byte) []uint16 {
	samples := make([]uint16, len(buf)/2)
	for i := range samples {
		samples[i] = binary.LittleEndian.Uint16(buf[i*2:])
	}
	return samples
}

func assertSamples(t *testing.T, got, want []uint16) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d samples, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d = %d, want %d\ngot:  %v\nwant: %v", i, got[i], want[i], got, want)
		}
	}
}

func assertInt32Ptr(t *testing.T, got *int32, want int32) {
	t.Helper()
	if got == nil {
		t.Fatalf("got nil, want %d", want)
	}
	if *got != want {
		t.Fatalf("got %d, want %d", *got, want)
	}
}

func assertNil(t *testing.T, got *int32) {
	t.Helper()
	if got != nil {
		t.Fatalf("got %d, want nil", *got)
	}
}

// TestDenoiseFilterResetMatchesFreshSource proves the temporal-state contract
// the denoise experiment depends on: the encoder worker (one Source reused
// across consecutive chunks, ResetFilter at chunk start) and the metric scorers
// (a fresh Source per chunk) must produce bit-identical filtered frames.
func TestDenoiseFilterResetMatchesFreshSource(t *testing.T) {
	const (
		width  = 32
		height = 32
		frames = 12
		start  = 6
		count  = 4
		filter = "hqdn3d=8:6:12:9"
	)
	path := writeTestY4M(t, width, height, frames)

	inf, err := Probe(path)
	if err != nil {
		t.Fatalf("Probe() error: %v", err)
	}

	// Encoder-worker pattern: sequential reads across the earlier chunk, then a
	// reset at the chunk boundary.
	reused := readFiltered(t, path, filter, func(src *Source) [][]byte {
		for i := 0; i < start; i++ {
			readInto(t, src, i, inf)
		}
		src.ResetFilter()
		return readRange(t, src, inf, start, count)
	})

	// Scorer pattern: a fresh Source that starts at the chunk boundary.
	fresh := readFiltered(t, path, filter, func(src *Source) [][]byte {
		src.ResetFilter()
		return readRange(t, src, inf, start, count)
	})

	for i := range fresh {
		if string(reused[i]) != string(fresh[i]) {
			t.Fatalf("frame %d differs between reset-at-chunk-start and fresh source", start+i)
		}
	}

	// Control: without the reset the carried filter history must change the
	// output, otherwise the test above would pass on a stateless filter.
	carried := readFiltered(t, path, filter, func(src *Source) [][]byte {
		for i := 0; i < start; i++ {
			readInto(t, src, i, inf)
		}
		return readRange(t, src, inf, start, count)
	})
	if string(carried[0]) == string(fresh[0]) {
		t.Fatal("carried temporal filter state produced identical output; the test cannot detect a reset bug")
	}
}

func readFiltered(t *testing.T, path, filter string, fn func(*Source) [][]byte) [][]byte {
	t.Helper()
	src, err := OpenFiltered(path, 1, filter)
	if err != nil {
		t.Fatalf("OpenFiltered() error: %v", err)
	}
	defer src.Close()
	return fn(src)
}

func readRange(t *testing.T, src *Source, inf *Info, start, count int) [][]byte {
	t.Helper()
	out := make([][]byte, count)
	for i := range out {
		out[i] = readInto(t, src, start+i, inf)
	}
	return out
}

func readInto(t *testing.T, src *Source, idx int, inf *Info) []byte {
	t.Helper()
	buf := make([]byte, FrameSize(inf, nil))
	if err := src.ReadFrame(idx, buf, inf, nil); err != nil {
		t.Fatalf("ReadFrame(%d) error: %v", idx, err)
	}
	return buf
}

// writeTestY4M writes a deterministic noisy YUV4MPEG2 clip. Raw Y4M keeps the
// test self-contained: no encoder, no external ffmpeg binary.
func writeTestY4M(t *testing.T, width, height, frames int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.y4m")
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "YUV4MPEG2 W%d H%d F25:1 Ip A1:1 C420\n", width, height)
	rnd := rand.New(rand.NewSource(1))
	for f := 0; f < frames; f++ {
		buf.WriteString("FRAME\n")
		for range height {
			for x := range width {
				buf.WriteByte(byte(16 + (x*4+f*3)%180 + rnd.Intn(40)))
			}
		}
		for range (height / 2) * (width / 2) * 2 {
			buf.WriteByte(byte(96 + rnd.Intn(64)))
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("failed to write test clip: %v", err)
	}
	return path
}
