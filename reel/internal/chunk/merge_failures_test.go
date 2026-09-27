package chunk

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/video"
)

func TestAppendIVFRejectsCorruptionAndWriteErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chunk.ivf")
	if err := writeTestIVF(path, 1, []byte("frame")); err != nil {
		t.Fatal(err)
	}
	valid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	failWrite := writerFunc(func([]byte) (int, error) { return 0, errors.New("sink failed") })
	for _, tc := range []struct {
		name string
		data []byte
		out  io.Writer
		want string
	}{
		{"short header", valid[:3], io.Discard, "read IVF header"},
		{"bad signature", append([]byte("NOPE"), valid[4:]...), io.Discard, "invalid IVF header"},
		{"short frame header", valid[:35], io.Discard, "read IVF frame header"},
		{"short payload", valid[:len(valid)-1], io.Discard, "append IVF frame"},
		{"failed output header", valid, failWrite, "write IVF header"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, tc.data, 0600); err != nil {
				t.Fatal(err)
			}
			index := uint32(0)
			if err := appendIVF(tc.out, path, true, 1, &index); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("appendIVF() = %v, want %q", err, tc.want)
			}
		})
	}
	index := uint32(0)
	if err := appendIVF(io.Discard, filepath.Join(dir, "missing.ivf"), true, 1, &index); err == nil || !strings.Contains(err.Error(), "open IVF chunk") {
		t.Fatalf("missing chunk: %v", err)
	}
}

func TestMergeOutputRejectsWrongFrameCount(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureEncodeDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := writeTestIVF(IVFPath(dir, 0), 1, []byte("frame")); err != nil {
		t.Fatal(err)
	}
	for _, frames := range []int{-1, 2} {
		if err := MergeOutput(dir, &video.Info{Frames: frames}, 1); err == nil {
			t.Fatalf("merged one frame as %d", frames)
		}
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(buf []byte) (int, error) { return f(buf) }
