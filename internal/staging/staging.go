// Package staging manages staging directories used during disc processing.
package staging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/five82/spindle/internal/logs"
)

// DirInfo describes a staging directory.
type DirInfo struct {
	Name      string
	Path      string
	ModTime   time.Time
	SizeBytes int64
}

// CleanStaleResult reports what was cleaned.
type CleanStaleResult struct {
	Removed int
	Errors  []error
}

// ListDirectories lists all directories in stagingDir with metadata.
func ListDirectories(stagingDir string) ([]DirInfo, error) {
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		return nil, fmt.Errorf("read staging dir: %w", err)
	}

	var dirs []DirInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		dirPath := filepath.Join(stagingDir, e.Name())
		size, err := dirSize(dirPath)
		if err != nil {
			size = 0
		}
		dirs = append(dirs, DirInfo{
			Name:      e.Name(),
			Path:      dirPath,
			ModTime:   info.ModTime(),
			SizeBytes: size,
		})
	}

	return dirs, nil
}

// CleanStale removes directories older than maxAge, skipping directories whose
// names match an active fingerprint or the "queue-*" pattern.
func CleanStale(ctx context.Context, stagingDir string, maxAge time.Duration, activeFingerprints map[string]struct{}, logger *slog.Logger) CleanStaleResult {
	var result CleanStaleResult

	dirs, err := ListDirectories(stagingDir)
	if err != nil {
		result.Errors = append(result.Errors, err)
		return result
	}

	cutoff := time.Now().Add(-maxAge)

	for _, d := range dirs {
		if ctx.Err() != nil {
			result.Errors = append(result.Errors, ctx.Err())
			return result
		}

		if isProtected(d.Name, activeFingerprints) {
			logger.Info("staging directory preserved",
				"dir", d.Name,
				"decision_type", logs.DecisionStagingCleanup,
				"decision_result", "preserved",
				"decision_reason", "protected",
			)
			continue
		}

		if d.ModTime.After(cutoff) {
			logger.Info("staging directory preserved",
				"dir", d.Name,
				"decision_type", logs.DecisionStagingCleanup,
				"decision_result", "preserved",
				"decision_reason", "recent",
			)
			continue
		}

		logger.Info("removing stale staging directory",
			"dir", d.Name,
			"age", time.Since(d.ModTime).Truncate(time.Second),
			"decision_type", logs.DecisionStagingCleanup,
			"decision_result", "removed",
			"decision_reason", "stale",
		)
		if err := os.RemoveAll(d.Path); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("remove %s: %w", d.Name, err))
			continue
		}
		result.Removed++
	}

	return result
}

// CleanOrphaned removes directories whose names do not match any active
// fingerprint or the "queue-*" format.
func CleanOrphaned(ctx context.Context, stagingDir string, activeFingerprints map[string]struct{}, logger *slog.Logger) CleanStaleResult {
	var result CleanStaleResult

	dirs, err := ListDirectories(stagingDir)
	if err != nil {
		result.Errors = append(result.Errors, err)
		return result
	}

	for _, d := range dirs {
		if ctx.Err() != nil {
			result.Errors = append(result.Errors, ctx.Err())
			return result
		}

		if isProtected(d.Name, activeFingerprints) {
			logger.Info("staging directory preserved",
				"dir", d.Name,
				"decision_type", logs.DecisionStagingCleanup,
				"decision_result", "preserved",
				"decision_reason", "protected",
			)
			continue
		}

		logger.Info("removing orphaned staging directory",
			"dir", d.Name,
			"decision_type", logs.DecisionStagingCleanup,
			"decision_result", "removed",
			"decision_reason", "orphaned",
		)
		if err := os.RemoveAll(d.Path); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("remove %s: %w", d.Name, err))
			continue
		}
		result.Removed++
	}

	return result
}

// isProtected reports whether a directory name should be skipped during cleanup.
func isProtected(name string, activeFingerprints map[string]struct{}) bool {
	if strings.HasPrefix(name, "queue-") {
		return true
	}
	_, active := activeFingerprints[name]
	return active
}

// dirSize returns the total size of all files in dir (recursive).
func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}
