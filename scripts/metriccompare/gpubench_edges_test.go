package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/reel/internal/video"
)

func TestGPUBenchInputValidationAndCPUDecode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.y4m")
	dist := filepath.Join(dir, "distorted.y4m")
	var clip bytes.Buffer
	clip.WriteString("YUV4MPEG2 W4 H4 F25:1 Ip A1:1 C420\n")
	for i := 0; i < 3; i++ {
		clip.WriteString("FRAME\n")
		clip.Write(bytes.Repeat([]byte{byte(16 + i)}, 16))
		clip.Write(bytes.Repeat([]byte{128}, 8))
	}
	if err := os.WriteFile(src, clip.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dist, clip.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		src, dist, crop string
		frames, reps    int
		want            string
	}{
		{"", "", "", 1, 1, "requires --src"},
		{src, dist, "", 0, 1, "must be positive"},
		{"missing", dist, "", 1, 1, "probe source"},
		{src, dist, "bad", 1, 1, "invalid crop"},
		{src, "missing", "", 1, 1, "probe distorted"},
		{src, dist, "2:2:0:0", 1, 1, "do not match"},
	} {
		err := runGPUBench(tc.src, tc.dist, 0, tc.frames, tc.reps, tc.crop, "", metricSet{ssimu2: true})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("runGPUBench: %v, want %q", err, tc.want)
		}
	}
	info, err := video.Probe(src)
	if err != nil {
		t.Fatal(err)
	}
	ref, probe, err := decodeFramesToRAM(src, dist, info, info, nil, 1, 2, 4, 4)
	if err != nil || len(ref) != 2 || len(probe) != 2 || ref[0].Planes[0] == nil {
		t.Fatalf("decoded buffers: %d %d, %v", len(ref), len(probe), err)
	}
	if _, _, err := decodeFramesToRAM(src, dist, info, info, nil, 9, 1, 4, 4); err == nil || !strings.Contains(err.Error(), "read source frame") {
		t.Fatalf("out of range: %v", err)
	}
	short := filepath.Join(dir, "short.y4m")
	firstFrameEnd := bytes.Index(clip.Bytes(), []byte("FRAME\n")) + len("FRAME\n") + 24
	if err := os.WriteFile(short, clip.Bytes()[:firstFrameEnd], 0600); err != nil {
		t.Fatal(err)
	}
	shortInfo, err := video.Probe(short)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeFramesToRAM(src, short, info, shortInfo, nil, 0, 2, 4, 4); err == nil || !strings.Contains(err.Error(), "read distorted frame") {
		t.Fatalf("short distorted stream: %v", err)
	}
}
