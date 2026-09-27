package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/video"
)

func TestHandlerTestRunsSerialTruthAndDistinctConcurrentScorers(t *testing.T) {
	dir := t.TempDir()
	data := []byte("YUV4MPEG2 W16 H16 F25:1 Ip A1:1 C420\n")
	for i := 0; i < 6; i++ {
		data = append(data, "FRAME\n"...)
		data = append(data, make([]byte, 384)...)
	}
	for name, contents := range map[string][]byte{
		"source.y4m":          data,
		"resume.json":         []byte(`{"frames":6,"crop_filter":"crop=8:8:2:2"}`),
		"target-quality.json": []byte(`{"target":8,"tolerance":0.5}`),
		"chunk-plan.txt":      []byte("0\n2\n4\n"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	created, closed, scored := 0, 0, 0
	factory := func(_, _, _ string, info *video.Info, crop *video.CropRect, w, h uint32) (func(chunk.Chunk) (float32, error), func(), error) {
		if info.Frames != 6 || crop.X != 2 || w != 8 || h != 8 {
			return nil, nil, fmt.Errorf("bad setup")
		}
		mu.Lock()
		created++
		mu.Unlock()
		return func(ch chunk.Chunk) (float32, error) {
			mu.Lock()
			defer mu.Unlock()
			scored++
			if ch.Frames() != 2 {
				return 0, fmt.Errorf("bad chunk %+v", ch)
			}
			return 8 + float32(ch.Idx)*0.1, nil
		}, func() { mu.Lock(); closed++; mu.Unlock() }, nil
	}
	runHandlerTest(filepath.Join(dir, "source.y4m"), dir, 2, 2, factory)
	if created != 5 || closed != 5 || scored != 9 {
		t.Fatalf("scorers: created=%d closed=%d scored=%d", created, closed, scored)
	}
}
