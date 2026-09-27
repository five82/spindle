package encode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/chunk"
)

func TestRefCacheRejectsTruncatedReadyFile(t *testing.T) {
	cache := newChunkRefCache(t.TempDir(), chunk.Chunk{Start: 20, End: 22}, 6, nil)
	if cache == nil {
		t.Fatal("failed to reserve cache budget")
	}
	defer cache.remove()
	if err := os.MkdirAll(filepath.Dir(cache.path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache.path, make([]byte, 6), 0600); err != nil {
		t.Fatal(err)
	}
	cache.ready = true
	reader := cache.open()
	if reader == nil {
		t.Fatal("expected ready cache reader")
	}
	defer reader.Close()
	if err := reader.ReadFrame(21, make([]byte, 6)); err == nil || !strings.Contains(err.Error(), "failed to read cached frame") {
		t.Fatalf("truncated cached frame: %v", err)
	}
}

func TestRefCacheDropsNonSequentialPass(t *testing.T) {
	cache := newChunkRefCache(t.TempDir(), chunk.Chunk{Start: 4, End: 6}, 4, nil)
	if cache == nil {
		t.Fatal("failed to reserve cache budget")
	}
	defer cache.remove()
	src := frameReaderFunc(func(_ int, output []byte) error {
		copy(output, []byte{1, 2, 3, 4})
		return nil
	})
	writer, done := cache.fill(src)
	if err := writer.ReadFrame(5, make([]byte, 4)); err != nil {
		t.Fatal(err)
	}
	done(true)
	if cache.ready {
		t.Fatal("out-of-order pass cannot produce a ready cache")
	}
	if _, err := os.Stat(cache.path); !os.IsNotExist(err) {
		t.Fatalf("discarded file still present: %v", err)
	}
}

type frameReaderFunc func(int, []byte) error

func (fn frameReaderFunc) ReadFrame(index int, output []byte) error { return fn(index, output) }
