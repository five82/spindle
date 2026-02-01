package discidcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheStoreAndLookup(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test_cache.json")

	cache := NewCache(cachePath, nil)

	entry := Entry{
		DiscID:    "ABC123DEF456",
		TMDBID:    27205,
		MediaType: "movie",
		Title:     "Inception",
		Year:      "2010",
		CachedAt:  time.Now(),
	}

	if err := cache.Store(entry); err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	found, ok := cache.Lookup("ABC123DEF456")
	if !ok {
		t.Fatal("Lookup failed to find stored entry")
	}

	if found.TMDBID != entry.TMDBID {
		t.Errorf("TMDBID mismatch: got %d, want %d", found.TMDBID, entry.TMDBID)
	}
	if found.Title != entry.Title {
		t.Errorf("Title mismatch: got %q, want %q", found.Title, entry.Title)
	}
	if found.MediaType != entry.MediaType {
		t.Errorf("MediaType mismatch: got %q, want %q", found.MediaType, entry.MediaType)
	}
}

func TestCacheLookupNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test_cache.json")

	cache := NewCache(cachePath, nil)

	_, ok := cache.Lookup("NONEXISTENT")
	if ok {
		t.Error("Lookup should return false for non-existent entry")
	}
}

func TestCacheLookupEmptyDiscID(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test_cache.json")

	cache := NewCache(cachePath, nil)

	_, ok := cache.Lookup("")
	if ok {
		t.Error("Lookup should return false for empty disc ID")
	}

	_, ok = cache.Lookup("   ")
	if ok {
		t.Error("Lookup should return false for whitespace disc ID")
	}
}

func TestCacheRemove(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test_cache.json")

	cache := NewCache(cachePath, nil)

	entry := Entry{
		DiscID:    "REMOVE_TEST",
		TMDBID:    12345,
		MediaType: "movie",
		Title:     "Test Movie",
		CachedAt:  time.Now(),
	}

	if err := cache.Store(entry); err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	if err := cache.Remove("REMOVE_TEST"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	_, ok := cache.Lookup("REMOVE_TEST")
	if ok {
		t.Error("Entry should not exist after removal")
	}
}

func TestCacheRemoveNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test_cache.json")

	cache := NewCache(cachePath, nil)

	err := cache.Remove("NONEXISTENT")
	if err == nil {
		t.Error("Remove should return error for non-existent entry")
	}
}

func TestCacheList(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test_cache.json")

	cache := NewCache(cachePath, nil)

	// Store entries with different timestamps
	entries := []Entry{
		{DiscID: "OLDEST", TMDBID: 1, MediaType: "movie", Title: "Oldest", CachedAt: time.Now().Add(-2 * time.Hour)},
		{DiscID: "NEWEST", TMDBID: 2, MediaType: "movie", Title: "Newest", CachedAt: time.Now()},
		{DiscID: "MIDDLE", TMDBID: 3, MediaType: "tv", Title: "Middle", SeasonNumber: 1, CachedAt: time.Now().Add(-1 * time.Hour)},
	}

	for _, e := range entries {
		if err := cache.Store(e); err != nil {
			t.Fatalf("Store failed: %v", err)
		}
	}

	list := cache.List()
	if len(list) != 3 {
		t.Fatalf("List should return 3 entries, got %d", len(list))
	}

	// Should be sorted by CachedAt descending
	if list[0].DiscID != "NEWEST" {
		t.Errorf("First entry should be NEWEST, got %s", list[0].DiscID)
	}
	if list[1].DiscID != "MIDDLE" {
		t.Errorf("Second entry should be MIDDLE, got %s", list[1].DiscID)
	}
	if list[2].DiscID != "OLDEST" {
		t.Errorf("Third entry should be OLDEST, got %s", list[2].DiscID)
	}
}

func TestCacheClear(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test_cache.json")

	cache := NewCache(cachePath, nil)

	// Store some entries
	for i := range 3 {
		entry := Entry{
			DiscID:    string(rune('A' + i)),
			TMDBID:    int64(i + 1),
			MediaType: "movie",
			Title:     "Test",
			CachedAt:  time.Now(),
		}
		if err := cache.Store(entry); err != nil {
			t.Fatalf("Store failed: %v", err)
		}
	}

	if cache.Count() != 3 {
		t.Fatalf("Expected 3 entries before clear, got %d", cache.Count())
	}

	if err := cache.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	if cache.Count() != 0 {
		t.Errorf("Expected 0 entries after clear, got %d", cache.Count())
	}

	list := cache.List()
	if len(list) != 0 {
		t.Errorf("List should be empty after clear, got %d entries", len(list))
	}
}

func TestCachePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "persist_test.json")

	// Create cache and store entry
	cache1 := NewCache(cachePath, nil)
	entry := Entry{
		DiscID:       "PERSIST_TEST",
		TMDBID:       999,
		MediaType:    "tv",
		Title:        "Breaking Bad",
		SeasonNumber: 1,
		Year:         "2008",
		CachedAt:     time.Now(),
	}

	if err := cache1.Store(entry); err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// Create new cache instance and verify data persisted
	cache2 := NewCache(cachePath, nil)
	found, ok := cache2.Lookup("PERSIST_TEST")
	if !ok {
		t.Fatal("Entry should persist across cache instances")
	}

	if found.TMDBID != entry.TMDBID {
		t.Errorf("TMDBID mismatch: got %d, want %d", found.TMDBID, entry.TMDBID)
	}
	if found.SeasonNumber != entry.SeasonNumber {
		t.Errorf("SeasonNumber mismatch: got %d, want %d", found.SeasonNumber, entry.SeasonNumber)
	}
}

func TestCacheEmptyPath(t *testing.T) {
	cache := NewCache("", nil)

	// All operations should be no-ops
	entry := Entry{DiscID: "TEST", TMDBID: 1, MediaType: "movie", Title: "Test"}

	// Store should succeed but not persist
	if err := cache.Store(entry); err != nil {
		t.Errorf("Store with empty path should not error: %v", err)
	}

	// Lookup should always return false
	_, ok := cache.Lookup("TEST")
	if ok {
		t.Error("Lookup with empty path should always return false")
	}

	// Count should be 0
	if cache.Count() != 0 {
		t.Errorf("Count with empty path should be 0, got %d", cache.Count())
	}

	// List should be nil
	if cache.List() != nil {
		t.Error("List with empty path should return nil")
	}

	// Clear and Remove should succeed
	if err := cache.Clear(); err != nil {
		t.Errorf("Clear with empty path should not error: %v", err)
	}
	if err := cache.Remove("TEST"); err != nil {
		t.Errorf("Remove with empty path should not error: %v", err)
	}
}

func TestCacheStoreEmptyDiscID(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test_cache.json")

	cache := NewCache(cachePath, nil)

	entry := Entry{
		DiscID:    "",
		TMDBID:    123,
		MediaType: "movie",
		Title:     "Test",
	}

	err := cache.Store(entry)
	if err == nil {
		t.Error("Store should fail for empty disc ID")
	}
}

func TestCacheRemoveEmptyDiscID(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test_cache.json")

	cache := NewCache(cachePath, nil)

	err := cache.Remove("")
	if err == nil {
		t.Error("Remove should fail for empty disc ID")
	}
}

func TestCacheUpdatesExistingEntry(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test_cache.json")

	cache := NewCache(cachePath, nil)

	// Store initial entry
	entry1 := Entry{
		DiscID:    "UPDATE_TEST",
		TMDBID:    100,
		MediaType: "movie",
		Title:     "Original Title",
		CachedAt:  time.Now().Add(-1 * time.Hour),
	}
	if err := cache.Store(entry1); err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// Update with new entry (same disc ID)
	entry2 := Entry{
		DiscID:    "UPDATE_TEST",
		TMDBID:    200,
		MediaType: "movie",
		Title:     "Updated Title",
		CachedAt:  time.Now(),
	}
	if err := cache.Store(entry2); err != nil {
		t.Fatalf("Store update failed: %v", err)
	}

	// Should have only one entry
	if cache.Count() != 1 {
		t.Errorf("Expected 1 entry after update, got %d", cache.Count())
	}

	// Should have the updated values
	found, ok := cache.Lookup("UPDATE_TEST")
	if !ok {
		t.Fatal("Entry not found after update")
	}
	if found.TMDBID != 200 {
		t.Errorf("TMDBID should be updated to 200, got %d", found.TMDBID)
	}
	if found.Title != "Updated Title" {
		t.Errorf("Title should be updated, got %q", found.Title)
	}
}

func TestCacheCorruptedFile(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "corrupt_cache.json")

	// Write invalid JSON
	if err := os.WriteFile(cachePath, []byte("not valid json"), 0o644); err != nil {
		t.Fatalf("Failed to write corrupt file: %v", err)
	}

	// NewCache should handle corrupt file gracefully (log warning, start empty)
	cache := NewCache(cachePath, nil)

	// Cache should be functional despite corrupt file
	entry := Entry{DiscID: "TEST", TMDBID: 1, MediaType: "movie", Title: "Test", CachedAt: time.Now()}
	if err := cache.Store(entry); err != nil {
		t.Errorf("Store should work after corrupt file: %v", err)
	}

	found, ok := cache.Lookup("TEST")
	if !ok {
		t.Error("Lookup should work after recovering from corrupt file")
	}
	if found.TMDBID != 1 {
		t.Errorf("TMDBID mismatch: got %d, want 1", found.TMDBID)
	}
}

func TestCacheTVShowWithEdition(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test_cache.json")

	cache := NewCache(cachePath, nil)

	// TV show entry (edition should be empty, season_number should be set)
	tvEntry := Entry{
		DiscID:       "TV_DISC_123",
		TMDBID:       1396,
		MediaType:    "tv",
		Title:        "Breaking Bad",
		SeasonNumber: 2,
		Year:         "2008",
		CachedAt:     time.Now(),
	}

	if err := cache.Store(tvEntry); err != nil {
		t.Fatalf("Store TV entry failed: %v", err)
	}

	found, ok := cache.Lookup("TV_DISC_123")
	if !ok {
		t.Fatal("TV entry not found")
	}

	if found.SeasonNumber != 2 {
		t.Errorf("SeasonNumber mismatch: got %d, want 2", found.SeasonNumber)
	}
	if found.MediaType != "tv" {
		t.Errorf("MediaType should be 'tv', got %q", found.MediaType)
	}
}

func TestCacheMovieWithEdition(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test_cache.json")

	cache := NewCache(cachePath, nil)

	// Movie entry with edition
	movieEntry := Entry{
		DiscID:    "MOVIE_DISC_456",
		TMDBID:    152,
		MediaType: "movie",
		Title:     "Star Trek: The Motion Picture",
		Edition:   "Director's Cut",
		Year:      "1979",
		CachedAt:  time.Now(),
	}

	if err := cache.Store(movieEntry); err != nil {
		t.Fatalf("Store movie entry failed: %v", err)
	}

	found, ok := cache.Lookup("MOVIE_DISC_456")
	if !ok {
		t.Fatal("Movie entry not found")
	}

	if found.Edition != "Director's Cut" {
		t.Errorf("Edition mismatch: got %q, want %q", found.Edition, "Director's Cut")
	}
	if found.MediaType != "movie" {
		t.Errorf("MediaType should be 'movie', got %q", found.MediaType)
	}
}
