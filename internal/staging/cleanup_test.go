package staging

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spindle/internal/logging"
)

func TestCleanStaleInvalidPaths(t *testing.T) {
	for _, dir := range []string{"", "   ", "/nonexistent/path/12345"} {
		result := CleanStale(context.Background(), dir, time.Hour, logging.NewNop())
		if len(result.Removed) != 0 || len(result.Errors) != 0 {
			t.Errorf("expected empty result for path %q", dir)
		}
	}
}

func TestCleanStaleRemovesOldDirectories(t *testing.T) {
	tmpDir := t.TempDir()

	// Create old directory
	oldDir := filepath.Join(tmpDir, "old-staging")
	if err := os.Mkdir(oldDir, 0o755); err != nil {
		t.Fatalf("create old dir: %v", err)
	}
	// Set modification time to 2 hours ago
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldDir, oldTime, oldTime); err != nil {
		t.Fatalf("set old time: %v", err)
	}

	// Create recent directory
	recentDir := filepath.Join(tmpDir, "recent-staging")
	if err := os.Mkdir(recentDir, 0o755); err != nil {
		t.Fatalf("create recent dir: %v", err)
	}

	result := CleanStale(context.Background(), tmpDir, time.Hour, logging.NewNop())

	if len(result.Removed) != 1 {
		t.Fatalf("expected 1 removed, got %d", len(result.Removed))
	}
	if result.Removed[0] != oldDir {
		t.Errorf("expected %s to be removed, got %s", oldDir, result.Removed[0])
	}

	// Old dir should be gone
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error("old directory should have been removed")
	}

	// Recent dir should still exist
	if _, err := os.Stat(recentDir); err != nil {
		t.Error("recent directory should still exist")
	}
}

func TestCleanStaleIgnoresFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create an old file (should be ignored)
	oldFile := filepath.Join(tmpDir, "old-file.txt")
	if err := os.WriteFile(oldFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
		t.Fatalf("set old time: %v", err)
	}

	result := CleanStale(context.Background(), tmpDir, time.Hour, logging.NewNop())

	if len(result.Removed) != 0 {
		t.Errorf("expected no removals for files, got %d", len(result.Removed))
	}

	// File should still exist
	if _, err := os.Stat(oldFile); err != nil {
		t.Error("file should not have been removed")
	}
}

func TestCleanOrphanedEmptyDir(t *testing.T) {
	for _, dir := range []string{"", "   "} {
		result := CleanOrphaned(context.Background(), dir, nil, logging.NewNop())
		if len(result.Removed) != 0 || len(result.Errors) != 0 {
			t.Errorf("expected empty result for path %q", dir)
		}
	}
}

func TestCleanOrphanedRemovesUnknownFingerprints(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directory with known fingerprint
	knownDir := filepath.Join(tmpDir, "ABC123")
	if err := os.Mkdir(knownDir, 0o755); err != nil {
		t.Fatalf("create known dir: %v", err)
	}

	// Create directory with unknown fingerprint
	unknownDir := filepath.Join(tmpDir, "XYZ789")
	if err := os.Mkdir(unknownDir, 0o755); err != nil {
		t.Fatalf("create unknown dir: %v", err)
	}

	activeFingerprints := map[string]struct{}{
		"ABC123": {},
	}

	result := CleanOrphaned(context.Background(), tmpDir, activeFingerprints, logging.NewNop())

	if len(result.Removed) != 1 {
		t.Fatalf("expected 1 removed, got %d", len(result.Removed))
	}
	if result.Removed[0] != unknownDir {
		t.Errorf("expected %s to be removed, got %s", unknownDir, result.Removed[0])
	}

	// Unknown dir should be gone
	if _, err := os.Stat(unknownDir); !os.IsNotExist(err) {
		t.Error("unknown directory should have been removed")
	}

	// Known dir should still exist
	if _, err := os.Stat(knownDir); err != nil {
		t.Error("known directory should still exist")
	}
}

func TestCleanOrphanedCaseInsensitive(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directory with lowercase fingerprint
	dir := filepath.Join(tmpDir, "abc123")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}

	// Active fingerprints use uppercase
	activeFingerprints := map[string]struct{}{
		"ABC123": {},
	}

	result := CleanOrphaned(context.Background(), tmpDir, activeFingerprints, logging.NewNop())

	if len(result.Removed) != 0 {
		t.Errorf("expected no removals (case insensitive match), got %d", len(result.Removed))
	}

	// Dir should still exist
	if _, err := os.Stat(dir); err != nil {
		t.Error("directory should still exist")
	}
}

func TestCleanOrphanedSkipsQueueDirs(t *testing.T) {
	tmpDir := t.TempDir()

	// Create queue-ID directory (should be skipped)
	queueDir := filepath.Join(tmpDir, "queue-123")
	if err := os.Mkdir(queueDir, 0o755); err != nil {
		t.Fatalf("create queue dir: %v", err)
	}

	// Create orphan directory
	orphanDir := filepath.Join(tmpDir, "orphan")
	if err := os.Mkdir(orphanDir, 0o755); err != nil {
		t.Fatalf("create orphan dir: %v", err)
	}

	activeFingerprints := map[string]struct{}{}

	result := CleanOrphaned(context.Background(), tmpDir, activeFingerprints, logging.NewNop())

	// Only orphan should be removed, not queue-123
	if len(result.Removed) != 1 {
		t.Fatalf("expected 1 removed, got %d: %v", len(result.Removed), result.Removed)
	}
	if result.Removed[0] != orphanDir {
		t.Errorf("expected orphan dir removed, got %s", result.Removed[0])
	}

	// Queue dir should still exist
	if _, err := os.Stat(queueDir); err != nil {
		t.Error("queue directory should still exist")
	}
}

func TestListDirectoriesInvalidPaths(t *testing.T) {
	for _, path := range []string{"", "/nonexistent/path/12345"} {
		dirs, err := ListDirectories(path)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", path, err)
		}
		if dirs != nil {
			t.Errorf("expected nil for path %q, got %v", path, dirs)
		}
	}
}

func TestListDirectories(t *testing.T) {
	tmpDir := t.TempDir()

	// Create some directories
	dir1 := filepath.Join(tmpDir, "staging-1")
	if err := os.Mkdir(dir1, 0o755); err != nil {
		t.Fatalf("create dir1: %v", err)
	}

	dir2 := filepath.Join(tmpDir, "staging-2")
	if err := os.Mkdir(dir2, 0o755); err != nil {
		t.Fatalf("create dir2: %v", err)
	}

	// Create a file (should be ignored)
	file := filepath.Join(tmpDir, "not-a-dir.txt")
	if err := os.WriteFile(file, []byte("test"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	// Add a file inside dir1 for size calculation
	innerFile := filepath.Join(dir1, "data.bin")
	if err := os.WriteFile(innerFile, []byte("12345"), 0o644); err != nil {
		t.Fatalf("create inner file: %v", err)
	}

	dirs, err := ListDirectories(tmpDir)
	if err != nil {
		t.Fatalf("ListDirectories: %v", err)
	}

	if len(dirs) != 2 {
		t.Fatalf("expected 2 directories, got %d", len(dirs))
	}

	// Check that dir1 has the correct size
	var foundDir1 bool
	for _, d := range dirs {
		if d.Name == "staging-1" {
			foundDir1 = true
			if d.Size != 5 {
				t.Errorf("dir1 size = %d, want 5", d.Size)
			}
		}
	}
	if !foundDir1 {
		t.Error("did not find staging-1 in results")
	}
}

func TestDirInfo(t *testing.T) {
	tmpDir := t.TempDir()

	dir := filepath.Join(tmpDir, "test-staging")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}

	dirs, err := ListDirectories(tmpDir)
	if err != nil {
		t.Fatalf("ListDirectories: %v", err)
	}

	if len(dirs) != 1 {
		t.Fatalf("expected 1 directory, got %d", len(dirs))
	}

	info := dirs[0]
	if info.Name != "test-staging" {
		t.Errorf("Name = %q, want test-staging", info.Name)
	}
	if info.Path != dir {
		t.Errorf("Path = %q, want %q", info.Path, dir)
	}
	if info.ModTime.IsZero() {
		t.Error("ModTime should not be zero")
	}
}
