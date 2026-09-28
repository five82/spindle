package video

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestDecoderPacketCountAndSeekFailures(t *testing.T) {
	path := writeTestY4M(t, 32, 24, 12)
	src, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if got := src.countVideoPackets(); got != 12 {
		t.Fatalf("packet count = %d", got)
	}
	inf, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, FrameSize(inf, nil))
	if err := src.ReadFrame(0, buf, inf, nil); err != nil {
		t.Fatal(err)
	}
	first := bytes.Clone(buf)
	if err := src.ReadFrame(11, buf, inf, nil); err != nil {
		t.Fatal(err)
	}
	if err := src.ReadFrame(0, buf, inf, nil); err != nil || !bytes.Equal(first, buf) {
		t.Fatalf("read after packet count and backward seek: %v", err)
	}
	if _, err := src.readRawFrame(-1); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("negative index: %v", err)
	}
	if _, err := src.readRawFrameNear(-1, 0); err == nil {
		t.Fatal("negative near index succeeded")
	}
	if _, err := src.readRawFrameNear(3, 0); err != nil {
		t.Fatalf("minimum near decode: %v", err)
	}
	if _, err := src.readRawFrame(99999); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("past EOF: %v", err)
	}
	if _, _, err := src.decodeOne(); err != io.EOF {
		t.Fatalf("decode after EOF: %v", err)
	}
}

func TestDecoderEmptyInputHasNoFrames(t *testing.T) {
	for _, frames := range []int{0, 1} {
		path := writeTestY4M(t, 16, 16, frames)
		src, err := Open(path, 1)
		if frames == 0 {
			if err == nil {
				defer src.Close()
				inf := &Info{Width: 16, Height: 16}
				if err := src.ReadFrame(0, make([]byte, FrameSize(inf, nil)), inf, nil); err == nil {
					t.Fatal("empty video decoded a frame")
				}
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		src.Close()
	}
}
