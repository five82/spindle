package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChunkbenchPlansRawVideoAndWritesScores(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "source.y4m")
	scores := filepath.Join(dir, "scores.txt")
	var clip bytes.Buffer
	clip.WriteString("YUV4MPEG2 W32 H32 F25:1 Ip A1:1 C420\n")
	for i := 0; i < 25; i++ {
		clip.WriteString("FRAME\n")
		clip.Write(bytes.Repeat([]byte{byte(16 + i)}, 32*32))
		clip.Write(bytes.Repeat([]byte{128}, 32*32/2))
	}
	if err := os.WriteFile(input, clip.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	oldArgs, oldStdout := os.Args, os.Stdout
	defer func() { os.Args, os.Stdout = oldArgs, oldStdout }()
	t.Setenv("REEL_SHOT_DETECT_WORKERS", "1")
	os.Args = []string{"chunkbench", input, scores}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	main()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Shot detection complete") || !strings.Contains(string(out), "Workers:   1") {
		t.Fatalf("missing planning summary: %s", out)
	}
	data, err := os.ReadFile(scores)
	if err != nil || len(data) == 0 {
		t.Fatalf("scores: %v", err)
	}
}
