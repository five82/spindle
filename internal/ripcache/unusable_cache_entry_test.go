package ripcache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCacheFailurePathsDoNotCreateUsableEntries(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	blocked := New(blocker, 1)
	if err := blocked.Register("fp", root, nil); err == nil || !strings.Contains(err.Error(), "create cache entry dir") {
		t.Fatalf("blocked register: %v", err)
	}
	if err := blocked.WriteMetadata("fp", EntryMetadata{}); err == nil || !strings.Contains(err.Error(), "ensure cache entry dir") {
		t.Fatalf("blocked metadata: %v", err)
	}
	if err := blocked.Prune(); err == nil || !strings.Contains(err.Error(), "read cache dir") {
		t.Fatalf("blocked prune: %v", err)
	}
	if _, err := blocked.List(); err == nil || !strings.Contains(err.Error(), "read cache dir") {
		t.Fatalf("blocked list: %v", err)
	}
	if err := blocked.Clear(); err == nil || !strings.Contains(err.Error(), "read cache dir") {
		t.Fatalf("blocked clear: %v", err)
	}
	store := New(filepath.Join(root, "cache"), 1)
	if err := store.Register("fp", filepath.Join(root, "missing"), nil); err == nil || !strings.Contains(err.Error(), "read source dir") {
		t.Fatalf("missing source: %v", err)
	}
	if store.HasCache("not-cached") {
		t.Fatal("missing entry recognized")
	}
	if meta, err := store.Restore("not-cached", filepath.Join(root, "dest"), nil); meta != nil || err != nil {
		t.Fatalf("missing restore: %+v %v", meta, err)
	}
	if meta, err := store.Restore("fp", filepath.Join(root, "dest"), nil); meta != nil || err == nil || !strings.Contains(err.Error(), "read metadata") {
		t.Fatalf("entry without metadata: %+v %v", meta, err)
	}
	if err := os.Mkdir(filepath.Join(root, "cache", "fp", metadataFileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata("fp", EntryMetadata{Fingerprint: "fp"}); err == nil || !strings.Contains(err.Error(), "rename metadata") {
		t.Fatalf("blocked metadata rename: %v", err)
	}
	if _, err := copyFileWithProgress(filepath.Join(root, "missing"), filepath.Join(root, "dest"), 0, 0, nil); err == nil || !strings.Contains(err.Error(), "stat source") {
		t.Fatalf("missing source copy: %v", err)
	}
	bad := filepath.Join(root, "cache", "invalid")
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, metadataFileName), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetMetadata("invalid"); err == nil || !strings.Contains(err.Error(), "parse metadata") {
		t.Fatalf("invalid metadata: %v", err)
	}
	if entries, err := store.List(); err != nil || len(entries) != 0 {
		t.Fatalf("invalid entries should be skipped: %+v %v", entries, err)
	}
}
