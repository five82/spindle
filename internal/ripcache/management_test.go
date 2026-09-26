package ripcache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListRemoveClearCacheEntries(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, 1)
	entries, err := store.List()
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty list: %v %v", entries, err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	registerEntry(t, store, "older", 4, now.Add(-time.Hour))
	registerEntry(t, store, "newer", 6, now)
	if err := os.Mkdir(filepath.Join(dir, "incomplete"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "not-an-entry"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err = store.List()
	if err != nil || len(entries) != 2 || entries[0].Fingerprint != "newer" || entries[1].Fingerprint != "older" {
		t.Fatalf("newest-first list: %v %v", entries, err)
	}
	if err := store.Remove("missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing remove: %v", err)
	}
	if err := store.Remove("older"); err != nil || store.HasCache("older") {
		t.Fatalf("remove older: %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"newer", "incomplete"} {
		if store.HasCache(name) {
			t.Errorf("entry %s survived clear", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "not-an-entry")); err != nil {
		t.Fatalf("clear removed non-entry: %v", err)
	}
	missing := New(filepath.Join(dir, "absent"), 1)
	if entries, err := missing.List(); err != nil || len(entries) != 0 {
		t.Fatalf("missing list: %v %v", entries, err)
	}
	if err := missing.Clear(); err != nil {
		t.Fatalf("missing clear: %v", err)
	}
}

func TestCacheMetadataAndRestoreFailurePaths(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, 1)
	if _, err := store.GetMetadata("absent"); err == nil || !strings.Contains(err.Error(), "read metadata") {
		t.Fatalf("missing metadata: %v", err)
	}
	entry := filepath.Join(dir, "broken")
	if err := os.Mkdir(entry, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, metadataFileName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetMetadata("broken"); err == nil || !strings.Contains(err.Error(), "parse metadata") {
		t.Fatalf("bad metadata: %v", err)
	}
	if _, err := store.Restore("broken", t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "read metadata") {
		t.Fatalf("restore bad metadata: %v", err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "title.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Register("valid", src, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata("valid", EntryMetadata{TotalBytes: 5}); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(dest, []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Restore("valid", dest, nil); err == nil || !strings.Contains(err.Error(), "create dest dir") {
		t.Fatalf("restore invalid destination: %v", err)
	}
	if err := store.Register("missing-source", filepath.Join(dir, "absent"), nil); err == nil || !strings.Contains(err.Error(), "read source dir") {
		t.Fatalf("register missing source: %v", err)
	}
	broken := t.TempDir()
	if err := os.Symlink(filepath.Join(broken, "absent"), filepath.Join(broken, "broken.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := store.Register("bad-entry", broken, nil); err == nil || !strings.Contains(err.Error(), "copy broken.mkv") {
		t.Fatalf("copy failure: %v", err)
	}
}
