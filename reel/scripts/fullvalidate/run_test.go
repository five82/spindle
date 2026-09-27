package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/video"
)

func TestRunValidationWithChunkScorers(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.y4m")
	data := []byte("YUV4MPEG2 W16 H16 F25:1 Ip A1:1 C420\n")
	for i := 0; i < 4; i++ {
		data = append(data, "FRAME\n"...)
		data = append(data, make([]byte, 16*16*3/2)...)
	}
	for name, contents := range map[string][]byte{
		"source.y4m":          data,
		"resume.json":         []byte(`{"target_quality":"7.5-8.5","frames":4,"crop_filter":"crop=8:8:2:2"}`),
		"target-quality.json": []byte(`{"chunks":[{"chunk_idx":0,"metric":"cvvdp","final_score":7.5,"final_crf":25,"probes":[{}]},{"chunk_idx":1,"metric":"ssimu2","final_score":8.2,"final_crf":30}]}`),
		"chunk-plan.txt":      []byte("0\n2\n"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dump := filepath.Join(dir, "report.json")
	t.Setenv("FULLVALIDATE_JSON", dump)
	var mu sync.Mutex
	seen := make(map[int]bool)
	factory := func(_, _, _ string, info *video.Info, crop *video.CropRect, w, h uint32) (func(chunk.Chunk) (float32, error), func(), error) {
		if info.Frames != 4 || w != 8 || h != 8 || crop.X != 2 || crop.Y != 2 {
			return nil, nil, fmt.Errorf("bad scoring setup: %+v %+v %dx%d", info, crop, w, h)
		}
		return func(c chunk.Chunk) (float32, error) {
			mu.Lock()
			defer mu.Unlock()
			seen[c.Idx] = true
			if c.Frames() != 2 {
				return 0, fmt.Errorf("bad chunk: %+v", c)
			}
			return 7.5 + float32(c.Idx), nil
		}, func() {}, nil
	}
	runValidation(source, dir, factory)
	if len(seen) != 2 {
		t.Fatalf("scored chunks: %v", seen)
	}
	result, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Target float32 `json:"target"`
		Chunks []struct {
			Idx    int     `json:"chunk_idx"`
			Full   float32 `json:"full_jod"`
			Metric string  `json:"search_metric"`
		} `json:"chunks"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatal(err)
	}
	if out.Target != 8 || len(out.Chunks) != 2 || out.Chunks[0].Idx != 0 || out.Chunks[1].Full != 8.5 || out.Chunks[1].Metric != "ssimu2" {
		t.Fatalf("report: %+v", out)
	}
}
