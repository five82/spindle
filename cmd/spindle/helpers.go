package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// selectPrimaryVideo returns the largest MKV file in dir along with the total
// count of MKV files found. An error is returned when no MKV files exist.
func selectPrimaryVideo(dir string) (string, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, fmt.Errorf("read cache directory %q: %w", dir, err)
	}
	type candidate struct {
		name string
		size int64
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".mkv" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{name: entry.Name(), size: info.Size()})
	}
	if len(candidates) == 0 {
		return "", 0, fmt.Errorf("no video files found in %q", dir)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].size == candidates[j].size {
			return candidates[i].name < candidates[j].name
		}
		return candidates[i].size > candidates[j].size
	})
	return filepath.Join(dir, candidates[0].name), len(candidates), nil
}
