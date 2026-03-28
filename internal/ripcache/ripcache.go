// Package ripcache manages cached rip results for reuse across pipeline runs.
package ripcache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// EntryMetadata stores metadata about a cached rip.
type EntryMetadata struct {
	Version      int       `json:"version"`
	Fingerprint  string    `json:"fingerprint"`
	DiscTitle    string    `json:"disc_title"`
	CachedAt     time.Time `json:"cached_at"`
	TitleCount   int       `json:"title_count"`
	TotalBytes   int64     `json:"total_bytes"`
	RipSpecData  string    `json:"ripspec_data,omitempty"`
	MetadataJSON string    `json:"metadata_json,omitempty"`
}

// Store manages the rip cache directory.
type Store struct {
	cacheDir string
	maxBytes int64
}

// New creates a rip cache store.
func New(cacheDir string, maxGiB int) *Store {
	return &Store{
		cacheDir: cacheDir,
		maxBytes: int64(maxGiB) * 1024 * 1024 * 1024,
	}
}

// Register copies ripped files from srcDir into the cache under fingerprint.
// Metadata is written as metadata.json alongside the cached files.
func (s *Store) Register(fingerprint, srcDir string, meta EntryMetadata) error {
	entryDir := filepath.Join(s.cacheDir, fingerprint)
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		return fmt.Errorf("create cache entry dir: %w", err)
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read source dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		srcPath := filepath.Join(srcDir, e.Name())
		dstPath := filepath.Join(entryDir, e.Name())
		if err := copyFile(srcPath, dstPath); err != nil {
			return fmt.Errorf("copy %s: %w", e.Name(), err)
		}
	}

	metaPath := filepath.Join(entryDir, "metadata.json")
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, data, 0o644); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	return nil
}

// Restore copies cached files for fingerprint into destDir.
// Returns nil, nil if no cache entry exists for the fingerprint.
func (s *Store) Restore(fingerprint, destDir string) (*EntryMetadata, error) {
	entryDir := filepath.Join(s.cacheDir, fingerprint)
	if _, err := os.Stat(entryDir); os.IsNotExist(err) {
		return nil, nil
	}

	meta, err := s.GetMetadata(fingerprint)
	if err != nil {
		return nil, fmt.Errorf("read metadata: %w", err)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("create dest dir: %w", err)
	}

	entries, err := os.ReadDir(entryDir)
	if err != nil {
		return nil, fmt.Errorf("read cache entry dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || e.Name() == "metadata.json" {
			continue
		}
		srcPath := filepath.Join(entryDir, e.Name())
		dstPath := filepath.Join(destDir, e.Name())
		if err := copyFile(srcPath, dstPath); err != nil {
			return nil, fmt.Errorf("copy %s: %w", e.Name(), err)
		}
	}

	return meta, nil
}

// HasCache reports whether a cache entry exists for the given fingerprint.
func (s *Store) HasCache(fingerprint string) bool {
	entryDir := filepath.Join(s.cacheDir, fingerprint)
	info, err := os.Stat(entryDir)
	return err == nil && info.IsDir()
}

// Prune removes the oldest cache entries until total size is under maxBytes.
func (s *Store) Prune() error {
	entries, err := os.ReadDir(s.cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read cache dir: %w", err)
	}

	type cacheEntry struct {
		name     string
		size     int64
		cachedAt time.Time
	}

	var all []cacheEntry
	var totalSize int64

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := s.GetMetadata(e.Name())
		if err != nil {
			// Skip entries without valid metadata.
			continue
		}
		all = append(all, cacheEntry{
			name:     e.Name(),
			size:     meta.TotalBytes,
			cachedAt: meta.CachedAt,
		})
		totalSize += meta.TotalBytes
	}

	if totalSize <= s.maxBytes {
		return nil
	}

	// Sort oldest first.
	sort.Slice(all, func(i, j int) bool {
		return all[i].cachedAt.Before(all[j].cachedAt)
	})

	for _, ce := range all {
		if totalSize <= s.maxBytes {
			break
		}
		entryDir := filepath.Join(s.cacheDir, ce.name)
		if err := os.RemoveAll(entryDir); err != nil {
			return fmt.Errorf("remove cache entry %s: %w", ce.name, err)
		}
		totalSize -= ce.size
	}

	return nil
}

// GetMetadata reads the metadata.json for a cache entry.
func (s *Store) GetMetadata(fingerprint string) (*EntryMetadata, error) {
	metaPath := filepath.Join(s.cacheDir, fingerprint, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("read metadata: %w", err)
	}

	var meta EntryMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse metadata: %w", err)
	}

	return &meta, nil
}

// List returns metadata for all cache entries, sorted newest first.
func (s *Store) List() ([]EntryMetadata, error) {
	entries, err := os.ReadDir(s.cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cache dir: %w", err)
	}

	var result []EntryMetadata
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := s.GetMetadata(e.Name())
		if err != nil {
			continue
		}
		result = append(result, *meta)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CachedAt.After(result[j].CachedAt)
	})
	return result, nil
}

// Remove deletes a cache entry by fingerprint.
func (s *Store) Remove(fingerprint string) error {
	entryDir := filepath.Join(s.cacheDir, fingerprint)
	if _, err := os.Stat(entryDir); os.IsNotExist(err) {
		return fmt.Errorf("cache entry not found: %s", fingerprint)
	}
	return os.RemoveAll(entryDir)
}

// Clear removes all cache entries.
func (s *Store) Clear() error {
	entries, err := os.ReadDir(s.cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read cache dir: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		entryDir := filepath.Join(s.cacheDir, e.Name())
		if err := os.RemoveAll(entryDir); err != nil {
			return fmt.Errorf("remove %s: %w", e.Name(), err)
		}
	}
	return nil
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}
