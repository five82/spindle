// Package discidcache provides a JSON file-backed cache mapping disc
// fingerprints to TMDB identification data.
package discidcache

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/five82/spindle/internal/logs"
)

// Entry maps a disc fingerprint to TMDB identification data.
type Entry struct {
	TMDBID                 int    `json:"tmdb_id"`
	MediaType              string `json:"media_type"`
	Title                  string `json:"title"`
	Year                   string `json:"year,omitempty"`
	Season                 int    `json:"season,omitempty"`
	HasForcedSubtitleTrack bool   `json:"has_forced_subtitle_track,omitempty"`
}

// Store is a JSON file-backed disc ID cache.
type Store struct {
	path    string
	mu      sync.RWMutex
	entries map[string]Entry // fingerprint -> entry
	logger  *slog.Logger
}

// Open loads or creates a disc ID cache at path.
func Open(path string, logger *slog.Logger) (*Store, error) {
	logger = logs.Default(logger)
	s := &Store{
		path:    path,
		entries: make(map[string]Entry),
		logger:  logger,
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// New cache; persist an empty file.
		if err := s.persist(); err != nil {
			return nil, fmt.Errorf("initialize cache: %w", err)
		}
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cache: %w", err)
	}

	if err := json.Unmarshal(data, &s.entries); err != nil {
		return nil, fmt.Errorf("parse cache: %w", err)
	}

	return s, nil
}

// Lookup finds an entry by fingerprint. Returns nil if not found.
func (s *Store) Lookup(fingerprint string) *Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.entries[fingerprint]
	if !ok {
		s.logger.Info("disc ID cache miss",
			"decision_type", logs.DecisionDiscIDCache,
			"decision_result", "miss",
			"decision_reason", "fingerprint not in cache",
			"fingerprint", fingerprint,
		)
		return nil
	}
	s.logger.Info("disc ID cache hit",
		"decision_type", logs.DecisionDiscIDCache,
		"decision_result", "hit",
		"decision_reason", fmt.Sprintf("cached tmdb_id=%d", entry.TMDBID),
		"fingerprint", fingerprint,
		"tmdb_id", entry.TMDBID,
		"media_type", entry.MediaType,
	)
	return &entry
}

// Set adds or updates an entry and persists the cache atomically.
func (s *Store) Set(fingerprint string, entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries[fingerprint] = entry
	s.logger.Info("disc ID cache entry stored", "fingerprint", fingerprint, "tmdb_id", entry.TMDBID)
	return s.persist()
}

// ListEntry is a disc ID cache entry with its fingerprint, for display.
type ListEntry struct {
	Fingerprint string
	Entry       Entry
}

// List returns all entries as a slice, sorted by fingerprint.
func (s *Store) List() []ListEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]ListEntry, 0, len(s.entries))
	for fp, e := range s.entries {
		result = append(result, ListEntry{Fingerprint: fp, Entry: e})
	}
	return result
}

// Size returns the number of entries.
func (s *Store) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Remove deletes an entry by fingerprint and persists.
func (s *Store) Remove(fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.entries[fingerprint]; !ok {
		return fmt.Errorf("entry not found: %s", fingerprint)
	}
	delete(s.entries, fingerprint)
	if err := s.persist(); err != nil {
		return err
	}
	s.logger.Info("disc ID cache entry removed", "fingerprint", fingerprint)
	return nil
}

// Clear removes all entries and persists.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := len(s.entries)
	s.entries = make(map[string]Entry)
	if err := s.persist(); err != nil {
		return err
	}
	s.logger.Info("disc ID cache cleared", "entries_removed", count)
	return nil
}

// persist writes the cache to disk atomically (write tmp, rename).
func (s *Store) persist() error {
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}

	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename tmp: %w", err)
	}

	return nil
}
