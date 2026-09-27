package chunk

import (
	"encoding/binary"
	"os"
	"strings"
	"testing"

	"github.com/five82/reel/internal/video"
)

func TestMergeOutputConcatenatesIVFAndRewritesFrameCount(t *testing.T) {
	workDir := t.TempDir()
	if err := EnsureEncodeDir(workDir); err != nil {
		t.Fatal(err)
	}
	if err := writeTestIVF(IVFPath(workDir, 0), 2, []byte("chunk0")); err != nil {
		t.Fatal(err)
	}
	if err := writeTestIVF(IVFPath(workDir, 1), 3, []byte("chunk1")); err != nil {
		t.Fatal(err)
	}

	inf := &video.Info{Frames: 5}
	if err := MergeOutput(workDir, inf, 2); err != nil {
		t.Fatal(err)
	}

	merged, err := os.ReadFile(GetVideoPath(workDir))
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(merged[24:28]); got != 5 {
		t.Fatalf("merged frame count = %d, want 5", got)
	}
	if strings.Count(string(merged), "DKIF") != 1 {
		t.Fatalf("merged IVF should contain one header, data = %q", string(merged))
	}
	if !strings.Contains(string(merged), "chunk0") || !strings.Contains(string(merged), "chunk1") {
		t.Fatalf("merged IVF payload missing chunks: %q", string(merged))
	}
	assertIVFTimestamps(t, merged, 5)
}

func assertIVFTimestamps(t *testing.T, data []byte, frames int) {
	t.Helper()
	offset := 32
	for i := range frames {
		frameSize := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
		pts := binary.LittleEndian.Uint64(data[offset+4 : offset+12])
		if pts != uint64(i) {
			t.Fatalf("frame %d timestamp = %d, want %d", i, pts, i)
		}
		offset += 12 + frameSize
	}
	if offset != len(data) {
		t.Fatalf("parsed %d bytes, merged file has %d", offset, len(data))
	}
}

func writeTestIVF(path string, frames uint32, payload []byte) error {
	header := make([]byte, 32)
	copy(header[:4], "DKIF")
	binary.LittleEndian.PutUint16(header[6:8], 32)
	copy(header[8:12], "AV01")
	binary.LittleEndian.PutUint32(header[24:28], frames)
	data := append([]byte{}, header...)
	for i := uint32(0); i < frames; i++ {
		frameHeader := make([]byte, 12)
		binary.LittleEndian.PutUint32(frameHeader[:4], uint32(len(payload)))
		binary.LittleEndian.PutUint64(frameHeader[4:12], uint64(i))
		data = append(data, frameHeader...)
		data = append(data, payload...)
	}
	return os.WriteFile(path, data, 0644)
}
