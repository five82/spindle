package video

import (
	"bytes"
	"testing"
)

func TestFilteredNonPlanarFramesNormalizeToTenBit(t *testing.T) {
	path := writeTestY4M(t, 32, 24, 4)
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	want := make([]byte, FrameSize(info, nil))
	if err := plain.ReadFrame(0, want, info, nil); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"nv12", "yuv444p"} {
		t.Run(format, func(t *testing.T) {
			src, err := OpenFiltered(path, 1, "format="+format)
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close()
			var first []byte
			for _, idx := range []int{0, 1, 3, 0} {
				buf := make([]byte, len(want))
				if err := src.ReadFrame(idx, buf, info, nil); err != nil {
					t.Fatalf("frame %d: %v", idx, err)
				}
				if idx == 0 {
					if !bytes.Equal(buf[:32*24*2], want[:32*24*2]) {
						t.Fatal("luma changed during pixel format conversion")
					}
					first = buf
				}
			}
			src.ResetFilter()
			buf := make([]byte, len(want))
			if err := src.ReadFrame(0, buf, info, nil); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf, first) {
				t.Fatal("reset did not reproduce converted frame")
			}
		})
	}
}
