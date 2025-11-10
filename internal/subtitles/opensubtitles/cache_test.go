package opensubtitles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheStoreAndLoad(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewCache(dir, nil)
	if err != nil {
		t.Fatalf("NewCache returned error: %v", err)
	}
	entry := CacheEntry{FileID: 42, Language: "en", FileName: "show.en.srt"}
	path, err := cache.Store(entry, []byte("hello"))
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected cached data file to exist: %v", err)
	}
	result, ok, err := cache.Load(42)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !ok {
		t.Fatalf("expected cache hit")
	}
	if string(result.Data) != "hello" {
		t.Fatalf("unexpected cache data: %q", string(result.Data))
	}
	if result.Entry.Language != "en" {
		t.Fatalf("unexpected language: %s", result.Entry.Language)
	}
	if result.Path != filepath.Join(dir, "42.srt") {
		t.Fatalf("unexpected path: %s", result.Path)
	}
}

func TestCacheLoadMiss(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewCache(dir, nil)
	if err != nil {
		t.Fatalf("NewCache returned error: %v", err)
	}
	if _, ok, err := cache.Load(99); err != nil {
		t.Fatalf("Load returned error: %v", err)
	} else if ok {
		t.Fatalf("expected cache miss")
	}
}
