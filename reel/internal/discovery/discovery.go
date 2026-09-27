// Package discovery provides file discovery for video processing.
package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/five82/reel/internal/util"
)

// FindVideoFiles finds video files in the given directory.
// Returns files sorted alphabetically by filename.
func FindVideoFiles(inputDir string) ([]string, error) {
	info, err := os.Stat(inputDir)
	if err != nil {
		return nil, fmt.Errorf("directory does not exist: %s", inputDir)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", inputDir)
	}

	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read directory %s: %w", inputDir, err)
	}

	var files []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()

		// Skip hidden files
		if strings.HasPrefix(name, ".") {
			continue
		}

		fullPath := filepath.Join(inputDir, name)
		if util.IsVideoFile(fullPath) {
			files = append(files, fullPath)
		}
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no video files found in %s", inputDir)
	}

	// Sort alphabetically
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(filepath.Base(files[i])) < strings.ToLower(filepath.Base(files[j]))
	})

	return files, nil
}
