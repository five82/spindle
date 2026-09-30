package encode

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/chunk"
)

type failingFrameReader struct{ err error }

func (r failingFrameReader) ReadFrame(_ int, _ []byte) error { return r.err }

func TestRefCacheFallsBackAfterFilesystemFailures(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"mkdir", filepath.Join(t.TempDir(), "missing", "cache.yuv")},
		{"create", filepath.Join(t.TempDir(), "cache.yuv")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "mkdir" {
				if err := os.WriteFile(filepath.Dir(tc.path), nil, 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(tc.path, 0700); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			cache := &chunkRefCache{path: tc.path, frameSize: 4, frames: 1, log: slog.New(slog.NewTextHandler(&logs, nil))}
			warnings := func() int { return strings.Count(logs.String(), "event_type=refcache_fallback") }
			src := failingFrameReader{errors.New("source failure")}
			reader, done := cache.fill(src)
			defer done(false)
			if err := reader.ReadFrame(0, make([]byte, 4)); !errors.Is(err, src.err) {
				t.Fatalf("source error lost: %v", err)
			}
			if !cache.disabled || warnings() != 1 {
				t.Fatalf("fallback disabled=%v warnings=%s", cache.disabled, logs.String())
			}
			cache.fail("again")
			if warnings() != 1 {
				t.Fatalf("duplicate warning: %s", logs.String())
			}
		})
	}
}

func TestRefCacheReaderAndWriterFailures(t *testing.T) {
	cache := newChunkRefCache(t.TempDir(), chunk.Chunk{Start: 10, End: 12}, 4, nil)
	if cache == nil {
		t.Fatal("expected cache budget")
	}
	defer cache.remove()
	if cache.open() != nil {
		t.Fatal("cache should not be readable before fill")
	}
	if err := os.MkdirAll(filepath.Dir(cache.path), 0755); err != nil {
		t.Fatal(err)
	}
	cache.ready = true
	if cache.open() != nil || !cache.disabled {
		t.Fatal("missing ready cache must fall back")
	}
	cache.disabled, cache.ready = false, false
	src := failingFrameReader{errors.New("read failed")}
	reader, done := cache.fill(src)
	if err := reader.ReadFrame(10, make([]byte, 4)); !errors.Is(err, src.err) {
		t.Fatalf("writer swallowed source error: %v", err)
	}
	done(false)
	if cache.ready {
		t.Fatal("incomplete cache must not become ready")
	}
	if _, err := os.Stat(cache.path); !os.IsNotExist(err) {
		t.Fatalf("incomplete cache file remains: %v", err)
	}
	// Once disabled, fill returns the source unchanged.
	cache.disabled = true
	if r, finish := cache.fill(src); r != src {
		t.Fatalf("disabled cache wrapped source: %T", r)
	} else {
		finish(false)
	}
}
