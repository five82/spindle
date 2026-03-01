package ripping

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func existsNonEmptyDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}

// selectCachedRip picks the largest MKV in dir, assuming it is the primary
// feature. Returns an empty string when none are present.
func selectCachedRip(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	type candidate struct {
		path string
		size int64
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if !strings.HasSuffix(name, ".mkv") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		candidates = append(candidates, candidate{path: filepath.Join(dir, entry.Name()), size: info.Size()})
	}
	if len(candidates) == 0 {
		return "", nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].size > candidates[j].size
	})
	return candidates[0].path, nil
}
