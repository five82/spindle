package keydb

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"

	"spindle/internal/logging"
)

const (
	defaultCatalogMaxAge   = 7 * 24 * time.Hour
	defaultDownloadURL     = "http://fvonline-db.bplaced.net/export/keydb_eng.zip"
	defaultDownloadTimeout = 5 * time.Minute
)

// Entry represents a single KEYDB record.
type Entry struct {
	DiscID string
	Title  string
	Raw    string
}

// Catalog lazily loads and caches KEYDB entries for lookup by Disc ID.
type Catalog struct {
	path        string
	mu          sync.RWMutex
	entries     map[string]Entry
	modTime     time.Time
	maxAge      time.Duration
	refresh     sync.Mutex
	refreshing  atomic.Bool
	logger      *slog.Logger
	downloadURL string
	client      *http.Client
}

// NewCatalog creates a catalog for the provided KEYDB path. If the path is empty, nil is returned.
func NewCatalog(path string, logger *slog.Logger, downloadURL string, timeout time.Duration) *Catalog {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	if strings.TrimSpace(downloadURL) == "" {
		downloadURL = defaultDownloadURL
	}
	if timeout <= 0 {
		timeout = defaultDownloadTimeout
	}
	return &Catalog{
		path:        trimmed,
		maxAge:      defaultCatalogMaxAge,
		logger:      logger,
		downloadURL: strings.TrimSpace(downloadURL),
		client:      &http.Client{Timeout: timeout},
	}
}

// Lookup returns a KEYDB entry for the given Disc ID, if present.
func (c *Catalog) Lookup(discID string) (Entry, bool, error) {
	if c == nil {
		return Entry{}, false, nil
	}
	normalized := strings.ToUpper(strings.TrimSpace(discID))
	if normalized == "" {
		return Entry{}, false, nil
	}
	if err := c.ensureLoaded(); err != nil {
		return Entry{}, false, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[normalized]
	return entry, ok, nil
}

func (c *Catalog) ensureLoaded() error {
	info, err := os.Stat(c.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if c.logger != nil {
				c.logger.Debug("refreshing keydb catalog", slog.String("path", c.path))
			}
			if err := c.refreshRemote(); err != nil {
				if c.logger != nil {
					c.logger.Warn("keydb refresh failed; keydb lookups may be stale",
						slog.String("path", c.path),
						logging.Error(err),
						logging.String(logging.FieldEventType, "keydb_refresh_failed"),
						logging.String(logging.FieldErrorHint, "check network access or set keydb_download_url"),
					)
				}
				c.mu.Lock()
				c.entries = map[string]Entry{}
				c.modTime = time.Time{}
				c.mu.Unlock()
				return nil
			}

			info, err = os.Stat(c.path)
			if err != nil {
				c.mu.Lock()
				c.entries = map[string]Entry{}
				c.modTime = time.Time{}
				c.mu.Unlock()
				return nil
			}
		} else {
			return err
		}
	}

	// Always load from disk if the file exists and hasn't been loaded yet.
	c.mu.RLock()
	alreadyLoaded := c.entries != nil && c.modTime.Equal(info.ModTime())
	c.mu.RUnlock()
	if alreadyLoaded {
		c.refreshRemoteAsync()
		return nil
	}

	if err := c.loadFromDisk(info); err != nil {
		return err
	}
	c.refreshRemoteAsync()
	return nil
}

func (c *Catalog) needsRefresh() (bool, error) {
	info, err := os.Stat(c.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	if c.maxAge <= 0 {
		return false, nil
	}
	return time.Since(info.ModTime()) > c.maxAge, nil
}

func (c *Catalog) refreshRemoteAsync() {
	needsRefresh, err := c.needsRefresh()
	if err != nil || !needsRefresh {
		return
	}
	if !c.refreshing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.refreshing.Store(false)
		if c.logger != nil {
			c.logger.Debug("refreshing keydb catalog", slog.String("path", c.path))
		}
		if err := c.refreshRemote(); err != nil {
			if c.logger != nil {
				c.logger.Warn("keydb refresh failed; keydb lookups may be stale",
					slog.String("path", c.path),
					logging.Error(err),
					logging.String(logging.FieldEventType, "keydb_refresh_failed"),
					logging.String(logging.FieldErrorHint, "check network access or set keydb_download_url"),
				)
			}
		}
	}()
}

func (c *Catalog) loadFromDisk(info fs.FileInfo) error {
	file, err := os.Open(c.path)
	if err != nil {
		return err
	}
	defer file.Close()

	entries := map[string]Entry{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		discIDRaw := strings.ToUpper(strings.TrimSpace(parts[0]))
		if strings.HasPrefix(discIDRaw, "0X") {
			discIDRaw = strings.TrimSpace(discIDRaw[2:])
		}
		if len(discIDRaw) != 40 {
			continue
		}
		if _, err := hex.DecodeString(discIDRaw); err != nil {
			continue
		}
		payload := strings.TrimSpace(parts[1])
		if payload == "" {
			continue
		}
		title := payload
		if fields := strings.SplitN(payload, "|", 2); len(fields) > 0 {
			title = strings.TrimSpace(fields[0])
		}
		if alias := extractAlias(title); alias != "" {
			title = alias
		} else {
			title = strings.TrimSpace(stripAlias(title))
		}
		if title == "" {
			title = payload
		}
		entries[discIDRaw] = Entry{
			DiscID: discIDRaw,
			Title:  title,
			Raw:    payload,
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	c.entries = entries
	c.modTime = info.ModTime()
	c.mu.Unlock()

	return nil
}

func (c *Catalog) refreshRemote() error {
	c.refresh.Lock()
	defer c.refresh.Unlock()

	if c.logger != nil {
		c.logger.Debug("downloading keydb catalog", slog.String("url", c.downloadURL))
	}
	client := c.client
	if client == nil {
		client = &http.Client{Timeout: defaultDownloadTimeout}
	}
	resp, err := client.Get(c.downloadURL)
	if err != nil {
		return fmt.Errorf("download keydb: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download keydb: unexpected status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("download keydb: %w", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("open keydb archive: %w", err)
	}

	var cfgData []byte
	for _, file := range zr.File {
		if strings.EqualFold(file.Name, "KEYDB.cfg") {
			rc, err := file.Open()
			if err != nil {
				return fmt.Errorf("open keydb entry: %w", err)
			}
			cfgData, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return fmt.Errorf("read keydb entry: %w", err)
			}
			break
		}
	}

	if len(cfgData) == 0 {
		return errors.New("keydb archive missing KEYDB.cfg")
	}

	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return fmt.Errorf("create keydb directory: %w", err)
	}

	tempPath := c.path + ".tmp"
	if err := os.WriteFile(tempPath, cfgData, 0o644); err != nil {
		return fmt.Errorf("write keydb temp file: %w", err)
	}
	if err := os.Rename(tempPath, c.path); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("replace keydb file: %w", err)
	}

	if c.logger != nil {
		c.logger.Debug("keydb catalog refreshed", slog.String("path", c.path), slog.Int("bytes", len(cfgData)))
	}
	return nil
}

func extractAlias(title string) string {
	start := strings.Index(title, "[")
	if start == -1 {
		return ""
	}
	end := strings.Index(title[start:], "]")
	if end == -1 {
		return ""
	}
	alias := strings.TrimSpace(title[start+1 : start+end])
	return alias
}

func stripAlias(title string) string {
	if idx := strings.Index(title, "["); idx != -1 {
		return strings.TrimSpace(title[:idx])
	}
	return title
}
