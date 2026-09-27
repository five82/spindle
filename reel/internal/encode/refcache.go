package encode

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/video"
)

// chunkRefCache holds one chunk's post-crop 10-bit frames in a work-directory
// file so repeat probes and the metric reference read them back instead of
// decoding and re-filtering the source again.
//
// It exists for denoised target-quality runs: measured wall on denoised 4K
// titles was 1.7-2.2x the undenoised run, and the cost was re-filtering the
// reference for every probe, not the filter itself. The cached bytes are
// exactly the buffers both consumers already read, so cached and uncached
// runs are bit-identical (see TestRefCacheMatchesFilteredSource).
//
// Disk is bounded by one file per in-flight chunk (about 7 GB for a 12s 4K
// chunk), removed as soon as the chunk finalizes or fails. Caching is best
// effort: a write failure (a full disk) disables it for that chunk and the run
// falls back to filtering.
//
// A chunk is processed start to finish by one worker goroutine, and its probe
// encodes and metric passes are strictly sequential, so the cache state needs
// no locking. Readers each own their file handle.
type chunkRefCache struct {
	path      string
	start     int
	frames    int
	frameSize int
	reserved  int64
	warn      func(string)

	ready    bool
	disabled bool
}

// refCacheBudgetBytes caps how much cached video can exist at once across all
// in-flight chunks. Cached frames are raw 10-bit YUV: a 12s 4K chunk is about
// 7 GB, and the target-quality scheduler opens up to roughly ten chunks in
// flight on a long title, which would be most of a disk. This keeps the
// caching set to a handful of 4K chunks (many more at 1080p); chunks that do
// not fit simply re-read the source, which is only slower.
const refCacheBudgetBytes int64 = 32 << 30

var refCacheBudget = struct {
	mu        sync.Mutex
	remaining int64
}{remaining: refCacheBudgetBytes}

// newChunkRefCache prepares (but does not fill) the cache for one chunk, or
// returns nil when the disk budget is already spoken for.
func newChunkRefCache(workDir string, ch chunk.Chunk, frameSize int, warn func(string)) *chunkRefCache {
	size := int64(ch.Frames()) * int64(frameSize)
	refCacheBudget.mu.Lock()
	defer refCacheBudget.mu.Unlock()
	if size > refCacheBudget.remaining {
		return nil
	}
	refCacheBudget.remaining -= size
	return &chunkRefCache{
		path:      filepath.Join(workDir, "refcache", fmt.Sprintf("%04d.yuv", ch.Idx)),
		start:     ch.Start,
		frames:    ch.Frames(),
		frameSize: frameSize,
		reserved:  size,
		warn:      warn,
	}
}

// open returns a reader over the cached frames, or nil when nothing is cached
// yet (the caller then reads the source). Nil-safe so call sites do not branch.
// The caller closes the returned reader.
func (c *chunkRefCache) open() *refCacheReader {
	if c == nil || !c.ready {
		return nil
	}
	f, err := os.Open(c.path)
	if err != nil {
		c.fail(fmt.Sprintf("reference cache unreadable for chunk %s: %v", filepath.Base(c.path), err))
		return nil
	}
	return &refCacheReader{cache: c, file: f}
}

// fill wraps src so every frame it produces is also appended to the cache
// file. The returned done must always be called, with ok reporting whether the
// pass read the whole chunk successfully. Nil-safe: a nil cache returns src
// unchanged.
func (c *chunkRefCache) fill(src video.FrameReader) (frames video.FrameReader, done func(ok bool)) {
	noCache := func(bool) {}
	if c == nil || c.disabled || c.ready {
		return src, noCache
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0755); err != nil {
		c.fail(fmt.Sprintf("reference cache disabled: %v", err))
		return src, noCache
	}
	f, err := os.Create(c.path)
	if err != nil {
		c.fail(fmt.Sprintf("reference cache disabled: %v", err))
		return src, noCache
	}
	w := &refCacheWriter{
		cache: c,
		src:   src,
		file:  f,
		buf:   bufio.NewWriterSize(f, 1<<20),
		next:  c.start,
	}
	return w, w.done
}

// remove deletes the cached frames and returns the chunk's share of the disk
// budget. Called exactly once, when the chunk finalizes or fails.
func (c *chunkRefCache) remove() {
	if c == nil {
		return
	}
	c.ready = false
	_ = os.Remove(c.path)
	if c.reserved > 0 {
		refCacheBudget.mu.Lock()
		refCacheBudget.remaining += c.reserved
		refCacheBudget.mu.Unlock()
		c.reserved = 0
	}
}

// fail disables caching for this chunk and reports why once.
func (c *chunkRefCache) fail(message string) {
	if c.disabled {
		return
	}
	c.disabled = true
	c.ready = false
	_ = os.Remove(c.path)
	if c.warn != nil {
		c.warn(message + "; falling back to re-reading the source")
	}
}

// refCacheWriter tees frames read from the source into the cache file.
type refCacheWriter struct {
	cache   *chunkRefCache
	src     video.FrameReader
	file    *os.File
	buf     *bufio.Writer
	next    int
	written int
}

func (w *refCacheWriter) ReadFrame(frameIdx int, output []byte) error {
	if err := w.src.ReadFrame(frameIdx, output); err != nil {
		return err
	}
	w.record(frameIdx, output)
	return nil
}

func (w *refCacheWriter) record(frameIdx int, output []byte) {
	if w.file == nil {
		return
	}
	// The cache is a flat sequential file, so a non-sequential or wrong-sized
	// read means this pass cannot produce a usable cache. Drop it silently:
	// nothing is wrong with the encode, only with caching it.
	if frameIdx != w.next || len(output) != w.cache.frameSize {
		w.discard()
		return
	}
	if _, err := w.buf.Write(output); err != nil {
		w.cache.fail(fmt.Sprintf("reference cache write failed: %v", err))
		w.dropFile()
		return
	}
	w.next++
	w.written++
}

// done completes the caching pass: ok means the consumer read every frame.
func (w *refCacheWriter) done(ok bool) {
	w.finish(ok)
	w.close()
}

func (w *refCacheWriter) finish(ok bool) {
	if w.file == nil {
		return
	}
	if !ok || w.written != w.cache.frames {
		w.discard()
		return
	}
	if err := w.buf.Flush(); err != nil {
		w.cache.fail(fmt.Sprintf("reference cache write failed: %v", err))
		w.dropFile()
		return
	}
	// No fsync: the cache is a within-run optimization, never resume state.
	w.cache.ready = true
}

func (w *refCacheWriter) close() {
	if w.file == nil {
		return
	}
	if err := w.file.Close(); err != nil && w.cache.ready {
		w.cache.fail(fmt.Sprintf("reference cache close failed: %v", err))
	}
	w.file = nil
	if !w.cache.ready {
		_ = os.Remove(w.cache.path)
	}
}

// discard abandons this pass's cache without treating it as a failure.
func (w *refCacheWriter) discard() {
	w.dropFile()
	w.cache.ready = false
}

func (w *refCacheWriter) dropFile() {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	_ = os.Remove(w.cache.path)
}

// refCacheReader reads frames back by absolute source frame index.
type refCacheReader struct {
	cache *chunkRefCache
	file  *os.File
}

func (r *refCacheReader) ReadFrame(frameIdx int, output []byte) error {
	offset := frameIdx - r.cache.start
	if offset < 0 || offset >= r.cache.frames {
		return fmt.Errorf("frame %d outside cached chunk range %d-%d", frameIdx, r.cache.start, r.cache.start+r.cache.frames-1)
	}
	if len(output) < r.cache.frameSize {
		return fmt.Errorf("frame buffer too small: got %d, need %d", len(output), r.cache.frameSize)
	}
	if _, err := r.file.ReadAt(output[:r.cache.frameSize], int64(offset)*int64(r.cache.frameSize)); err != nil {
		return fmt.Errorf("failed to read cached frame %d: %w", frameIdx, err)
	}
	return nil
}

func (r *refCacheReader) Close() {
	_ = r.file.Close()
}
