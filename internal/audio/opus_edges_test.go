package audio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpusEncoderLifecycleAndErrors(t *testing.T) {
	if err := loadOpusenc(); err != nil {
		t.Skipf("libopusenc unavailable: %v", err)
	}
	if err := (*opusEncoder)(nil).close(); err != nil {
		t.Fatal(err)
	}
	var empty opusEncoder
	empty.destroy()
	if err := empty.close(); err != nil {
		t.Fatal(err)
	}
	// A nonexistent parent exercises the native file creation error path.
	if _, err := newOpusEncoder(filepath.Join(t.TempDir(), "missing", "audio.opus"), 2, 128); err == nil || !strings.Contains(err.Error(), "opus encoder failed") {
		t.Fatalf("invalid output: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "audio.opus")
	enc, err := newOpusEncoder(path, 2, 128)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.writeFloat(nil, 2); err != nil {
		t.Fatal(err)
	}
	// Ten milliseconds of silence verify sample count and drain; this is not a
	// video encode and does not depend on a GPU or external CLI.
	if err := enc.writeFloat(make([]float32, 480*2), 2); err != nil {
		t.Fatal(err)
	}
	if err := enc.close(); err != nil {
		t.Fatal(err)
	}
	if err := enc.close(); err != nil {
		t.Fatal(err)
	}
	if stat, err := os.Stat(path); err != nil || stat.Size() == 0 {
		t.Fatalf("opus file: %v, %v", stat, err)
	}
}
