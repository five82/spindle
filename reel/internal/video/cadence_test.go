package video

import (
	"bytes"
	"testing"
)

func TestReadFrameRepeatsPreviousFrameAcrossTimestampHole(t *testing.T) {
	path := writeTestY4M(t, 16, 16, 5)
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := Open(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer baseline.Close()
	want := make([][]byte, 3)
	for i := range want {
		want[i] = make([]byte, FrameSize(info, nil))
		if err := baseline.ReadFrame(i, want[i], info, nil); err != nil {
			t.Fatal(err)
		}
	}

	// The real Y4M timestamps are contiguous. Offset the decoder's timestamp
	// origin by one frame to simulate a missing CFR slot before the first
	// decoded frame, without requiring a VFR-encoded fixture.
	for _, indexes := range [][]int{{0, 1, 2, 3}, {0, 3}} {
		src, err := Open(path, 1)
		if err != nil {
			t.Fatal(err)
		}
		if src.tsMul != src.tsDiv {
			src.Close()
			t.Fatalf("unexpected Y4M timestamp scale: %d/%d", src.tsMul, src.tsDiv)
		}
		src.startTime--
		for _, idx := range indexes {
			buf := make([]byte, FrameSize(info, nil))
			if err := src.ReadFrame(idx, buf, info, nil); err != nil {
				src.Close()
				t.Fatalf("frame %d: %v", idx, err)
			}
			// With the shifted origin, the first decoded frame occupies both
			// the hole at index 0 and its timestamp slot at index 1.
			expected := want[max(0, idx-1)]
			if !bytes.Equal(buf, expected) {
				src.Close()
				t.Fatalf("frame %d did not repeat/advance as expected", idx)
			}
		}
		src.Close()
	}
}
