package ripcache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"spindle/internal/config"
)

// ResolveTargetArg resolves either a 1-based cache entry number or a direct
// file/directory path into a concrete video file path and human label.
func ResolveTargetArg(ctx context.Context, manager *Manager, arg string) (string, string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", errors.New("cache entry or path is required")
	}

	if entryNum, err := strconv.Atoi(arg); err == nil {
		if entryNum < 1 {
			return "", "", fmt.Errorf("invalid cache entry number: %d", entryNum)
		}
		if manager == nil {
			return "", "", errors.New("ripcache: manager unavailable")
		}
		stats, err := manager.Stats(ctx)
		if err != nil {
			return "", "", err
		}
		if entryNum > len(stats.EntrySummaries) {
			return "", "", fmt.Errorf("cache entry %d out of range (only %d entries exist)", entryNum, len(stats.EntrySummaries))
		}
		entry := stats.EntrySummaries[entryNum-1]
		if entry.PrimaryFile == "" {
			return "", "", fmt.Errorf("cache entry %d has no detectable video files", entryNum)
		}
		target := filepath.Join(entry.Directory, entry.PrimaryFile)
		label := strings.TrimSpace(entry.PrimaryFile)
		if label == "" {
			label = filepath.Base(entry.Directory)
		}
		return target, label, nil
	}

	path, err := config.ExpandPath(arg)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("inspect path %q: %w", path, err)
	}
	if info.IsDir() {
		target, count, err := SelectPrimaryVideo(path)
		if err != nil {
			return "", "", err
		}
		label := filepath.Base(path)
		if label == "" {
			label = path
		}
		if count > 1 {
			label = fmt.Sprintf("%s (+%d more)", label, count-1)
		}
		return target, label, nil
	}
	return path, filepath.Base(path), nil
}
