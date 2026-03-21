// Package fingerprint provides disc identification via SHA-256 hashes
// derived from disc filesystem content.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Generate creates a disc fingerprint from the mounted filesystem.
// It tries strategies in order: Blu-ray, DVD, then fallback.
func Generate(mountPoint string) (string, error) {
	// Try Blu-ray first (look for BDMV/index.bdmv).
	if fp, err := blurayFingerprint(mountPoint); err == nil && fp != "" {
		return fp, nil
	}

	// Try DVD (look for VIDEO_TS).
	if fp, err := dvdFingerprint(mountPoint); err == nil && fp != "" {
		return fp, nil
	}

	// Fallback: hash file content with size cap.
	return fallbackFingerprint(mountPoint)
}

// blurayFingerprint hashes content-significant Blu-ray files: index.bdmv,
// MovieObject.bdmv (if present), all .mpls playlists, and all .clpi clip info.
// CERTIFICATE/ and STREAM/ directories are excluded.
func blurayFingerprint(mountPoint string) (string, error) {
	bdmvDir := filepath.Join(mountPoint, "BDMV")
	if _, err := os.Stat(filepath.Join(bdmvDir, "index.bdmv")); err != nil {
		return "", err
	}

	var files []string

	// Required file.
	files = append(files, filepath.Join(bdmvDir, "index.bdmv"))

	// Optional file.
	if _, err := os.Stat(filepath.Join(bdmvDir, "MovieObject.bdmv")); err == nil {
		files = append(files, filepath.Join(bdmvDir, "MovieObject.bdmv"))
	}

	// Playlist files.
	files = append(files, collectGlob(filepath.Join(bdmvDir, "PLAYLIST"), "*.mpls")...)

	// Clip info files.
	files = append(files, collectGlob(filepath.Join(bdmvDir, "CLIPINF"), "*.clpi")...)

	return hashFiles(bdmvDir, files, 0)
}

// dvdFingerprint hashes all .ifo files from VIDEO_TS/.
func dvdFingerprint(mountPoint string) (string, error) {
	videoTSDir := filepath.Join(mountPoint, "VIDEO_TS")
	if _, err := os.Stat(videoTSDir); err != nil {
		return "", err
	}

	files := collectGlob(videoTSDir, "*.ifo")
	// Also match uppercase (common on DVD media).
	files = append(files, collectGlob(videoTSDir, "*.IFO")...)
	files = dedup(files)

	return hashFiles(videoTSDir, files, 0)
}

// fallbackFingerprint walks the entire mount point and hashes the first
// 64 KiB of each file.
func fallbackFingerprint(mountPoint string) (string, error) {
	var files []string
	err := filepath.WalkDir(mountPoint, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walking %s: %w", mountPoint, err)
	}
	return hashFiles(mountPoint, files, 65536)
}

// hashFiles computes a SHA-256 digest over the given files. For each file it
// writes: relative_path (forward slashes) + \x00 + size (decimal) + \x00 +
// file content (full or capped to maxBytes) + \x00. Files are processed in
// sorted order by relative path. If maxBytes is 0, full file content is read.
func hashFiles(basePath string, files []string, maxBytes int64) (string, error) {
	type entry struct {
		rel  string
		path string
	}

	var entries []entry
	for _, f := range files {
		rel, err := filepath.Rel(basePath, f)
		if err != nil {
			continue
		}
		// Normalize to forward slashes for cross-platform consistency.
		rel = filepath.ToSlash(rel)
		entries = append(entries, entry{rel: rel, path: f})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].rel < entries[j].rel
	})

	h := sha256.New()
	for _, e := range entries {
		content, err := readFileContent(e.path, maxBytes)
		if err != nil {
			continue // skip unreadable files
		}

		info, err := os.Stat(e.path)
		if err != nil {
			continue
		}

		// Write: relative_path \x00 size \x00 content \x00
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", e.rel, info.Size())
		_, _ = h.Write(content)
		_, _ = h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// readFileContent reads a file's content. If maxBytes > 0, only the first
// maxBytes are returned. If maxBytes is 0, the full file is read.
func readFileContent(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return os.ReadFile(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, maxBytes))
}

// collectGlob returns all files matching pattern within dir. Returns nil on
// error or no matches.
func collectGlob(dir, pattern string) []string {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return nil
	}
	return matches
}

// dedup removes duplicate paths from a sorted-or-unsorted slice.
func dedup(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	result := make([]string, 0, len(paths))
	for _, p := range paths {
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	return result
}
