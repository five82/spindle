package discidcache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"spindle/internal/logging"
)

// Entry represents a cached mapping from disc ID to TMDB metadata.
type Entry struct {
	DiscID       string    `json:"disc_id"`
	TMDBID       int64     `json:"tmdb_id"`
	MediaType    string    `json:"media_type"`    // "movie" or "tv"
	Title        string    `json:"title"`         // Identified title
	Edition      string    `json:"edition"`       // e.g., "Director's Cut" (movies only)
	SeasonNumber int       `json:"season_number"` // TV shows only
	Year         string    `json:"year"`
	CachedAt     time.Time `json:"cached_at"`
}

// Cache provides thread-safe access to the disc ID cache.
type Cache struct {
	path    string
	logger  *slog.Logger
	mu      sync.RWMutex
	entries map[string]Entry // keyed by disc ID
}

// NewCache creates a new cache instance. If path is empty, the cache will be
// non-functional (all operations become no-ops). The cache file is created
// lazily on first Store call.
func NewCache(path string, logger *slog.Logger) *Cache {
	if logger == nil {
		logger = logging.NewNop()
	}
	logger = logging.NewComponentLogger(logger, "discidcache")

	c := &Cache{
		path:    path,
		logger:  logger,
		entries: make(map[string]Entry),
	}

	if path == "" {
		return c
	}

	// Load existing cache if present
	if err := c.load(); err != nil {
		logger.Warn("failed to load disc id cache",
			logging.String(logging.FieldEventType, "discidcache_load_failed"),
			logging.Error(err),
			logging.String(logging.FieldErrorHint, "cache will start empty"),
			logging.String(logging.FieldImpact, "previously cached disc IDs will need re-identification"))
	}

	return c
}

// Lookup returns the cache entry for the given disc ID if found.
func (c *Cache) Lookup(discID string) (Entry, bool) {
	discID = strings.TrimSpace(discID)
	if discID == "" || c.path == "" {
		return Entry{}, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, found := c.entries[discID]
	return entry, found
}

// Store adds or updates an entry in the cache and persists to disk.
func (c *Cache) Store(entry Entry) error {
	entry.DiscID = strings.TrimSpace(entry.DiscID)
	if entry.DiscID == "" {
		return errors.New("disc ID cannot be empty")
	}
	if c.path == "" {
		return nil // no-op when path not configured
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[entry.DiscID] = entry

	if err := c.save(); err != nil {
		return fmt.Errorf("persist cache: %w", err)
	}

	c.logger.Debug("cached disc id mapping",
		logging.String("disc_id", entry.DiscID),
		logging.Int64("tmdb_id", entry.TMDBID),
		logging.String("title", entry.Title),
		logging.String("media_type", entry.MediaType))

	return nil
}

// Remove deletes an entry by disc ID and persists the change.
func (c *Cache) Remove(discID string) error {
	discID = strings.TrimSpace(discID)
	if discID == "" {
		return errors.New("disc ID cannot be empty")
	}
	if c.path == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.entries[discID]; !exists {
		return fmt.Errorf("disc ID %q not found in cache", discID)
	}

	delete(c.entries, discID)

	if err := c.save(); err != nil {
		return fmt.Errorf("persist cache: %w", err)
	}

	c.logger.Debug("removed disc id from cache", logging.String("disc_id", discID))
	return nil
}

// List returns all cache entries sorted by CachedAt descending (newest first).
func (c *Cache) List() []Entry {
	if c.path == "" {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	entries := make([]Entry, 0, len(c.entries))
	for _, entry := range c.entries {
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CachedAt.After(entries[j].CachedAt)
	})

	return entries
}

// Clear removes all entries and persists the empty cache.
func (c *Cache) Clear() error {
	if c.path == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]Entry)

	if err := c.save(); err != nil {
		return fmt.Errorf("persist cache: %w", err)
	}

	c.logger.Debug("cleared disc id cache")
	return nil
}

// Count returns the number of entries in the cache.
func (c *Cache) Count() int {
	if c.path == "" {
		return 0
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.entries)
}

// load reads the cache from disk into memory.
func (c *Cache) load() error {
	data, err := os.ReadFile(c.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // fresh start
		}
		return fmt.Errorf("read cache file: %w", err)
	}

	if len(data) == 0 {
		return nil
	}

	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parse cache file: %w", err)
	}

	c.entries = make(map[string]Entry, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.DiscID) != "" {
			c.entries[entry.DiscID] = entry
		}
	}

	c.logger.Debug("loaded disc id cache",
		logging.Int("entry_count", len(c.entries)),
		logging.String("path", c.path))

	return nil
}

// save writes the cache to disk atomically.
func (c *Cache) save() error {
	entries := make([]Entry, 0, len(c.entries))
	for _, entry := range c.entries {
		entries = append(entries, entry)
	}

	// Sort for deterministic output
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CachedAt.After(entries[j].CachedAt)
	})

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}

	// Ensure parent directory exists
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}

	// Write atomically via temp file
	tmpPath := c.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, c.path); err != nil {
		os.Remove(tmpPath) // cleanup on failure
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}
