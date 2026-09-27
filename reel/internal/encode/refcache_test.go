package encode

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/video"
)

// refCacheTestFilter is temporal, so a cached frame can only match an
// uncached one if the cache really replays what the filter produced.
const refCacheTestFilter = "hqdn3d=8:6:12:9"

// TestRefCacheMatchesFilteredSource is the correctness contract the reference
// cache rests on: the frames a probe or a metric pass reads back from the
// cache must be byte-for-byte the frames it would have decoded and filtered
// itself, otherwise cached and uncached runs would score differently.
func TestRefCacheMatchesFilteredSource(t *testing.T) {
	path := writeTestY4M(t, 32, 32, 16)
	inf, err := video.Probe(path)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	ch := chunk.Chunk{Idx: 3, Start: 4, End: 12}
	frameSize := video.FrameSize(inf, nil)

	// First pass: the probe encode reads the filtered source and fills the cache.
	cache := newChunkRefCache(t.TempDir(), ch, frameSize, nil)
	src := openFiltered(t, path, refCacheTestFilter)
	frames, done := cache.fill(src.FrameReader(inf, nil))
	filtered := readChunk(t, frames, ch, frameSize)
	done(true)
	src.Close()

	if !cache.ready {
		t.Fatal("cache is not ready after a complete pass")
	}

	// Second pass: a repeat probe or the metric reference reads the cache.
	reader := cache.open()
	if reader == nil {
		t.Fatal("cache.open returned no reader")
	}
	cached := readChunk(t, reader, ch, frameSize)
	reader.Close()

	// Control: what an uncached consumer sees, which is what the cache claims
	// to stand in for.
	uncached := openFiltered(t, path, refCacheTestFilter)
	defer uncached.Close()
	fresh := readChunk(t, uncached.FrameReader(inf, nil), ch, frameSize)

	for i := range fresh {
		if !bytes.Equal(cached[i], fresh[i]) {
			t.Fatalf("cached frame %d differs from the filtered source", ch.Start+i)
		}
		if !bytes.Equal(filtered[i], fresh[i]) {
			t.Fatalf("cache-filling pass altered frame %d", ch.Start+i)
		}
	}

	// The filter must actually change the frames, otherwise the comparison
	// above would hold even for a cache that replayed the wrong pixels.
	plain := openFiltered(t, path, "")
	defer plain.Close()
	if bytes.Equal(readChunk(t, plain.FrameReader(inf, nil), ch, frameSize)[0], fresh[0]) {
		t.Fatal("the test filter is a no-op; it cannot detect a cache that bypasses filtering")
	}

	cache.remove()
	if _, err := os.Stat(cache.path); !os.IsNotExist(err) {
		t.Errorf("cache file survived remove: %v", err)
	}
}

// TestRefCacheDiscardsIncompletePass keeps a failed or partial probe from
// leaving frames behind that a later probe would read as complete.
func TestRefCacheDiscardsIncompletePass(t *testing.T) {
	path := writeTestY4M(t, 32, 32, 16)
	inf, err := video.Probe(path)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	ch := chunk.Chunk{Idx: 0, Start: 0, End: 8}
	frameSize := video.FrameSize(inf, nil)

	cache := newChunkRefCache(t.TempDir(), ch, frameSize, nil)
	src := openFiltered(t, path, refCacheTestFilter)
	defer src.Close()
	frames, done := cache.fill(src.FrameReader(inf, nil))
	buf := make([]byte, frameSize)
	for i := 0; i < 3; i++ {
		if err := frames.ReadFrame(ch.Start+i, buf); err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
	}
	done(false)

	if cache.ready {
		t.Error("an incomplete pass must not produce a usable cache")
	}
	if cache.open() != nil {
		t.Error("an incomplete cache must not open")
	}
	if _, err := os.Stat(cache.path); !os.IsNotExist(err) {
		t.Errorf("partial cache file was not removed: %v", err)
	}
}

func TestRefCacheRejectsFramesOutsideTheChunk(t *testing.T) {
	cache := &chunkRefCache{path: filepath.Join(t.TempDir(), "c.yuv"), start: 10, frames: 4, frameSize: 8, ready: true}
	if err := os.WriteFile(cache.path, make([]byte, 32), 0644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	reader := cache.open()
	if reader == nil {
		t.Fatal("cache.open returned no reader")
	}
	defer reader.Close()

	buf := make([]byte, 8)
	if err := reader.ReadFrame(10, buf); err != nil {
		t.Errorf("first cached frame: %v", err)
	}
	if err := reader.ReadFrame(9, buf); err == nil {
		t.Error("a frame before the chunk should not read")
	}
	if err := reader.ReadFrame(14, buf); err == nil {
		t.Error("a frame after the chunk should not read")
	}
	if err := reader.ReadFrame(10, make([]byte, 4)); err == nil {
		t.Error("an undersized buffer should not read")
	}
}

// A nil cache is the untreated path: every call must be a no-op passthrough.
func TestNilRefCacheIsPassthrough(t *testing.T) {
	var cache *chunkRefCache
	if cache.open() != nil {
		t.Error("nil cache should not open a reader")
	}
	stub := stubFrameReader{}
	frames, done := cache.fill(stub)
	if frames != video.FrameReader(stub) {
		t.Error("nil cache should return the source unwrapped")
	}
	done(true)
	cache.remove()
}

type stubFrameReader struct{}

func (stubFrameReader) ReadFrame(int, []byte) error { return nil }

func openFiltered(t *testing.T, path, filter string) *video.Source {
	t.Helper()
	src, err := video.OpenFiltered(path, 1, filter)
	if err != nil {
		t.Fatalf("OpenFiltered: %v", err)
	}
	src.ResetFilter()
	return src
}

func readChunk(t *testing.T, frames video.FrameReader, ch chunk.Chunk, frameSize int) [][]byte {
	t.Helper()
	out := make([][]byte, ch.Frames())
	for i := range out {
		buf := make([]byte, frameSize)
		if err := frames.ReadFrame(ch.Start+i, buf); err != nil {
			t.Fatalf("ReadFrame(%d): %v", ch.Start+i, err)
		}
		out[i] = buf
	}
	return out
}

// writeTestY4M writes a deterministic noisy YUV4MPEG2 clip, so the test needs
// no encoder and no external ffmpeg binary.
func writeTestY4M(t *testing.T, width, height, frames int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.y4m")
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "YUV4MPEG2 W%d H%d F25:1 Ip A1:1 C420\n", width, height)
	rnd := rand.New(rand.NewSource(1))
	for f := 0; f < frames; f++ {
		buf.WriteString("FRAME\n")
		for range height {
			for x := range width {
				buf.WriteByte(byte(16 + (x*4+f*3)%180 + rnd.Intn(40)))
			}
		}
		for range (height / 2) * (width / 2) * 2 {
			buf.WriteByte(byte(96 + rnd.Intn(64)))
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("failed to write test clip: %v", err)
	}
	return path
}

// TestRefCacheBudgetBoundsDiskUse: caching is an optimization, so a chunk that
// does not fit the budget must fall back to re-reading the source rather than
// filling the disk.
func TestRefCacheBudgetBoundsDiskUse(t *testing.T) {
	refCacheBudget.mu.Lock()
	original := refCacheBudget.remaining
	refCacheBudget.remaining = 100
	refCacheBudget.mu.Unlock()
	defer func() {
		refCacheBudget.mu.Lock()
		refCacheBudget.remaining = original
		refCacheBudget.mu.Unlock()
	}()

	fits := newChunkRefCache(t.TempDir(), chunk.Chunk{Idx: 0, Start: 0, End: 10}, 10, nil)
	if fits == nil {
		t.Fatal("a chunk within the budget should be cached")
	}
	if over := newChunkRefCache(t.TempDir(), chunk.Chunk{Idx: 1, Start: 0, End: 10}, 10, nil); over != nil {
		t.Error("a chunk beyond the budget should not be cached")
	}

	fits.remove()
	again := newChunkRefCache(t.TempDir(), chunk.Chunk{Idx: 2, Start: 0, End: 10}, 10, nil)
	if again == nil {
		t.Fatal("finishing a chunk should return its budget")
	}
	again.remove()
}
