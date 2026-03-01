package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"spindle/internal/config"
	"spindle/internal/ripcache"
)

// resolveCacheTarget resolves a cache entry number or path to a video file target.
// Returns (targetPath, label, error).
func resolveCacheTarget(ctx *commandContext, arg string, out io.Writer) (string, string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", errors.New("cache entry or path is required")
	}

	if entryNum, err := strconv.Atoi(arg); err == nil {
		if entryNum < 1 {
			return "", "", fmt.Errorf("invalid cache entry number: %d", entryNum)
		}
		manager, warn, err := cacheManager(ctx)
		if warn != "" {
			fmt.Fprintln(out, warn)
		}
		if err != nil || manager == nil {
			if err != nil {
				return "", "", err
			}
			return "", "", errors.New("rip cache is unavailable")
		}
		stats, err := manager.Stats(context.Background())
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
		target, count, err := ripcache.SelectPrimaryVideo(path)
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
