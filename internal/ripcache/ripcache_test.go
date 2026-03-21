package ripcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHasCacheEmptyStore(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, 10)

	if store.HasCache("nonexistent") {
		t.Fatal("expected HasCache to return false for empty store")
	}
}

func TestRegisterAndHasCache(t *testing.T) {
	cacheDir := t.TempDir()
	srcDir := t.TempDir()
	store := New(cacheDir, 10)

	// Create a source file to register.
	if err := os.WriteFile(filepath.Join(srcDir, "title01.mkv"), []byte("video data"), 0o644); err != nil {
		t.Fatal(err)
	}

	meta := EntryMetadata{
		Fingerprint: "abc123",
		DiscTitle:   "Test Disc",
		CachedAt:    time.Now(),
		TitleCount:  1,
		TotalBytes:  10,
	}

	if err := store.Register("abc123", srcDir, meta); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if !store.HasCache("abc123") {
		t.Fatal("expected HasCache to return true after Register")
	}
}

func TestRegisterAndRestoreRoundTrip(t *testing.T) {
	cacheDir := t.TempDir()
	srcDir := t.TempDir()
	destDir := t.TempDir()
	store := New(cacheDir, 10)

	content := []byte("ripped video content")
	if err := os.WriteFile(filepath.Join(srcDir, "title01.mkv"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	meta := EntryMetadata{
		Fingerprint: "fp001",
		DiscTitle:   "Round Trip Disc",
		CachedAt:    time.Now(),
		TitleCount:  1,
		TotalBytes:  int64(len(content)),
	}

	if err := store.Register("fp001", srcDir, meta); err != nil {
		t.Fatalf("Register: %v", err)
	}

	restored, err := store.Restore("fp001", destDir)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored == nil {
		t.Fatal("expected metadata, got nil")
	}
	if restored.DiscTitle != "Round Trip Disc" {
		t.Fatalf("DiscTitle: got %q, want %q", restored.DiscTitle, "Round Trip Disc")
	}

	got, err := os.ReadFile(filepath.Join(destDir, "title01.mkv"))
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("content mismatch: got %q, want %q", got, content)
	}
}

func TestRestoreMissReturnsNil(t *testing.T) {
	cacheDir := t.TempDir()
	store := New(cacheDir, 10)

	meta, err := store.Restore("nonexistent", t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Fatal("expected nil metadata for cache miss")
	}
}

func TestGetMetadata(t *testing.T) {
	cacheDir := t.TempDir()
	srcDir := t.TempDir()
	store := New(cacheDir, 10)

	if err := os.WriteFile(filepath.Join(srcDir, "title.mkv"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	meta := EntryMetadata{
		Fingerprint: "meta01",
		DiscTitle:   "Metadata Test",
		CachedAt:    now,
		TitleCount:  3,
		TotalBytes:  4,
	}

	if err := store.Register("meta01", srcDir, meta); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := store.GetMetadata("meta01")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if got.Fingerprint != "meta01" {
		t.Fatalf("Fingerprint: got %q, want %q", got.Fingerprint, "meta01")
	}
	if got.TitleCount != 3 {
		t.Fatalf("TitleCount: got %d, want 3", got.TitleCount)
	}
	if !got.CachedAt.Equal(now) {
		t.Fatalf("CachedAt: got %v, want %v", got.CachedAt, now)
	}
}
