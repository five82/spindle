// Package fingerprint provides disc identification via SHA-256 hashes
// derived from disc filesystem metadata (file paths and sizes).
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

	// Fallback: hash all file metadata.
	return fallbackFingerprint(mountPoint)
}

// blurayFingerprint hashes the file manifest of the BDMV/ directory.
func blurayFingerprint(mountPoint string) (string, error) {
	bdmvDir := filepath.Join(mountPoint, "BDMV")
	if _, err := os.Stat(filepath.Join(bdmvDir, "index.bdmv")); err != nil {
		return "", err
	}
	return hashFileManifest(bdmvDir)
}

// dvdFingerprint hashes the file manifest of the VIDEO_TS/ directory.
func dvdFingerprint(mountPoint string) (string, error) {
	videoTSDir := filepath.Join(mountPoint, "VIDEO_TS")
	if _, err := os.Stat(videoTSDir); err != nil {
		return "", err
	}
	return hashFileManifest(videoTSDir)
}

// fallbackFingerprint hashes the file manifest of the entire mount point.
func fallbackFingerprint(mountPoint string) (string, error) {
	return hashFileManifest(mountPoint)
}

// hashFileManifest walks dir, collects "relative/path:size" entries (sorted),
// joins them with newlines, and returns the hex-encoded SHA-256 hash.
// Unreadable entries are silently skipped.
func hashFileManifest(dir string) (string, error) {
	var entries []string

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable entries.
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		entries = append(entries, fmt.Sprintf("%s:%d", rel, info.Size()))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walking %s: %w", dir, err)
	}

	sort.Strings(entries)

	h := sha256.New()
	_, err = fmt.Fprint(h, strings.Join(entries, "\n"))
	if err != nil {
		return "", fmt.Errorf("hashing manifest: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
