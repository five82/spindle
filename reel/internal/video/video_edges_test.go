package video

import (
	"strings"
	"testing"
)

func TestVideoSourceFrameErrorsAndGeometry(t *testing.T) {
	path := writeTestY4M(t, 16, 16, 4)
	inf, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	src, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for _, tc := range []struct {
		idx  int
		want string
	}{{-1, ""}, {10, ""}} {
		if err := src.ReadFrame(tc.idx, make([]byte, FrameSize(inf, nil)), inf, nil); err == nil {
			t.Errorf("frame %d: expected error", tc.idx)
		}
	}
	if _, err := src.ReadLumaFrame(0, inf); err != nil {
		t.Fatalf("read luma: %v", err)
	}
	if _, err := src.ReadLumaFrameNear(2, inf, 3); err != nil {
		t.Fatalf("read near: %v", err)
	}
	var nilSource *Source
	if _, err := nilSource.ReadLumaFrameNear(0, inf, 2); err == nil {
		t.Fatal("nil source")
	}
	if _, err := src.ReadLumaFrameNear(0, nil, 2); err == nil {
		t.Fatal("nil info")
	}
	if err := src.ReadFrame(0, make([]byte, 1), inf, nil); err == nil {
		t.Fatal("short output buffer")
	}
	for _, rect := range []CropRect{{Width: 0, Height: 4}, {X: 16, Width: 2, Height: 4}, {X: 0, Y: 16, Width: 2, Height: 2}, {X: 0, Y: 0, Width: 3, Height: 4}} {
		if err := ValidateCropRect(inf, rect); err == nil {
			t.Errorf("accepted invalid crop %+v", rect)
		}
	}
	if err := ValidateCropRect(inf, CropRect{Width: 16, Height: 16}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCropRect(nil, CropRect{Width: 2, Height: 2}); err == nil {
		t.Fatal("nil info crop")
	}
	if Calc8BitSize(16, 16) != 384 {
		t.Fatal("8 bit size")
	}
}

func TestPlaneBoundsRejectsTruncatedAndInvalidGeometry(t *testing.T) {
	for _, tc := range []struct {
		plane                       []byte
		start, rows, rowLen, stride int
		want                        string
	}{
		{make([]byte, 8), -1, 1, 2, 2, "invalid"},
		{make([]byte, 8), 0, 2, 4, 2, "invalid"},
		{make([]byte, 8), 9, 0, 0, 1, "starts beyond"},
		{make([]byte, 8), 0, 3, 4, 4, "exceeds"},
	} {
		if err := validatePlaneBounds("Y", tc.plane, tc.start, tc.rows, tc.rowLen, tc.stride); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("bounds: %v", err)
		}
	}
	if err := validatePlaneBounds("Y", make([]byte, 8), 8, 0, 0, 1); err != nil {
		t.Fatal(err)
	}
}
