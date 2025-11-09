package overrides

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"log/slog"
)

// Catalog loads user-authored identification overrides.
type Catalog struct {
	path    string
	logger  *slog.Logger
	mu      sync.RWMutex
	loaded  time.Time
	entries []Override
}

// Override pins a disc fingerprint or DiscID to curated metadata.
type Override struct {
	Fingerprints  []string          `json:"fingerprints"`
	DiscIDs       []string          `json:"disc_ids"`
	Title         string            `json:"title"`
	TMDBID        int64             `json:"tmdb_id"`
	MediaType     string            `json:"media_type"`
	Season        int               `json:"season"`
	Episodes      []int             `json:"episodes"`
	EpisodeTitles map[string]string `json:"episode_titles"`
}

// NewCatalog constructs a catalog backed by the provided JSON file.
func NewCatalog(path string, logger *slog.Logger) *Catalog {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Catalog{path: trimmed, logger: logger}
}

// Lookup returns an override matching the fingerprint or DiscID.
func (c *Catalog) Lookup(fingerprint, discID string) (Override, bool, error) {
	if c == nil || strings.TrimSpace(c.path) == "" {
		return Override{}, false, nil
	}
	if err := c.ensureLoaded(); err != nil {
		return Override{}, false, err
	}
	fp := strings.ToUpper(strings.TrimSpace(fingerprint))
	disc := strings.ToUpper(strings.TrimSpace(discID))

	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, entry := range c.entries {
		if entry.matches(fp, disc) {
			return entry, true, nil
		}
	}
	return Override{}, false, nil
}

func (c *Catalog) ensureLoaded() error {
	c.mu.RLock()
	path := c.path
	c.mu.RUnlock()

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	c.mu.RLock()
	alreadyLoaded := !c.loaded.IsZero() && c.loaded.Equal(info.ModTime())
	c.mu.RUnlock()
	if alreadyLoaded {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}

	entries, err := parseOverrides(data)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.entries = entries
	c.loaded = info.ModTime()
	c.mu.Unlock()
	if c.logger != nil {
		c.logger.Info("loaded identification overrides", slog.String("path", path), slog.Int("count", len(entries)))
	}
	return nil
}

func parseOverrides(data []byte) ([]Override, error) {
	data = bytesTrimUTF8BOM(data)
	if len(bytesTrimSpace(data)) == 0 {
		return nil, nil
	}
	var entries []Override
	// Accept either array or object with overrides field.
	if len(data) > 0 && data[0] == '{' {
		var wrapper struct {
			Overrides []Override `json:"overrides"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return nil, err
		}
		entries = wrapper.Overrides
	} else {
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, err
		}
	}
	normalized := make([]Override, 0, len(entries))
	for _, entry := range entries {
		entry.normalize()
		normalized = append(normalized, entry)
	}
	return normalized, nil
}

func (o *Override) matches(fingerprint, discID string) bool {
	for _, fp := range o.Fingerprints {
		if fp != "" && fp == fingerprint {
			return true
		}
	}
	for _, disc := range o.DiscIDs {
		if disc != "" && disc == discID {
			return true
		}
	}
	return false
}

func (o *Override) normalize() {
	o.Title = strings.TrimSpace(o.Title)
	o.MediaType = strings.ToLower(strings.TrimSpace(o.MediaType))
	normalizeList := func(values []string) []string {
		cleaned := make([]string, 0, len(values))
		for _, value := range values {
			trimmed := strings.ToUpper(strings.TrimSpace(value))
			if trimmed == "" {
				continue
			}
			cleaned = append(cleaned, trimmed)
		}
		return cleaned
	}
	o.Fingerprints = normalizeList(o.Fingerprints)
	o.DiscIDs = normalizeList(o.DiscIDs)
}

func bytesTrimUTF8BOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}

func bytesTrimSpace(data []byte) []byte {
	start := 0
	for start < len(data) && (data[start] == ' ' || data[start] == '\n' || data[start] == '\t' || data[start] == '\r') {
		start++
	}
	end := len(data)
	for end > start && (data[end-1] == ' ' || data[end-1] == '\n' || data[end-1] == '\t' || data[end-1] == '\r') {
		end--
	}
	return data[start:end]
}
