package keydb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookupNilCatalog(t *testing.T) {
	var c *Catalog
	if got := c.Lookup("anything"); got != "" {
		t.Errorf("Lookup on nil catalog = %q, want empty", got)
	}
}

func TestSizeNilCatalog(t *testing.T) {
	var c *Catalog
	if got := c.Size(); got != 0 {
		t.Errorf("Size on nil catalog = %d, want 0", got)
	}
}

const sampleKeyDB = `; This is a comment
; Another comment

A1B2C3D4E5 | The Matrix | extra data | more stuff
F6G7H8I9J0 | Inception
DEADBEEF | Blade Runner 2049 | director cut

; malformed lines below
no-pipe-here
 |  | empty fields
||
`

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "KEYDB.cfg")
	if err := os.WriteFile(path, []byte(sampleKeyDB), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}

	if got := cat.Size(); got != 3 {
		t.Errorf("Size = %d, want 3", got)
	}
}

func TestLookupFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "KEYDB.cfg")
	if err := os.WriteFile(path, []byte(sampleKeyDB), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, err := LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		discID string
		want   string
	}{
		{"A1B2C3D4E5", "The Matrix"},
		{"F6G7H8I9J0", "Inception"},
		{"DEADBEEF", "Blade Runner 2049"},
	}
	for _, tt := range tests {
		if got := cat.Lookup(tt.discID); got != tt.want {
			t.Errorf("Lookup(%q) = %q, want %q", tt.discID, got, tt.want)
		}
	}
}

func TestLookupNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "KEYDB.cfg")
	if err := os.WriteFile(path, []byte(sampleKeyDB), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, err := LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := cat.Lookup("NONEXISTENT"); got != "" {
		t.Errorf("Lookup(NONEXISTENT) = %q, want empty", got)
	}
}

func TestMalformedLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "KEYDB.cfg")

	content := `no-pipe
| only-title
 |  |
VALID1 | Good Title
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}

	if got := cat.Size(); got != 1 {
		t.Errorf("Size = %d, want 1 (only valid line)", got)
	}
	if got := cat.Lookup("VALID1"); got != "Good Title" {
		t.Errorf("Lookup(VALID1) = %q, want %q", got, "Good Title")
	}
}

func TestCommentLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "KEYDB.cfg")

	content := `; comment one
;comment two
ABC123 | Real Entry
; another comment
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}

	if got := cat.Size(); got != 1 {
		t.Errorf("Size = %d, want 1", got)
	}
	if got := cat.Lookup("ABC123"); got != "Real Entry" {
		t.Errorf("Lookup(ABC123) = %q, want %q", got, "Real Entry")
	}
}

func TestLoadFromFileNotExist(t *testing.T) {
	_, err := LoadFromFile("/nonexistent/path/KEYDB.cfg")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}
