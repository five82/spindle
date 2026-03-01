package api

import (
	"path/filepath"
	"testing"
	"time"

	"spindle/internal/discidcache"
	"spindle/internal/logging"
)

func TestRemoveDiscIDEntryByNumber(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "discid_cache.json")
	cache := discidcache.NewCache(cachePath, logging.NewNop())

	entryA := discidcache.Entry{
		DiscID:    "disc-a",
		TMDBID:    100,
		MediaType: "movie",
		Title:     "A",
		CachedAt:  time.Now().Add(-time.Hour),
	}
	entryB := discidcache.Entry{
		DiscID:    "disc-b",
		TMDBID:    200,
		MediaType: "movie",
		Title:     "B",
		CachedAt:  time.Now(),
	}

	if err := cache.Store(entryA); err != nil {
		t.Fatalf("store entryA: %v", err)
	}
	if err := cache.Store(entryB); err != nil {
		t.Fatalf("store entryB: %v", err)
	}

	removed, err := RemoveDiscIDEntryByNumber(cache, 1)
	if err != nil {
		t.Fatalf("RemoveDiscIDEntryByNumber: %v", err)
	}
	if removed.DiscID != "disc-b" {
		t.Fatalf("removed disc ID = %q, want disc-b", removed.DiscID)
	}
	if count := cache.Count(); count != 1 {
		t.Fatalf("cache count = %d, want 1", count)
	}
}
