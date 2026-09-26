package video

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecoderSequentialAndBackwardReads(t *testing.T) {
	path := writeTestY4M(t, 16, 16, 10)
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	src, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	buf := make([]byte, FrameSize(info, nil))
	read := func(index int) []byte {
		t.Helper()
		if err := src.FrameReader(info, nil).ReadFrame(index, buf); err != nil {
			t.Fatalf("frame %d: %v", index, err)
		}
		return bytes.Clone(buf)
	}
	first := read(0)
	for i := 1; i < 10; i++ {
		read(i)
	}
	if got := read(0); !bytes.Equal(got, first) {
		t.Fatal("backward seek changed first frame")
	}
	if got := read(0); !bytes.Equal(got, first) {
		t.Fatal("repeated frame read changed first frame")
	}
	if _, err := src.ReadLumaFrameNear(-1, info, 0); err == nil || !strings.Contains(err.Error(), "negative frame") {
		t.Fatalf("negative near index: %v", err)
	}
	if _, err := src.ReadLumaFrameNear(10000, info, 0); err == nil {
		t.Fatal("past-EOF near read should fail")
	}
}

func TestDecoderFilterRejectsInvalidGraph(t *testing.T) {
	path := writeTestY4M(t, 16, 16, 4)
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	src, err := OpenFiltered(path, 1, "no_such_filter")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := src.ReadFrame(0, make([]byte, FrameSize(info, nil)), info, nil); err == nil || !strings.Contains(err.Error(), "failed to build denoise filter") {
		t.Fatalf("invalid filter: %v", err)
	}
	src.ResetFilter()
	(*Source)(nil).ResetFilter()
	(*Source)(nil).Close()
}

func TestProbeMissingAndEmptyVideo(t *testing.T) {
	if _, err := Probe(filepath.Join(t.TempDir(), "absent.y4m")); err == nil {
		t.Fatal("missing video must fail")
	}
	path := filepath.Join(t.TempDir(), "empty.y4m")
	if err := os.WriteFile(path, []byte("YUV4MPEG2 W16 H16 F25:1 Ip A1:1 C420\n"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Frames != 0 || info.Width != 16 || info.Height != 16 {
		t.Fatalf("empty video: %+v", info)
	}
}
