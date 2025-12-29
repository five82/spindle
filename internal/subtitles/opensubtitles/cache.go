package opensubtitles

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"log/slog"
)

// CacheEntry captures metadata about a cached OpenSubtitles download.
type CacheEntry struct {
	FileID       int64     `json:"file_id"`
	Language     string    `json:"language"`
	FileName     string    `json:"file_name"`
	DownloadURL  string    `json:"download_url"`
	TMDBID       int64     `json:"tmdb_id"`
	ParentTMDBID int64     `json:"parent_tmdb_id"`
	Season       int       `json:"season"`
	Episode      int       `json:"episode"`
	FeatureTitle string    `json:"feature_title"`
	FeatureYear  int       `json:"feature_year"`
	StoredAt     time.Time `json:"stored_at"`
}

// CacheResult represents a cache hit including the subtitle payload.
type CacheResult struct {
	Entry CacheEntry
	Data  []byte
	Path  string
}

// DownloadResult converts a cached payload into a DownloadResult structure.
func (r CacheResult) DownloadResult() DownloadResult {
	return DownloadResult{
		Data:        append([]byte(nil), r.Data...),
		FileName:    r.Entry.FileName,
		Language:    r.Entry.Language,
		DownloadURL: r.Entry.DownloadURL,
	}
}

// Cache persists OpenSubtitles payloads locally to avoid repeat downloads.
type Cache struct {
	dir    string
	logger *slog.Logger
}

// NewCache initialises a cache rooted at dir.
func NewCache(dir string, logger *slog.Logger) (*Cache, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("cache directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	return &Cache{dir: dir, logger: logger}, nil
}

// Dir exposes the backing directory for inspection.
func (c *Cache) Dir() string {
	if c == nil {
		return ""
	}
	return c.dir
}

// Load returns the cached payload for fileID when present.
func (c *Cache) Load(fileID int64) (CacheResult, bool, error) {
	if c == nil {
		return CacheResult{}, false, errors.New("cache unavailable")
	}
	if fileID <= 0 {
		return CacheResult{}, false, errors.New("invalid file id")
	}
	dataPath := c.dataPath(fileID)
	data, err := os.ReadFile(dataPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CacheResult{}, false, nil
		}
		return CacheResult{}, false, fmt.Errorf("read cache data: %w", err)
	}
	metaPath := c.metaPath(fileID)
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// treat as miss so caller can refresh from API
			_ = os.Remove(dataPath)
			return CacheResult{}, false, nil
		}
		return CacheResult{}, false, fmt.Errorf("read cache metadata: %w", err)
	}
	var entry CacheEntry
	if err := json.Unmarshal(metaBytes, &entry); err != nil {
		return CacheResult{}, false, fmt.Errorf("decode cache metadata: %w", err)
	}
	if entry.FileID == 0 {
		entry.FileID = fileID
	}
	return CacheResult{
		Entry: entry,
		Data:  data,
		Path:  dataPath,
	}, true, nil
}

// Store writes the supplied payload into the cache and returns the data path.
func (c *Cache) Store(entry CacheEntry, data []byte) (string, error) {
	if c == nil {
		return "", errors.New("cache unavailable")
	}
	if entry.FileID <= 0 {
		return "", errors.New("invalid file id")
	}
	entry.Language = strings.TrimSpace(entry.Language)
	entry.FileName = strings.TrimSpace(entry.FileName)
	entry.DownloadURL = strings.TrimSpace(entry.DownloadURL)
	entry.StoredAt = time.Now().UTC()
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return "", fmt.Errorf("ensure cache dir: %w", err)
	}
	dataPath := c.dataPath(entry.FileID)
	if err := writeFileAtomic(dataPath, data, 0o644); err != nil {
		return "", err
	}
	metaPath := c.metaPath(entry.FileID)
	metaBytes, err := json.Marshal(entry)
	if err != nil {
		return "", fmt.Errorf("encode metadata: %w", err)
	}
	if err := writeFileAtomic(metaPath, metaBytes, 0o644); err != nil {
		return "", err
	}
	if c.logger != nil {
		c.logger.Debug("opensubtitles cache stored",
			slog.Int64("file_id", entry.FileID),
			slog.String("path", dataPath),
			slog.String("language", entry.Language),
		)
	}
	return dataPath, nil
}

func (c *Cache) dataPath(fileID int64) string {
	return filepath.Join(c.dir, fmt.Sprintf("%d.srt", fileID))
}

func (c *Cache) metaPath(fileID int64) string {
	return filepath.Join(c.dir, fmt.Sprintf("%d.json", fileID))
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "cache-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
