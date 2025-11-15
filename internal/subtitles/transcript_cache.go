package subtitles

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/logging"
)

type transcriptCache struct {
	dir    string
	logger *slog.Logger
}

type transcriptCacheEntry struct {
	Key      string    `json:"key"`
	Language string    `json:"language"`
	Segments int       `json:"segments"`
	Updated  time.Time `json:"updated"`
	path     string    `json:"-"`
}

func newTranscriptCache(dir string, logger *slog.Logger) (*transcriptCache, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("transcript cache directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("ensure transcript cache dir: %w", err)
	}
	return &transcriptCache{dir: dir, logger: logger}, nil
}

func (c *transcriptCache) Load(key string) ([]byte, transcriptCacheEntry, bool, error) {
	var entry transcriptCacheEntry
	if c == nil {
		return nil, entry, false, nil
	}
	path := c.dataPath(key)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, entry, false, nil
		}
		return nil, entry, false, fmt.Errorf("read transcript cache %s: %w", key, err)
	}
	meta, err := c.readMeta(key)
	if err == nil {
		entry = meta
	}
	entry.path = path
	return data, entry, true, nil
}

func (c *transcriptCache) Store(key, language string, segments int, data []byte) (string, error) {
	if c == nil {
		return "", nil
	}
	path := c.dataPath(key)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", fmt.Errorf("write transcript cache temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("rename transcript cache file: %w", err)
	}
	meta := transcriptCacheEntry{
		Key:      key,
		Language: language,
		Segments: segments,
		Updated:  time.Now().UTC(),
	}
	if err := c.writeMeta(key, meta); err != nil && c.logger != nil {
		c.logger.Warn("transcript cache metadata write failed", logging.Error(err))
	}
	return path, nil
}

func (c *transcriptCache) dataPath(key string) string {
	hash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(key))))
	name := hex.EncodeToString(hash[:])
	return filepath.Join(c.dir, name+".srt")
}

func (c *transcriptCache) metaPath(key string) string {
	hash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(key))))
	name := hex.EncodeToString(hash[:])
	return filepath.Join(c.dir, name+".json")
}

func (c *transcriptCache) writeMeta(key string, entry transcriptCacheEntry) error {
	path := c.metaPath(key)
	payload, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal transcript cache metadata: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return fmt.Errorf("write transcript cache metadata temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename transcript cache metadata: %w", err)
	}
	return nil
}

func (c *transcriptCache) readMeta(key string) (transcriptCacheEntry, error) {
	path := c.metaPath(key)
	data, err := os.ReadFile(path)
	if err != nil {
		return transcriptCacheEntry{}, err
	}
	var entry transcriptCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return transcriptCacheEntry{}, err
	}
	return entry, nil
}
