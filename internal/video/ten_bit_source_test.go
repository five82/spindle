package video

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestTenBitY4MReadAndCropPreserveSamples(t *testing.T) {
	const w, h = 32, 24
	path := filepath.Join(t.TempDir(), "ten-bit.y4m")
	var data bytes.Buffer
	data.WriteString("YUV4MPEG2 W32 H24 F25:1 Ip A1:1 C420p10\n")
	for frame := range 3 {
		data.WriteString("FRAME\n")
		for i := range w * h * 3 / 2 {
			if err := binary.Write(&data, binary.LittleEndian, uint16(100+frame+i%32)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Is10Bit {
		t.Fatalf("depth: %+v", info)
	}
	src, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for _, idx := range []int{2, 1, 0} {
		buf := make([]byte, FrameSize(info, nil))
		if err := src.ReadFrame(idx, buf, info, nil); err != nil {
			t.Fatal(err)
		}
		if got := binary.LittleEndian.Uint16(buf); got != uint16(100+idx) {
			t.Fatalf("frame %d sample %d", idx, got)
		}
	}
	crop := &CropRect{X: 2, Y: 2, Width: 16, Height: 12}
	buf := make([]byte, FrameSize(info, crop))
	if err := src.ReadFrame(1, buf, info, crop); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(buf); got != 103 {
		t.Fatalf("cropped first sample %d", got)
	}
}
