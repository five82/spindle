package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClearQueueDBFilesRemovesOnlyQueueFiles(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "queue.db")
	paths := []string{dbPath, dbPath + "-wal", dbPath + "-shm"}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	other := filepath.Join(dir, "staging-output.mkv")
	if err := os.WriteFile(other, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write other: %v", err)
	}

	if err := clearQueueDBFiles(dbPath); err != nil {
		t.Fatalf("clearQueueDBFiles: %v", err)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists or stat failed with non-missing error: %v", path, err)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("non-queue file was removed or became unreadable: %v", err)
	}
}

func TestClearQueueDBFilesMissingFilesOK(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	if err := clearQueueDBFiles(dbPath); err != nil {
		t.Fatalf("clearQueueDBFiles missing files: %v", err)
	}
}
