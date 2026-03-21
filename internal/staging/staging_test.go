package staging

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestListDirectories(t *testing.T) {
	dir := t.TempDir()

	// Create some subdirectories with files.
	subA := filepath.Join(dir, "aaa")
	subB := filepath.Join(dir, "bbb")
	if err := os.Mkdir(subA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(subB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subA, "file.mkv"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Also create a regular file (should be ignored).
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := ListDirectories(dir)
	if err != nil {
		t.Fatalf("ListDirectories: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 directories, got %d", len(dirs))
	}

	// Find the one with the file and verify size.
	var found bool
	for _, d := range dirs {
		if d.Name == "aaa" {
			found = true
			if d.SizeBytes != 4 {
				t.Fatalf("SizeBytes: got %d, want 4", d.SizeBytes)
			}
		}
	}
	if !found {
		t.Fatal("directory 'aaa' not found in listing")
	}
}

func TestCleanStalePreservesActive(t *testing.T) {
	dir := t.TempDir()
	logger := testLogger()

	// Create an "old" directory with an active fingerprint.
	oldActive := filepath.Join(dir, "active-fp")
	if err := os.Mkdir(oldActive, 0o755); err != nil {
		t.Fatal(err)
	}
	// Backdate it.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldActive, old, old); err != nil {
		t.Fatal(err)
	}

	// Create an old directory that is not active.
	oldOrphan := filepath.Join(dir, "orphan-fp")
	if err := os.Mkdir(oldOrphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldOrphan, old, old); err != nil {
		t.Fatal(err)
	}

	active := map[string]struct{}{
		"active-fp": {},
	}

	result := CleanStale(context.Background(), dir, 24*time.Hour, active, logger)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if result.Removed != 1 {
		t.Fatalf("Removed: got %d, want 1", result.Removed)
	}

	// Active directory should still exist.
	if _, err := os.Stat(oldActive); os.IsNotExist(err) {
		t.Fatal("active directory was removed")
	}

	// Orphan directory should be gone.
	if _, err := os.Stat(oldOrphan); !os.IsNotExist(err) {
		t.Fatal("orphan directory was not removed")
	}
}

func TestCleanStaleRemovesOldDirs(t *testing.T) {
	dir := t.TempDir()
	logger := testLogger()

	// Create two old directories.
	for _, name := range []string{"old1", "old2"} {
		p := filepath.Join(dir, name)
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-72 * time.Hour)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	// Create a fresh directory.
	fresh := filepath.Join(dir, "fresh")
	if err := os.Mkdir(fresh, 0o755); err != nil {
		t.Fatal(err)
	}

	result := CleanStale(context.Background(), dir, 24*time.Hour, nil, logger)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if result.Removed != 2 {
		t.Fatalf("Removed: got %d, want 2", result.Removed)
	}

	// Fresh directory should still exist.
	if _, err := os.Stat(fresh); os.IsNotExist(err) {
		t.Fatal("fresh directory was removed")
	}
}

func TestCleanStalePreservesQueueDirs(t *testing.T) {
	dir := t.TempDir()
	logger := testLogger()

	// Create an old queue directory.
	queueDir := filepath.Join(dir, "queue-abc123")
	if err := os.Mkdir(queueDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(queueDir, old, old); err != nil {
		t.Fatal(err)
	}

	result := CleanStale(context.Background(), dir, 24*time.Hour, nil, logger)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if result.Removed != 0 {
		t.Fatalf("Removed: got %d, want 0", result.Removed)
	}

	if _, err := os.Stat(queueDir); os.IsNotExist(err) {
		t.Fatal("queue directory was removed")
	}
}

func TestCleanOrphaned(t *testing.T) {
	dir := t.TempDir()
	logger := testLogger()

	// Active directory.
	if err := os.Mkdir(filepath.Join(dir, "active-fp"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Queue directory.
	if err := os.Mkdir(filepath.Join(dir, "queue-job1"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Orphaned directory.
	orphan := filepath.Join(dir, "orphan-fp")
	if err := os.Mkdir(orphan, 0o755); err != nil {
		t.Fatal(err)
	}

	active := map[string]struct{}{
		"active-fp": {},
	}

	result := CleanOrphaned(context.Background(), dir, active, logger)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if result.Removed != 1 {
		t.Fatalf("Removed: got %d, want 1", result.Removed)
	}

	// Active and queue directories should remain.
	if _, err := os.Stat(filepath.Join(dir, "active-fp")); os.IsNotExist(err) {
		t.Fatal("active directory was removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "queue-job1")); os.IsNotExist(err) {
		t.Fatal("queue directory was removed")
	}

	// Orphan should be gone.
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan directory was not removed")
	}
}
