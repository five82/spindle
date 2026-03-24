package keydb

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

// Valid 40-char hex IDs for testing.
const (
	hexID1 = "A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2"
	hexID2 = "1234567890ABCDEF1234567890ABCDEF12345678"
	hexID3 = "DEADBEEFDEADBEEFDEADBEEFDEADBEEFDEADBEEF"
)

var sampleKeyDB = "; This is a comment\n" +
	"; Another comment\n" +
	"\n" +
	hexID1 + " | The Matrix | extra data | more stuff\n" +
	hexID2 + " | Inception\n" +
	hexID3 + " | Blade Runner 2049 | director cut\n" +
	"\n" +
	"; malformed lines below\n" +
	"no-pipe-here\n" +
	" |  | empty fields\n" +
	"||\n"

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "KEYDB.cfg")
	if err := os.WriteFile(path, []byte(sampleKeyDB), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, stale, err := LoadFromFile(path, nil)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if stale {
		t.Error("expected stale=false for freshly written file")
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

	cat, _, err := LoadFromFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		discID string
		want   string
	}{
		{hexID1, "The Matrix"},
		{hexID2, "Inception"},
		{hexID3, "Blade Runner 2049"},
	}
	for _, tt := range tests {
		if got := cat.Lookup(tt.discID); got != tt.want {
			t.Errorf("Lookup(%q) = %q, want %q", tt.discID, got, tt.want)
		}
	}
}

func TestLookupWithPrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "KEYDB.cfg")
	if err := os.WriteFile(path, []byte(sampleKeyDB), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, _, err := LoadFromFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Lookup with 0X prefix should still find the entry.
	if got := cat.Lookup("0X" + hexID1); got != "The Matrix" {
		t.Errorf("Lookup with 0X prefix = %q, want %q", got, "The Matrix")
	}
	// Lowercase should also work.
	lower := "0x" + "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	if got := cat.Lookup(lower); got != "The Matrix" {
		t.Errorf("Lookup with lowercase = %q, want %q", got, "The Matrix")
	}
}

func TestLookupNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "KEYDB.cfg")
	if err := os.WriteFile(path, []byte(sampleKeyDB), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, _, err := LoadFromFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Valid hex but not in catalog.
	if got := cat.Lookup("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); got != "" {
		t.Errorf("Lookup(nonexistent) = %q, want empty", got)
	}
	// Invalid (not 40 hex chars) returns empty.
	if got := cat.Lookup("NONEXISTENT"); got != "" {
		t.Errorf("Lookup(invalid) = %q, want empty", got)
	}
}

func TestMalformedLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "KEYDB.cfg")

	validID := "ABCDEF1234567890ABCDEF1234567890ABCDEF12"
	content := "no-pipe\n" +
		"| only-title\n" +
		" |  |\n" +
		"SHORT | Bad ID\n" +
		validID + " | Good Title\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, _, err := LoadFromFile(path, nil)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}

	if got := cat.Size(); got != 1 {
		t.Errorf("Size = %d, want 1 (only valid line)", got)
	}
	if got := cat.Lookup(validID); got != "Good Title" {
		t.Errorf("Lookup(%s) = %q, want %q", validID, got, "Good Title")
	}
}

func TestCommentLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "KEYDB.cfg")

	validID := "ABCDEF1234567890ABCDEF1234567890ABCDEF12"
	content := "; comment one\n" +
		";comment two\n" +
		validID + " | Real Entry\n" +
		"; another comment\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, _, err := LoadFromFile(path, nil)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}

	if got := cat.Size(); got != 1 {
		t.Errorf("Size = %d, want 1", got)
	}
	if got := cat.Lookup(validID); got != "Real Entry" {
		t.Errorf("Lookup(%s) = %q, want %q", validID, got, "Real Entry")
	}
}

func TestLoadFromFileNotExist(t *testing.T) {
	_, _, err := LoadFromFile("/nonexistent/path/KEYDB.cfg", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestStaleness(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "KEYDB.cfg")

	validID := "ABCDEF1234567890ABCDEF1234567890ABCDEF12"
	content := validID + " | Some Movie\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Set mod time to 8 days ago.
	oldTime := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	_, stale, err := LoadFromFile(path, nil)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if !stale {
		t.Error("expected stale=true for file older than 7 days")
	}
}

func TestNormalizeDiscID(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantOK  bool
	}{
		{"ABCDEF1234567890ABCDEF1234567890ABCDEF12", "ABCDEF1234567890ABCDEF1234567890ABCDEF12", true},
		{"abcdef1234567890abcdef1234567890abcdef12", "ABCDEF1234567890ABCDEF1234567890ABCDEF12", true},
		{"0XABCDEF1234567890ABCDEF1234567890ABCDEF12", "ABCDEF1234567890ABCDEF1234567890ABCDEF12", true},
		{"0xabcdef1234567890abcdef1234567890abcdef12", "ABCDEF1234567890ABCDEF1234567890ABCDEF12", true},
		{"SHORT", "", false},
		{"ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ", "", false}, // invalid hex
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := normalizeDiscID(tt.input)
		if ok != tt.wantOK || got != tt.want {
			t.Errorf("normalizeDiscID(%q) = (%q, %v), want (%q, %v)", tt.input, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestTitleExtractionChain(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// extractAlias: bracketed content
		{"Foo [Bar]", "Bar"},
		{"Foo [Bar Baz]", "Bar Baz"},
		// stripAlias: strip from [
		// (extractAlias wins when brackets have content)
		// normalizeDuplicateTitle
		{"Movie (Movie)", "Movie"},
		{"The Film (The Film)", "The Film"},
		// No transformation needed
		{"Plain Title", "Plain Title"},
		// Empty bracket content falls through
		{"Title []", "Title"},
	}
	for _, tt := range tests {
		got := cleanTitle(tt.input)
		if got != tt.want {
			t.Errorf("cleanTitle(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractAlias(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Foo [Bar]", "Bar"},
		{"Foo [Bar Baz] extra", "Bar Baz"},
		{"No brackets", ""},
		{"Empty []", ""},
		{"[Only]", "Only"},
	}
	for _, tt := range tests {
		if got := extractAlias(tt.input); got != tt.want {
			t.Errorf("extractAlias(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStripAlias(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Foo [Bar]", "Foo"},
		{"No brackets", ""},
		{"[Start]", ""},
	}
	for _, tt := range tests {
		if got := stripAlias(tt.input); got != tt.want {
			t.Errorf("stripAlias(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeDuplicateTitle(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Movie (Movie)", "Movie"},
		{"The Film (The Film)", "The Film"},
		{"Different (Other)", ""},
		{"No parens", ""},
		{"Nested (Nested)", "Nested"},
	}
	for _, tt := range tests {
		if got := normalizeDuplicateTitle(tt.input); got != tt.want {
			t.Errorf("normalizeDuplicateTitle(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
