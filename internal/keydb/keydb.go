// Package keydb provides KeyDB catalog management for Blu-ray disc identification.
// KeyDB is a database mapping disc IDs to human-readable titles.
package keydb

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
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
	logger  *slog.Logger
}

// Lookup finds a title by disc ID. Returns empty string if not found.
// The disc ID is normalized (0X prefix stripped, uppercased, validated as 40 hex chars).
func (c *Catalog) Lookup(discID string) string {
	if c == nil {
		return ""
	}
	normalized, ok := normalizeDiscID(discID)
	if !ok {
		return ""
	}
	title := c.entries[normalized]
	if title != "" {
		c.logger.Info("KeyDB lookup hit",
			"decision_type", "keydb_lookup",
			"decision_result", "hit",
			"disc_id", normalized,
			"title", title,
		)
	} else {
		c.logger.Debug("KeyDB lookup miss",
			"decision_type", "keydb_lookup",
			"decision_result", "miss",
			"disc_id", normalized,
		)
	}
	return title
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
// Disc IDs are normalized (0X prefix stripped, uppercased, validated as 40 hex chars).
// Titles are cleaned via the title extraction chain.
// If stale is true, the file is older than 7 days and should be re-downloaded.
// If logger is nil, slog.Default() is used.
func LoadFromFile(path string, logger *slog.Logger) (cat *Catalog, stale bool, err error) {
	if logger == nil {
		logger = slog.Default()
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("keydb: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// Check staleness.
	if info, statErr := f.Stat(); statErr == nil {
		stale = time.Since(info.ModTime()) > 7*24*time.Hour
	}
	if stale {
		logger.Warn("KeyDB catalog is stale",
			"event_type", "keydb_stale",
			"error_hint", "catalog file older than 7 days",
			"impact", "disc identification may use outdated data",
		)
	}

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
		rawID := strings.TrimSpace(parts[0])
		rawTitle := strings.TrimSpace(parts[1])
		if rawID == "" || rawTitle == "" {
			continue
		}
		discID, ok := normalizeDiscID(rawID)
		if !ok {
			continue
		}
		entries[discID] = cleanTitle(rawTitle)
	}
	if err := scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("keydb: scan %s: %w", path, err)
	}

	return &Catalog{entries: entries, logger: logger}, stale, nil
}

// normalizeDiscID strips a 0X prefix, validates exactly 40 hex characters,
// and returns the uppercased ID.
func normalizeDiscID(raw string) (string, bool) {
	s := strings.TrimPrefix(raw, "0X")
	s = strings.TrimPrefix(s, "0x")
	if len(s) != 40 {
		return "", false
	}
	// Validate hex characters.
	if _, err := hex.DecodeString(s); err != nil {
		return "", false
	}
	return strings.ToUpper(s), true
}

// cleanTitle applies the title extraction chain. First non-empty result wins.
func cleanTitle(raw string) string {
	if t := extractAlias(raw); t != "" {
		return t
	}
	if t := stripAlias(raw); t != "" {
		return t
	}
	if t := normalizeDuplicateTitle(raw); t != "" {
		return t
	}
	return raw
}

// extractAlias extracts bracketed content as the title alias.
// e.g. "Foo [Bar]" -> "Bar"
func extractAlias(title string) string {
	start := strings.IndexByte(title, '[')
	if start < 0 {
		return ""
	}
	end := strings.LastIndexByte(title, ']')
	if end <= start+1 {
		return ""
	}
	return strings.TrimSpace(title[start+1 : end])
}

// stripAlias strips everything from the first '[' onward.
// e.g. "Foo [extra]" -> "Foo"
func stripAlias(title string) string {
	idx := strings.IndexByte(title, '[')
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(title[:idx])
}

// normalizeDuplicateTitle unwraps "Title (Title)" patterns where the
// parenthesized suffix exactly matches the prefix.
// e.g. "Movie (Movie)" -> "Movie"
func normalizeDuplicateTitle(title string) string {
	// Find the last balanced parenthesized group.
	end := len(title) - 1
	if end < 2 || title[end] != ')' {
		return ""
	}
	depth := 0
	start := -1
	for i := end; i >= 0; i-- {
		if title[i] == ')' {
			depth++
		} else if title[i] == '(' {
			depth--
			if depth == 0 {
				start = i
				break
			}
		}
	}
	if start < 1 {
		return ""
	}
	prefix := strings.TrimSpace(title[:start])
	inner := strings.TrimSpace(title[start+1 : end])
	if strings.EqualFold(prefix, inner) {
		return prefix
	}
	return ""
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
// If logger is nil, slog.Default() is used.
func LoadOrDownload(ctx context.Context, path, url string, timeout time.Duration, logger *slog.Logger) (*Catalog, bool, error) {
	cat, stale, err := LoadFromFile(path, logger)
	if err == nil {
		return cat, stale, nil
	}
	if !os.IsNotExist(err) {
		return nil, false, err
	}

	destDir := filepath.Dir(path)
	if err := Download(ctx, url, destDir, timeout); err != nil {
		return nil, false, err
	}
	cat, stale, err = LoadFromFile(path, logger)
	return cat, stale, err
}
