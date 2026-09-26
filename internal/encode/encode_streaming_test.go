package encode

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/encoder"
	"github.com/five82/reel/internal/video"
	"github.com/five82/reel/internal/worker"
)

// A few frames of raw Y4M are enough to exercise the streaming worker's
// frame handoff, IVF output, and resume bookkeeping without an external encoder.
func TestEncodeAllTinyRawClipAndResume(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiny.y4m")
	data := []byte("YUV4MPEG2 W32 H32 F25:1 Ip A1:1 C420\n")
	for i := 0; i < 6; i++ {
		data = append(data, []byte("FRAME\n")...)
		for j := 0; j < 32*32*3/2; j++ {
			data = append(data, byte(40+i))
		}
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := video.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	chunks := []chunk.Chunk{{Idx: 0, Start: 0, End: 3}, {Idx: 1, Start: 3, End: 6}}
	cfg := &EncodeConfig{CRF: 30, Preset: 12}
	work := filepath.Join(dir, "work")
	for run := 0; run < 2; run++ {
		var progress []worker.Progress
		var progressMu sync.Mutex
		workers, err := EncodeAll(context.Background(), chunks, path, info, cfg, work, nil, func(p worker.Progress) {
			progressMu.Lock()
			progress = append(progress, p)
			progressMu.Unlock()
		})
		if err != nil || workers < 1 {
			t.Fatalf("run %d: workers=%d error=%v", run, workers, err)
		}
		resume, err := chunk.GetResume(work)
		if err != nil {
			t.Fatal(err)
		}
		if len(resume.ChunksDone) != 2 {
			t.Fatalf("run %d: completed=%+v", run, resume.ChunksDone)
		}
		for _, ch := range chunks {
			f, err := os.Open(chunk.IVFPath(work, ch.Idx))
			if err != nil {
				t.Fatal(err)
			}
			frames, err := encoder.IVFVideoBytes(f)
			_ = f.Close()
			if err != nil || frames == 0 {
				t.Fatalf("chunk %d: bytes=%d err=%v", ch.Idx, frames, err)
			}
		}
		if run == 0 && (len(progress) == 0 || progress[len(progress)-1].ChunksComplete != 2) {
			t.Fatalf("progress=%+v", progress)
		}
		if run == 1 && len(progress) != 0 {
			t.Fatalf("resumed chunks were re-encoded: %+v", progress)
		}
	}
}
