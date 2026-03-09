// Package keydb provides KeyDB catalog management for Blu-ray disc identification.
// KeyDB is a database mapping disc IDs to human-readable titles.
package keydb

import (
	"archive/zip"
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Entry represents a KeyDB catalog entry.
type Entry struct {
	DiscID string
	Title  string
}

// Catalog holds the parsed KeyDB data.
type Catalog struct {
	entries map[string]string // discID -> title
}

// Lookup finds a title by disc ID. Returns empty string if not found.
func (c *Catalog) Lookup(discID string) string {
	if c == nil {
		return ""
	}
	return c.entries[discID]
}

// Size returns the number of entries in the catalog.
func (c *Catalog) Size() int {
	if c == nil {
		return 0
	}
	return len(c.entries)
}

// LoadFromFile parses a KEYDB.cfg file and returns a Catalog.
// Lines have the format: discID | title | extra...
// Comment lines (starting with ;) and malformed lines are skipped.
func LoadFromFile(path string) (*Catalog, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("keydb: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	entries := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 2 {
			continue
		}
		discID := strings.TrimSpace(parts[0])
		title := strings.TrimSpace(parts[1])
		if discID == "" || title == "" {
			continue
		}
		entries[discID] = title
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("keydb: scan %s: %w", path, err)
	}

	return &Catalog{entries: entries}, nil
}

// Download fetches a KeyDB zip file from url and extracts KEYDB.cfg into destDir.
// The destDir is created if it does not exist.
func Download(ctx context.Context, url, destDir string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("keydb: create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("keydb: download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("keydb: download: HTTP %d", resp.StatusCode)
	}

	// Write response to a temporary file so zip.OpenReader can seek.
	tmpFile, err := os.CreateTemp("", "keydb-*.zip")
	if err != nil {
		return fmt.Errorf("keydb: create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("keydb: write temp file: %w", err)
	}
	_ = tmpFile.Close()

	zr, err := zip.OpenReader(tmpPath)
	if err != nil {
		return fmt.Errorf("keydb: open zip: %w", err)
	}
	defer func() { _ = zr.Close() }()

	for _, zf := range zr.File {
		if strings.EqualFold(filepath.Base(zf.Name), "KEYDB.cfg") {
			if err := os.MkdirAll(destDir, 0o755); err != nil {
				return fmt.Errorf("keydb: create dir %s: %w", destDir, err)
			}
			return extractFile(zf, filepath.Join(destDir, "KEYDB.cfg"))
		}
	}

	return fmt.Errorf("keydb: KEYDB.cfg not found in zip")
}

func extractFile(zf *zip.File, dest string) error {
	rc, err := zf.Open()
	if err != nil {
		return fmt.Errorf("keydb: open zip entry: %w", err)
	}
	defer func() { _ = rc.Close() }()

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("keydb: create %s: %w", dest, err)
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("keydb: extract to %s: %w", dest, err)
	}
	return nil
}

// LoadOrDownload tries to load a catalog from path. If the file does not exist,
// it downloads the KeyDB zip from url, extracts it to the directory containing
// path, then loads the result.
func LoadOrDownload(ctx context.Context, path, url string, timeout time.Duration) (*Catalog, error) {
	cat, err := LoadFromFile(path)
	if err == nil {
		return cat, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	destDir := filepath.Dir(path)
	if err := Download(ctx, url, destDir, timeout); err != nil {
		return nil, err
	}
	return LoadFromFile(path)
}
