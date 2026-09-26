package discidcache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListRemoveClearPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	store, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"b", "a"} {
		if err := store.Set(id, Entry{TMDBID: 42, Title: id}); err != nil {
			t.Fatal(err)
		}
	}
	if store.Size() != 2 {
		t.Fatalf("size: %d", store.Size())
	}
	entries := store.List()
	if len(entries) != 2 || entries[0].DiscID == entries[1].DiscID {
		t.Fatalf("list: %+v", entries)
	}
	if err := store.Remove("absent"); err == nil {
		t.Fatal("removed missing entry")
	}
	if err := store.Remove("a"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Size() != 1 || reopened.Lookup("a") != nil || reopened.Lookup("b") == nil {
		t.Fatalf("remove not persisted: %+v", reopened.List())
	}
	if err := reopened.Clear(); err != nil {
		t.Fatal(err)
	}
	reopened, err = Open(path, nil)
	if err != nil || reopened.Size() != 0 {
		t.Fatalf("clear not persisted: %v %v", reopened, err)
	}
}

func TestOpenInvalidCacheAndPersistenceFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, nil); err == nil {
		t.Fatal("accepted corrupt cache")
	}
	if _, err := Open(dir, nil); err == nil {
		t.Fatal("accepted directory as cache")
	}
	file := filepath.Join(dir, "parent")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(file, "child.json"), nil); err == nil {
		t.Fatal("initialized cache under file")
	}
}
