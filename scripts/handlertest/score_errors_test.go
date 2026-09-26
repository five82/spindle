package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/quality"
	"github.com/five82/reel/internal/video"
)

func TestScoreChunkFailuresBeforeGPUCompute(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.y4m")
	data := append([]byte("YUV4MPEG2 W16 H16 F25:1 Ip A1:1 C420\nFRAME\n"), make([]byte, 16*16*3/2)...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := video.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	ch := chunk.Chunk{Idx: 0, Start: 0, End: 1}
	proc := &quality.VshipProcessor{}
	if _, err := scoreChunk(proc, path+".missing", ch, info, nil, 16, 16, dir); err == nil {
		t.Fatal("accepted missing source")
	}
	if _, err := scoreChunk(proc, path, ch, info, nil, 16, 16, dir); err == nil {
		t.Fatal("accepted missing probe")
	}
	if err := os.MkdirAll(filepath.Dir(chunk.IVFPath(dir, 0)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chunk.IVFPath(dir, 0), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := scoreChunk(nil, path, ch, info, nil, 16, 16, dir); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed handler: %v", err)
	}
}
