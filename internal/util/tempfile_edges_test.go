package util

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTempCleanupEmptyPathsAndAlreadyClosedFile(t *testing.T) {
	if err := (&TempDir{}).Cleanup(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "temporary")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	// Cleanup still removes the path if the file has already been closed.
	if err := (&TempFile{File: file, path: path}).Cleanup(); err == nil {
		t.Fatal("expected the already-closed file error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temp file remains: %v", err)
	}
	if err := (&TempFile{}).Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupStaleTempFilesKeepsRecentAndNestedFiles(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "reel_old.tmp")
	recent := filepath.Join(dir, "reel_recent.tmp")
	nested := filepath.Join(dir, "nested", "reel_old.tmp")
	if err := os.Mkdir(filepath.Dir(nested), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{old, recent, nested} {
		if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	for _, path := range []string{old, nested} {
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
	count, err := CleanupStaleTempFiles(dir, "reel", 24)
	if err != nil || count != 1 {
		t.Fatalf("removed %d, err %v", count, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("stale top-level file remains: %v", err)
	}
	for _, path := range []string{recent, nested} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("file %q unexpectedly removed: %v", path, err)
		}
	}
}
