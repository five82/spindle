package encoder

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

func TestIVFVideoBytesExcludesHeadersAndRejectsTruncation(t *testing.T) {
	var out bytes.Buffer
	if err := writeIVFHeader(&out, 16, 16, 24, 1); err != nil {
		t.Fatal(err)
	}
	for i, size := range []int{3, 5, 7} {
		if err := writeIVFFrame(&out, bytes.Repeat([]byte{byte(i)}, size), int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	data := out.Bytes()
	got, err := IVFVideoBytes(bytes.NewReader(data))
	if err != nil || got != 15 {
		t.Fatalf("payload = %d, %v", got, err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"header", data[:31], "IVF header"},
		{"partial frame header", data[:33], "frame header"},
		{"partial payload", data[:len(data)-1], "frame data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := IVFVideoBytes(bytes.NewReader(tc.data)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	// EOF at the next frame boundary is not a truncated frame.
	if got, err := IVFVideoBytes(bytes.NewReader(data[:32])); err != nil || got != 0 {
		t.Fatalf("empty IVF = %d, %v", got, err)
	}
}

func TestIVFWritersReportIOErrors(t *testing.T) {
	fail := failingIVFWriter{}
	if err := writeIVFHeader(fail, 2, 2, 1, 1); err == nil {
		t.Fatal("header write succeeded")
	}
	if err := writeIVFFrame(fail, []byte{1}, 0); err == nil {
		t.Fatal("frame write succeeded")
	}
	var out bytes.Buffer
	if err := writeIVFFrame(&out, nil, 42); err != nil || binary.LittleEndian.Uint64(out.Bytes()[4:]) != 42 {
		t.Fatalf("timestamp: %v", err)
	}
}

type failingIVFWriter struct{}

func (failingIVFWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
