package subtitle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/srtutil"
)

func TestWriteSRTAtomicFailurePreservesDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "movie.en.srt")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeSRTAtomic(path, []srtutil.Cue{}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) == "original" {
		t.Fatalf("new content: %q %v", data, err)
	}
	if err := writeSRTAtomic(filepath.Join(dir, "missing", "movie.srt"), nil); err == nil || !strings.Contains(err.Error(), "create temp file") {
		t.Fatalf("missing parent: %v", err)
	}
	if err := writeSRTAtomic(dir, nil); err == nil || !strings.Contains(err.Error(), "rename temp file") {
		t.Fatalf("directory destination: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".subtitle-") {
			t.Fatalf("temp leaked: %s", e.Name())
		}
	}
}
