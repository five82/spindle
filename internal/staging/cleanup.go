package staging

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"spindle/internal/logging"
)

// CleanStaleResult contains the outcome of a stale directory cleanup operation.
type CleanStaleResult struct {
	Removed []string
	Errors  []CleanupError
}

// CleanupError pairs a directory path with its cleanup error.
type CleanupError struct {
	Path  string
	Error error
}

// CleanStale removes staging directories older than maxAge.
// It returns the list of removed directories and any errors encountered.
func CleanStale(ctx context.Context, stagingDir string, maxAge time.Duration, logger *slog.Logger) CleanStaleResult {
	result := CleanStaleResult{}

	stagingDir = strings.TrimSpace(stagingDir)
	if stagingDir == "" {
		return result
	}

	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		if !os.IsNotExist(err) {
			result.Errors = append(result.Errors, CleanupError{Path: stagingDir, Error: err})
		}
		return result
	}

	cutoff := time.Now().Add(-maxAge)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirPath := filepath.Join(stagingDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			result.Errors = append(result.Errors, CleanupError{Path: dirPath, Error: err})
			continue
		}

		if info.ModTime().Before(cutoff) {
			if err := os.RemoveAll(dirPath); err != nil {
				result.Errors = append(result.Errors, CleanupError{Path: dirPath, Error: err})
				if logger != nil {
					logger.Warn("failed to remove stale staging directory",
						logging.String("path", dirPath),
						logging.Error(err),
						logging.String(logging.FieldEventType, "staging_cleanup_failed"),
						logging.String(logging.FieldErrorHint, "check staging_dir permissions"),
						logging.String(logging.FieldImpact, "disk space not reclaimed"),
					)
				}
			} else {
				result.Removed = append(result.Removed, dirPath)
				if logger != nil {
					logger.Info("removed stale staging directory",
						logging.String("path", dirPath),
						logging.Duration("age", time.Since(info.ModTime())),
						logging.String(logging.FieldEventType, "staging_cleanup"),
					)
				}
			}
		}
	}

	return result
}

// CleanOrphaned removes staging directories that don't match any active fingerprint.
// It returns the list of removed directories and any errors encountered.
func CleanOrphaned(ctx context.Context, stagingDir string, activeFingerprints map[string]struct{}, logger *slog.Logger) CleanStaleResult {
	result := CleanStaleResult{}

	stagingDir = strings.TrimSpace(stagingDir)
	if stagingDir == "" {
		return result
	}

	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		if !os.IsNotExist(err) {
			result.Errors = append(result.Errors, CleanupError{Path: stagingDir, Error: err})
		}
		return result
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirName := strings.ToUpper(entry.Name())
		dirPath := filepath.Join(stagingDir, entry.Name())

		// Check if directory matches any active fingerprint
		if _, active := activeFingerprints[dirName]; active {
			continue
		}

		// Also check for queue-{ID} format directories
		if strings.HasPrefix(strings.ToLower(entry.Name()), "queue-") {
			// These are identified by ID, not fingerprint - skip for orphan detection
			// They should be cleaned by stale cleanup instead
			continue
		}

		if err := os.RemoveAll(dirPath); err != nil {
			result.Errors = append(result.Errors, CleanupError{Path: dirPath, Error: err})
			if logger != nil {
				logger.Warn("failed to remove orphaned staging directory",
					logging.String("path", dirPath),
					logging.Error(err),
					logging.String(logging.FieldEventType, "staging_cleanup_failed"),
					logging.String(logging.FieldErrorHint, "check staging_dir permissions"),
					logging.String(logging.FieldImpact, "disk space not reclaimed"),
				)
			}
		} else {
			result.Removed = append(result.Removed, dirPath)
			if logger != nil {
				logger.Info("removed orphaned staging directory",
					logging.String("path", dirPath),
					logging.String(logging.FieldEventType, "staging_cleanup"),
				)
			}
		}
	}

	return result
}

// ListDirectories returns all directories in the staging directory with their metadata.
func ListDirectories(stagingDir string) ([]DirInfo, error) {
	stagingDir = strings.TrimSpace(stagingDir)
	if stagingDir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var dirs []DirInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		dirPath := filepath.Join(stagingDir, entry.Name())
		size, _ := dirSize(dirPath)

		dirs = append(dirs, DirInfo{
			Name:    entry.Name(),
			Path:    dirPath,
			ModTime: info.ModTime(),
			Size:    size,
		})
	}

	return dirs, nil
}

// DirInfo contains metadata about a staging directory.
type DirInfo struct {
	Name    string
	Path    string
	ModTime time.Time
	Size    int64
}

// dirSize calculates the total size of a directory recursively.
func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Ignore errors, best effort
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}
