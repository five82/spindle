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

	"github.com/five82/spindle/internal/logs"
)

const refreshAge = 7 * 24 * time.Hour

// Entry represents a KeyDB catalog entry.
type Entry struct {
	DiscID string
	Title  string
}

// Catalog holds the parsed KeyDB data.
type Catalog struct {
	entries    map[string]string // discID -> title
	sourcePath string
	modTime    time.Time
}

// Lookup finds a title by disc ID and logs the item-scoped decision. It returns
// empty string if not found. The disc ID is normalized (0X prefix stripped,
// uppercased, validated as 40 hex chars).
func (c *Catalog) Lookup(discID string, logger *slog.Logger) string {
	if c == nil {
		return ""
	}
	normalized, ok := normalizeDiscID(discID)
	if !ok {
		return ""
	}
	logger = logs.Default(logger)
	title := c.entries[normalized]
	if title != "" {
		logger.Info("KeyDB lookup hit",
			"decision_type", logs.DecisionKeyDBLookup,
			"decision_result", "hit",
			"decision_reason", fmt.Sprintf("matched title=%q", title),
			"disc_id", normalized,
			"title", title,
		)
	} else {
		logger.Info("KeyDB lookup miss",
			"decision_type", logs.DecisionKeyDBLookup,
			"decision_result", "miss",
			"decision_reason", "not in catalog",
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
// Disc rows have the format: 0xdiscID = title | attributes...
// Comment, key-material, and malformed lines are skipped. A file containing no
// valid disc rows is rejected rather than silently disabling KeyDB lookups.
// Disc IDs are normalized (0X prefix stripped, uppercased, validated as 40 hex chars).
// Titles are cleaned via the title extraction chain.
// If stale is true, the file is older than 7 days and should be re-downloaded.
func LoadFromFile(path string) (cat *Catalog, stale bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("keydb: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var modTime time.Time
	if info, statErr := f.Stat(); statErr == nil {
		modTime = info.ModTime()
		stale = time.Since(modTime) > refreshAge
	}
	entries := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) < 2 {
			continue
		}
		rawID, rawTitle, ok := strings.Cut(parts[0], "=")
		if !ok {
			continue
		}
		rawID = strings.TrimSpace(rawID)
		rawTitle = strings.TrimSpace(rawTitle)
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
	if len(entries) == 0 {
		return nil, false, fmt.Errorf("keydb: no valid disc entries in %s", path)
	}

	return &Catalog{
		entries:    entries,
		sourcePath: path,
		modTime:    modTime,
	}, stale, nil
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
	if t := unwrapVolumeAlias(raw); t != "" {
		return t
	}
	return raw
}

// unwrapVolumeAlias extracts the parenthesized display name when the prefix is a
// raw volume identifier, the most common KeyDB row shape:
// "MARY_POPPINS_50TH_ANNIVERSARY (Mary Poppins 50th Anniversary Edition)" ->
// "Mary Poppins 50th Anniversary Edition". The volume ID is useless as a TMDB
// query, and KeyDB titles feed the search directly. Requiring an underscore in
// an all-caps prefix keeps genuinely short all-caps titles ("JFK (Director's
// Cut)") from having their name mistaken for the volume ID.
func unwrapVolumeAlias(title string) string {
	if !strings.HasSuffix(title, ")") {
		return ""
	}
	start := strings.IndexByte(title, '(')
	if start < 1 {
		return ""
	}
	prefix := strings.TrimSpace(title[:start])
	if !strings.Contains(prefix, "_") || strings.ToUpper(prefix) != prefix {
		return ""
	}
	return strings.TrimSpace(title[start+1 : len(title)-1])
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
func Download(ctx context.Context, url, destDir string, timeout time.Duration, logger *slog.Logger) error {
	logger = logs.Default(logger)
	logger.Info("KeyDB download started",
		"event_type", "keydb_download_start",
	)

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
			if err := extractFile(zf, filepath.Join(destDir, "KEYDB.cfg")); err != nil {
				return err
			}
			logger.Info("KeyDB download completed",
				"event_type", "keydb_download_complete",
				"dest_dir", destDir,
			)
			return nil
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

// LoadOrDownload returns current when it still represents the fresh file at
// path. Otherwise it loads the file and, if it is missing or older than 7 days,
// downloads a fresh copy before loading it. A stale current catalog remains
// available as the fallback when the refresh fails.
func LoadOrDownload(ctx context.Context, path, url string, timeout time.Duration, current *Catalog, logger *slog.Logger) (*Catalog, bool, error) {
	logger = logs.Default(logger)

	var cat *Catalog
	var stale bool
	info, statErr := os.Stat(path)
	if statErr == nil && current != nil && current.sourcePath == path && current.modTime.Equal(info.ModTime()) {
		stale = time.Since(info.ModTime()) > refreshAge
		if !stale {
			return current, false, nil
		}
		cat = current
	} else {
		var err error
		cat, stale, err = LoadFromFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, false, err
		}
		if err == nil && !stale {
			return cat, false, nil
		}
		if os.IsNotExist(err) && current != nil && current.sourcePath == path {
			cat = current
		}
	}

	reason := "file_missing"
	if stale {
		reason = "catalog_stale"
	}
	logger.Info("KeyDB catalog needs refresh",
		"decision_type", "keydb_refresh",
		"decision_result", reason,
		"decision_reason", fmt.Sprintf("path=%s", path),
	)

	destDir := filepath.Dir(path)
	if dlErr := Download(ctx, url, destDir, timeout, logger); dlErr != nil {
		// If we have a stale catalog, use it rather than failing.
		if cat != nil {
			logger.Warn("KeyDB download failed, using stale catalog",
				"event_type", "keydb_download_error",
				"error_hint", dlErr.Error(),
				"impact", "disc identification may use outdated data",
			)
			return cat, true, nil
		}
		return nil, false, dlErr
	}
	cat, stale, err := LoadFromFile(path)
	return cat, stale, err
}
