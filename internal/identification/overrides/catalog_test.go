package overrides

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseOverridesAcceptsWrapperAndNormalizes(t *testing.T) {
	data := []byte("\xEF\xBB\xBF{ \"overrides\": [{\"fingerprints\":[\" abcd \"],\"disc_ids\":[\" 0x123 \"],\"title\":\" Test \",\"media_type\":\"TV\"}]}") // BOM + wrapper
	entries, err := parseOverrides(data)
	if err != nil {
		t.Fatalf("parseOverrides failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.MediaType != "tv" {
		t.Fatalf("expected media_type normalized to tv, got %q", entry.MediaType)
	}
	if len(entry.Fingerprints) != 1 || entry.Fingerprints[0] != "ABCD" {
		t.Fatalf("expected fingerprints normalized, got %+v", entry.Fingerprints)
	}
	if len(entry.DiscIDs) != 1 || entry.DiscIDs[0] != "0X123" {
		t.Fatalf("expected disc_ids normalized, got %+v", entry.DiscIDs)
	}
}

func TestCatalogLookupMatchesFingerprintOrDiscID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.json")
	data := []byte(`[{"fingerprints":[" FP1 "],"disc_ids":["0xABC"],"title":"Show","media_type":"tv","tmdb_id":42}]`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write overrides: %v", err)
	}
	catalog := NewCatalog(path, nil)
	match, ok, err := catalog.Lookup("fp1", "")
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}
	if !ok || match.TMDBID != 42 {
		t.Fatalf("expected fingerprint match, got %+v", match)
	}
	match, ok, err = catalog.Lookup("", "0xabc")
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}
	if !ok || match.Title != "Show" {
		t.Fatalf("expected disc id match, got %+v", match)
	}
}
