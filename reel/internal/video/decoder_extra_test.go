package video

import (
	"bytes"
	"strings"
	"testing"
)

func TestDecoderFrameAccessAndLumaSampling(t *testing.T) {
	path := writeTestY4M(t, 32, 24, 16)
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 32 || info.Height != 24 || info.Frames != 16 || info.FPSNum != 25 {
		t.Fatalf("probe: %+v", info)
	}
	src, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	buf := make([]byte, FrameSize(info, nil))
	if err := src.ReadFrame(0, buf, info, nil); err != nil {
		t.Fatal(err)
	}
	first := bytes.Clone(buf)
	for _, idx := range []int{5, 9, 2, 15, 0} {
		if err := src.FrameReader(info, nil).ReadFrame(idx, buf); err != nil {
			t.Fatalf("frame %d: %v", idx, err)
		}
	}
	if !bytes.Equal(first, buf) {
		t.Fatal("backward seek did not recover first frame")
	}
	crop := &CropRect{X: 2, Y: 4, Width: 16, Height: 12}
	cropped := make([]byte, FrameSize(info, crop))
	if err := src.ReadFrame(3, cropped, info, crop); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(cropped[:16], first[:16]) {
		t.Fatal("cropped frame should differ from first frame")
	}
	luma, err := src.ReadLumaFrame(2, info)
	if err != nil || luma.Width != 32 || luma.Height != 24 {
		t.Fatalf("luma: %+v %v", luma, err)
	}
	near, err := src.ReadLumaFrameNear(11, info, 4)
	if err != nil || near.Width != 32 {
		t.Fatalf("near luma: %+v %v", near, err)
	}
}

func TestDecoderInvalidInputs(t *testing.T) {
	if _, err := Probe("/does/not/exist.y4m"); err == nil {
		t.Fatal("missing probe succeeded")
	}
	if _, err := Open("/does/not/exist.y4m", 1); err == nil {
		t.Fatal("missing open succeeded")
	}
	path := writeTestY4M(t, 16, 16, 3)
	src, err := OpenFiltered(path, 1, "no_such_filter")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, FrameSize(info, nil))
	if err := src.ReadFrame(0, buf, info, nil); err == nil {
		t.Fatal("bad filter should fail")
	}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"nil source", (*Source)(nil).ReadFrame(0, buf, info, nil)},
		{"nil info", src.ReadFrame(0, buf, nil, nil)},
		{"nil luma source", func() error { _, e := (*Source)(nil).ReadLumaFrame(0, info); return e }()},
		{"nil luma info", func() error { _, e := src.ReadLumaFrame(0, nil); return e }()},
		{"nil near source", func() error { _, e := (*Source)(nil).ReadLumaFrameNear(0, info, 2); return e }()},
		{"nil near info", func() error { _, e := src.ReadLumaFrameNear(0, nil, 2); return e }()},
	} {
		if tc.err == nil || !strings.Contains(tc.err.Error(), "nil") {
			t.Errorf("%s: %v", tc.name, tc.err)
		}
	}
}
