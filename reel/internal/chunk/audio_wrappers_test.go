package chunk

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkDirLifecycleAndEmptyAudio(t *testing.T) {
	temp := t.TempDir()
	work := GetWorkDirPath("/movies/feature.mkv", temp)
	if work != filepath.Join(temp, WorkDirName("/movies/feature.mkv")) {
		t.Fatalf("workdir: %q", work)
	}
	if WorkDirExists(work) {
		t.Fatal("new workdir already exists")
	}
	if err := CreateWorkDir(work); err != nil {
		t.Fatal(err)
	}
	if !WorkDirExists(work) {
		t.Fatal("workdir not created")
	}
	if _, err := os.Stat(filepath.Join(work, "encode")); err != nil {
		t.Fatalf("encode directory: %v", err)
	}
	if result, err := ExtractAudio(context.Background(), "nonexistent.mkv", work, nil, 1); err != nil || len(result) != 0 {
		t.Fatalf("empty streams: %v %v", result, err)
	}
	if err := CleanupWorkDir(work); err != nil {
		t.Fatal(err)
	}
	if WorkDirExists(work) {
		t.Fatal("workdir not removed")
	}
	file := filepath.Join(temp, "occupied")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CreateWorkDir(filepath.Join(file, "child")); err == nil {
		t.Fatal("created a directory inside a file")
	}
}
