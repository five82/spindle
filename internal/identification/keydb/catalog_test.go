package keydb

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const sampleDiscID = "0123456789ABCDEF0123456789ABCDEF01234567"

func TestCatalogLookupParsesEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keydb.cfg")
	content := []byte("# comment\n0x" + sampleDiscID + "=Main Title [Alias]|extra\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write keydb: %v", err)
	}
	catalog := NewCatalog(path, nil, "", 0)

	entry, ok, err := catalog.Lookup(sampleDiscID)
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}
	if !ok {
		t.Fatal("expected lookup to find entry")
	}
	if entry.DiscID != sampleDiscID {
		t.Fatalf("unexpected disc id: %q", entry.DiscID)
	}
	if entry.Title != "Alias" {
		t.Fatalf("unexpected title: %q", entry.Title)
	}
}

func TestCatalogLookupNormalizesDuplicateTitle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keydb.cfg")
	content := []byte("0x" + sampleDiscID + "=Goodfellas (1990) (Goodfellas (1990))\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write keydb: %v", err)
	}
	catalog := NewCatalog(path, nil, "", 0)

	entry, ok, err := catalog.Lookup(sampleDiscID)
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}
	if !ok {
		t.Fatal("expected lookup to find entry")
	}
	if entry.Title != "Goodfellas (1990)" {
		t.Fatalf("unexpected title: %q", entry.Title)
	}
}

func TestCatalogRefreshesRemoteWhenMissing(t *testing.T) {
	zipData := buildKeyDBZip(t, []byte(sampleDiscID+"=Remote Title\n"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(zipData)
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "missing.cfg")
	catalog := NewCatalog(path, nil, server.URL, time.Second)

	entry, ok, err := catalog.Lookup(sampleDiscID)
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}
	if !ok || entry.Title != "Remote Title" {
		t.Fatalf("expected remote title, got %+v", entry)
	}
}

func buildKeyDBZip(t *testing.T, cfg []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writer, err := zw.Create("KEYDB.cfg")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := io.Copy(writer, bytes.NewReader(cfg)); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}
