package encoding

import (
	"os"
	"path/filepath"
	"testing"

	"spindle/internal/encodingstate"
)

func TestUpdateEstimatedSizeNilSnapshot(t *testing.T) {
	if updateEstimatedSize(nil, 50.0) {
		t.Fatal("expected false for nil snapshot")
	}
}

func TestUpdateEstimatedSizeNilVideo(t *testing.T) {
	snapshot := &encodingstate.Snapshot{}
	if updateEstimatedSize(snapshot, 50.0) {
		t.Fatal("expected false for nil video")
	}
}

func TestUpdateEstimatedSizeLowPercent(t *testing.T) {
	snapshot := &encodingstate.Snapshot{
		Video: &encodingstate.Video{OutputFile: "/tmp/test.mkv"},
	}
	if updateEstimatedSize(snapshot, 5.0) {
		t.Fatal("expected false for percent < 10")
	}
	if updateEstimatedSize(snapshot, 9.9) {
		t.Fatal("expected false for percent < 10")
	}
}

func TestUpdateEstimatedSizeEmptyPath(t *testing.T) {
	snapshot := &encodingstate.Snapshot{
		Video: &encodingstate.Video{OutputFile: ""},
	}
	if updateEstimatedSize(snapshot, 50.0) {
		t.Fatal("expected false for empty output path")
	}
}

func TestUpdateEstimatedSizeMissingFile(t *testing.T) {
	snapshot := &encodingstate.Snapshot{
		Video: &encodingstate.Video{OutputFile: "/nonexistent/path/file.mkv"},
	}
	if updateEstimatedSize(snapshot, 50.0) {
		t.Fatal("expected false for missing file")
	}
}

func TestUpdateEstimatedSizeCalculation(t *testing.T) {
	// Create a temp file with known size
	dir := t.TempDir()
	filePath := filepath.Join(dir, "output.mkv")

	// Write 1MB of data
	const fileSize = 1024 * 1024 // 1 MB
	data := make([]byte, fileSize)
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	snapshot := &encodingstate.Snapshot{
		Video: &encodingstate.Video{OutputFile: filePath},
	}

	// At 50% progress, estimated total should be 2MB
	if !updateEstimatedSize(snapshot, 50.0) {
		t.Fatal("expected true for valid update")
	}

	if snapshot.CurrentOutputBytes != fileSize {
		t.Fatalf("expected CurrentOutputBytes=%d, got %d", fileSize, snapshot.CurrentOutputBytes)
	}

	expectedEstimate := int64(fileSize / 0.5) // 2 MB
	if snapshot.EstimatedTotalBytes != expectedEstimate {
		t.Fatalf("expected EstimatedTotalBytes=%d, got %d", expectedEstimate, snapshot.EstimatedTotalBytes)
	}

	// At 25% progress, estimated total should be 4MB
	if !updateEstimatedSize(snapshot, 25.0) {
		t.Fatal("expected true for valid update")
	}

	expectedEstimate = int64(fileSize / 0.25) // 4 MB
	if snapshot.EstimatedTotalBytes != expectedEstimate {
		t.Fatalf("expected EstimatedTotalBytes=%d, got %d", expectedEstimate, snapshot.EstimatedTotalBytes)
	}
}

func TestUpdateEstimatedSizeNoChangeWhenSame(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "output.mkv")

	const fileSize = 1024 * 1024
	data := make([]byte, fileSize)
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	snapshot := &encodingstate.Snapshot{
		Video: &encodingstate.Video{OutputFile: filePath},
	}

	// First call should return true (changed)
	if !updateEstimatedSize(snapshot, 50.0) {
		t.Fatal("expected true for first update")
	}

	// Second call with same values should return false (no change)
	if updateEstimatedSize(snapshot, 50.0) {
		t.Fatal("expected false when values unchanged")
	}
}
