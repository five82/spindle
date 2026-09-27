package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDirectoryWritable(t *testing.T) {
	// Test with valid writable directory
	tmpDir := t.TempDir()
	if err := EnsureDirectoryWritable(tmpDir); err != nil {
		t.Errorf("Expected no error for writable dir, got %v", err)
	}

	// Test with non-existent directory
	err := EnsureDirectoryWritable("/nonexistent/directory/path")
	if err == nil {
		t.Error("Expected error for non-existent directory")
	}

	// Test with file instead of directory
	tmpFile := filepath.Join(tmpDir, "testfile")
	if err := os.WriteFile(tmpFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	err = EnsureDirectoryWritable(tmpFile)
	if err == nil {
		t.Error("Expected error for file instead of directory")
	}
}

func TestCreateTempDir(t *testing.T) {
	baseDir := t.TempDir()

	tempDir, err := CreateTempDir(baseDir, "test")
	if err != nil {
		t.Fatalf("CreateTempDir failed: %v", err)
	}
	t.Cleanup(func() { _ = tempDir.Cleanup() })

	// Check that directory was created
	info, err := os.Stat(tempDir.Path())
	if err != nil {
		t.Fatalf("Temp directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("Expected a directory")
	}

	// Check that prefix is in the path
	if filepath.Base(tempDir.Path())[:5] != "test_" {
		t.Errorf("Directory name should start with 'test_', got %s", filepath.Base(tempDir.Path()))
	}

	// Test cleanup
	path := tempDir.Path()
	if err := tempDir.Cleanup(); err != nil {
		t.Errorf("Cleanup failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Directory should be removed after cleanup")
	}
}

func TestCreateTempFile(t *testing.T) {
	baseDir := t.TempDir()

	tempFile, err := CreateTempFile(baseDir, "test", "txt")
	if err != nil {
		t.Fatalf("CreateTempFile failed: %v", err)
	}
	t.Cleanup(func() { _ = tempFile.Cleanup() })

	// Check that file was created
	info, err := os.Stat(tempFile.path)
	if err != nil {
		t.Fatalf("Temp file not created: %v", err)
	}
	if info.IsDir() {
		t.Error("Expected a file, got directory")
	}

	// Check file name format
	base := filepath.Base(tempFile.path)
	if base[:5] != "test_" {
		t.Errorf("File name should start with 'test_', got %s", base)
	}
	if filepath.Ext(tempFile.path) != ".txt" {
		t.Errorf("File should have .txt extension, got %s", filepath.Ext(tempFile.path))
	}

	// Test cleanup
	path := tempFile.path
	if err := tempFile.Cleanup(); err != nil {
		t.Errorf("Cleanup failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("File should be removed after cleanup")
	}
}

func TestCreateTempFilePath(t *testing.T) {
	baseDir := t.TempDir()

	path, err := CreateTempFilePath(baseDir, "test", "mkv")
	if err != nil {
		t.Fatalf("CreateTempFilePath failed: %v", err)
	}

	// Check that file was NOT created
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("File should not exist yet")
	}

	// Check path format
	base := filepath.Base(path)
	if base[:5] != "test_" {
		t.Errorf("Path name should start with 'test_', got %s", base)
	}
	if filepath.Ext(path) != ".mkv" {
		t.Errorf("Path should have .mkv extension, got %s", filepath.Ext(path))
	}

	// Verify path is in the correct directory
	if filepath.Dir(path) != baseDir {
		t.Errorf("Path should be in %s, got %s", baseDir, filepath.Dir(path))
	}
}

func TestCleanupStaleTempFiles(t *testing.T) {
	baseDir := t.TempDir()

	// Create some test files with the prefix
	for i := range 3 {
		path := filepath.Join(baseDir, "test_old"+string(rune('0'+i))+".tmp")
		if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Create a file without the prefix
	otherPath := filepath.Join(baseDir, "other.tmp")
	if err := os.WriteFile(otherPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Cleanup with 0 max age should remove all matching files
	count, err := CleanupStaleTempFiles(baseDir, "test", 0)
	if err != nil {
		t.Fatalf("CleanupStaleTempFiles failed: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 files cleaned, got %d", count)
	}

	// The other file should still exist
	if _, err := os.Stat(otherPath); os.IsNotExist(err) {
		t.Error("File without prefix should not be removed")
	}
}

func TestCleanupStaleTempFiles_NonExistentDir(t *testing.T) {
	// Should not error on non-existent directory
	count, err := CleanupStaleTempFiles("/nonexistent/path", "test", 0)
	if err != nil {
		t.Errorf("Should not error on non-existent dir: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 files cleaned, got %d", count)
	}
}

func TestGetAvailableSpace(t *testing.T) {
	// Test with a valid path
	space := GetAvailableSpace("/tmp")
	if space == 0 {
		t.Log("GetAvailableSpace returned 0, this might be expected on some systems")
	}

	// Test with invalid path - should return 0
	space = GetAvailableSpace("/nonexistent/path")
	if space != 0 {
		t.Errorf("Expected 0 for invalid path, got %d", space)
	}
}

func TestCheckDiskSpace(t *testing.T) {
	// Test with a valid path - should not panic and return a result
	_ = CheckDiskSpace("/tmp", nil)

	// Test with logger
	logger := func(format string, args ...any) {
		// Just verify the logger is called without panicking
		_ = format
		_ = args
	}
	// This should work without panicking
	CheckDiskSpace("/tmp", logger)
}

func TestGenerateRandomString(t *testing.T) {
	s1, err := generateRandomString(8)
	if err != nil {
		t.Fatalf("generateRandomString failed: %v", err)
	}
	if len(s1) != 8 {
		t.Errorf("Expected length 8, got %d", len(s1))
	}

	// Generate another and ensure they're different (very high probability)
	s2, err := generateRandomString(8)
	if err != nil {
		t.Fatalf("generateRandomString failed: %v", err)
	}
	if s1 == s2 {
		t.Error("Two random strings should be different")
	}
}
