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

	if err := os.WriteFile(filepath.Join(srcDir, "title01.mkv"), []byte("video data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := store.Register("abc123", srcDir, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	meta := EntryMetadata{
		Fingerprint: "abc123",
		DiscTitle:   "Test Disc",
		CachedAt:    time.Now(),
		TitleCount:  1,
		TotalBytes:  10,
	}
	if err := store.WriteMetadata("abc123", meta); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
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

	if err := store.Register("fp001", srcDir, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	meta := EntryMetadata{
		Fingerprint: "fp001",
		DiscTitle:   "Round Trip Disc",
		CachedAt:    time.Now(),
		TitleCount:  1,
		TotalBytes:  int64(len(content)),
	}
	if err := store.WriteMetadata("fp001", meta); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	restored, err := store.Restore("fp001", destDir, nil)
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

	meta, err := store.Restore("nonexistent", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Fatal("expected nil metadata for cache miss")
	}
}

func TestRegisterProgressCallback(t *testing.T) {
	cacheDir := t.TempDir()
	srcDir := t.TempDir()
	store := New(cacheDir, 10)

	if err := os.WriteFile(filepath.Join(srcDir, "a.mkv"), []byte("aaaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "b.mkv"), []byte("bbbbbb"), 0o644); err != nil {
		t.Fatal(err)
	}

	var reports []CopyProgress
	progress := func(p CopyProgress) {
		reports = append(reports, p)
	}

	if err := store.Register("prog01", srcDir, progress); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if len(reports) == 0 {
		t.Fatal("expected progress callbacks, got none")
	}

	last := reports[len(reports)-1]
	if last.TotalBytes != 10 {
		t.Fatalf("TotalBytes: got %d, want 10", last.TotalBytes)
	}
	if last.BytesCopied != 10 {
		t.Fatalf("final BytesCopied: got %d, want 10", last.BytesCopied)
	}
}

func TestRestoreProgressCallback(t *testing.T) {
	cacheDir := t.TempDir()
	srcDir := t.TempDir()
	destDir := t.TempDir()
	store := New(cacheDir, 10)

	content := []byte("restore progress data")
	if err := os.WriteFile(filepath.Join(srcDir, "title.mkv"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := store.Register("prog02", srcDir, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	meta := EntryMetadata{
		Fingerprint: "prog02",
		DiscTitle:   "Restore Progress",
		CachedAt:    time.Now(),
		TitleCount:  1,
		TotalBytes:  int64(len(content)),
	}
	if err := store.WriteMetadata("prog02", meta); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	var reports []CopyProgress
	progress := func(p CopyProgress) {
		reports = append(reports, p)
	}

	restored, err := store.Restore("prog02", destDir, progress)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored == nil {
		t.Fatal("expected metadata, got nil")
	}

	if len(reports) == 0 {
		t.Fatal("expected progress callbacks, got none")
	}

	last := reports[len(reports)-1]
	if last.BytesCopied != int64(len(content)) {
		t.Fatalf("final BytesCopied: got %d, want %d", last.BytesCopied, len(content))
	}
}

func TestGetMetadata(t *testing.T) {
	cacheDir := t.TempDir()
	store := New(cacheDir, 10)

	now := time.Now().Truncate(time.Second)
	meta := EntryMetadata{
		Fingerprint: "meta01",
		DiscTitle:   "Metadata Test",
		CachedAt:    now,
		TitleCount:  3,
		TotalBytes:  4,
	}

	if err := store.WriteMetadata("meta01", meta); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
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

func TestMetadataFilenameIsSpindleCacheJSON(t *testing.T) {
	cacheDir := t.TempDir()
	store := New(cacheDir, 10)

	meta := EntryMetadata{DiscTitle: "filename test"}
	if err := store.WriteMetadata("fp01", meta); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	// Verify the file is named spindle.cache.json, not metadata.json.
	expected := filepath.Join(cacheDir, "fp01", "spindle.cache.json")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected %s to exist: %v", expected, err)
	}

	old := filepath.Join(cacheDir, "fp01", "metadata.json")
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("metadata.json should not exist, got err=%v", err)
	}
}

func TestWriteMetadataIsAtomic(t *testing.T) {
	cacheDir := t.TempDir()
	store := New(cacheDir, 10)

	meta := EntryMetadata{DiscTitle: "atomic test"}
	if err := store.WriteMetadata("fp01", meta); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	// No temp files should be left behind.
	entries, err := os.ReadDir(filepath.Join(cacheDir, "fp01"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != metadataFileName {
			t.Errorf("unexpected file in cache entry dir: %s", e.Name())
		}
	}
}

func TestRestoreSkipsMetadataSidecar(t *testing.T) {
	cacheDir := t.TempDir()
	srcDir := t.TempDir()
	destDir := t.TempDir()
	store := New(cacheDir, 10)

	content := []byte("video data")
	if err := os.WriteFile(filepath.Join(srcDir, "title.mkv"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := store.Register("fp01", srcDir, nil); err != nil {
		t.Fatal(err)
	}
	meta := EntryMetadata{TotalBytes: int64(len(content))}
	if err := store.WriteMetadata("fp01", meta); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Restore("fp01", destDir, nil); err != nil {
		t.Fatal(err)
	}

	// Metadata sidecar should NOT be copied to destDir.
	if _, err := os.Stat(filepath.Join(destDir, metadataFileName)); !os.IsNotExist(err) {
		t.Error("metadata sidecar should not be copied to restore destination")
	}
	// But the MKV should be there.
	if _, err := os.Stat(filepath.Join(destDir, "title.mkv")); err != nil {
		t.Errorf("restored file missing: %v", err)
	}
}
