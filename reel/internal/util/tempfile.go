// Package util provides utility functions for file operations.
package util

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// MinTempSpaceMB is the minimum free space required for temporary operations (in MB).
const MinTempSpaceMB = 100

// TempDir represents a temporary directory with automatic cleanup.
type TempDir struct {
	path string
}

// Path returns the path to the temporary directory.
func (t *TempDir) Path() string {
	return t.path
}

// Cleanup removes the temporary directory and all its contents.
func (t *TempDir) Cleanup() error {
	if t.path == "" {
		return nil
	}
	return os.RemoveAll(t.path)
}

// TempFile represents a temporary file with automatic cleanup.
type TempFile struct {
	*os.File
	path string
}

// Cleanup closes and removes the temporary file.
func (t *TempFile) Cleanup() error {
	var closeErr error
	if t.File != nil {
		closeErr = t.Close()
	}
	if t.path == "" {
		return closeErr
	}
	if err := os.Remove(t.path); err != nil {
		return err
	}
	return closeErr
}

// EnsureDirectoryWritable checks if a directory exists and is writable.
func EnsureDirectoryWritable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("directory does not exist: %s", path)
		}
		return fmt.Errorf("cannot access directory: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", path)
	}

	// Check if directory is writable by attempting to create a test file
	testPath := filepath.Join(path, ".reel_write_test")
	f, err := os.Create(testPath)
	if err != nil {
		return fmt.Errorf("directory is not writable: %s", path)
	}
	_ = f.Close()
	_ = os.Remove(testPath)

	return nil
}

// GetAvailableSpace returns the available disk space in bytes for the given path.
// Returns 0 if the space cannot be determined.
func GetAvailableSpace(path string) uint64 {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0
	}
	return stat.Bavail * uint64(stat.Bsize)
}

// CheckDiskSpace checks if there is sufficient disk space and logs a warning if low.
// Returns true if space is sufficient or cannot be determined.
func CheckDiskSpace(path string, logger func(format string, args ...any)) bool {
	available := GetAvailableSpace(path)
	if available == 0 {
		return true // Cannot determine, assume OK
	}

	availableMB := available / (1024 * 1024)
	if availableMB < MinTempSpaceMB {
		if logger != nil {
			logger("Low disk space in %s: %d MB available (minimum recommended: %d MB)",
				path, availableMB, MinTempSpaceMB)
		}
		return false
	}
	return true
}

// CreateTempDir creates a temporary directory with the given prefix.
// The caller is responsible for calling Cleanup() when done.
func CreateTempDir(baseDir, prefix string) (*TempDir, error) {
	// Validate base directory is writable
	if err := EnsureDirectoryWritable(baseDir); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Check available disk space (warning only)
	CheckDiskSpace(baseDir, nil)

	// Generate random suffix
	randomSuffix, err := generateRandomString(8)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random string: %w", err)
	}

	dirName := fmt.Sprintf("%s_%s", prefix, randomSuffix)
	dirPath := filepath.Join(baseDir, dirName)

	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory in %s: %w", baseDir, err)
	}

	return &TempDir{path: dirPath}, nil
}

// CreateTempFile creates a temporary file with the given prefix and extension.
// The caller is responsible for calling Cleanup() when done.
func CreateTempFile(dir, prefix, extension string) (*TempFile, error) {
	if err := EnsureDirectoryWritable(dir); err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	randomSuffix, err := generateRandomString(8)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random string: %w", err)
	}

	filename := fmt.Sprintf("%s_%s.%s", prefix, randomSuffix, extension)
	filePath := filepath.Join(dir, filename)

	f, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file in %s: %w", dir, err)
	}

	return &TempFile{File: f, path: filePath}, nil
}

// CreateTempFilePath returns a temporary file path with random suffix.
// Does not create the file. Validates the directory exists and is writable first.
func CreateTempFilePath(dir, prefix, extension string) (string, error) {
	if err := EnsureDirectoryWritable(dir); err != nil {
		return "", fmt.Errorf("failed to create temp file path: %w", err)
	}

	randomSuffix, err := generateRandomString(8)
	if err != nil {
		return "", fmt.Errorf("failed to generate random string: %w", err)
	}

	filename := fmt.Sprintf("%s_%s.%s", prefix, randomSuffix, extension)
	tempPath := filepath.Join(dir, filename)

	// Ensure the path doesn't already exist (extremely unlikely but safer)
	if _, err := os.Stat(tempPath); err == nil {
		// Path exists, retry
		return CreateTempFilePath(dir, prefix, extension)
	}

	return tempPath, nil
}

// CleanupStaleTempFiles removes temporary files matching the prefix older than maxAgeHours.
// Returns the number of files cleaned up.
func CleanupStaleTempFiles(dir, prefix string, maxAgeHours uint64) (int, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return 0, nil
	}

	cleanedCount := 0
	maxAge := time.Duration(maxAgeHours) * time.Hour
	now := time.Now()

	prefixMatch := fmt.Sprintf("%s_", prefix)

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}

		// Only process files in the top level
		if d.IsDir() {
			if path != dir {
				return fs.SkipDir
			}
			return nil
		}

		filename := d.Name()
		if !strings.HasPrefix(filename, prefixMatch) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		age := now.Sub(info.ModTime())
		if age > maxAge {
			if err := os.Remove(path); err == nil {
				cleanedCount++
			}
		}

		return nil
	})

	if err != nil {
		return cleanedCount, fmt.Errorf("failed to read temp directory for cleanup: %w", err)
	}

	return cleanedCount, nil
}

// generateRandomString generates a random hex string of the given length.
func generateRandomString(length int) (string, error) {
	bytes := make([]byte, (length+1)/2)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes)[:length], nil
}
