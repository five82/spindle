package keyframe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractKeyframesFileReuseAndErrors(t *testing.T) {
	dir := t.TempDir()
	path, err := ExtractKeyframesIfNeeded("unused", dir, 24, 1, 50, 1)
	if err != nil || path != filepath.Join(dir, "chunk-plan.txt") {
		t.Fatalf("path=%q err=%v", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "0\n24\n48\n" {
		t.Fatalf("boundaries=%q err=%v", data, err)
	}
	if err := os.WriteFile(path, []byte("resume\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractKeyframesIfNeeded("unused", dir, 24, 1, 50, 1); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "resume\n" {
		t.Fatal("overwrote existing plan")
	}
	if _, err := ExtractKeyframesIfNeeded("unused", filepath.Join(dir, "missing"), 24, 1, 50, 1); err == nil || !strings.Contains(err.Error(), "failed to create") {
		t.Fatalf("missing directory: %v", err)
	}
}
