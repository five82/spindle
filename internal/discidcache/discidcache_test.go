package discidcache

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestOpenCreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestLookupMiss(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	entry := store.Lookup("nonexistent")
	if entry != nil {
		t.Fatal("expected nil for missing fingerprint")
	}
}

func TestSetAndLookup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	want := Entry{
		TMDBID:    12345,
		MediaType: "movie",
		Title:     "Test Movie",
		Year:      "2024",
	}

	if err := store.Set("fp001", want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got := store.Lookup("fp001")
	if got == nil {
		t.Fatal("expected entry, got nil")
	}
	if got.TMDBID != want.TMDBID {
		t.Fatalf("TMDBID: got %d, want %d", got.TMDBID, want.TMDBID)
	}
	if got.Title != want.Title {
		t.Fatalf("Title: got %q, want %q", got.Title, want.Title)
	}
	if got.Year != want.Year {
		t.Fatalf("Year: got %q, want %q", got.Year, want.Year)
	}
}

func TestPersistenceAcrossOpenCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	store1, err := Open(path)
	if err != nil {
		t.Fatalf("Open (1): %v", err)
	}

	entry := Entry{
		TMDBID:    67890,
		MediaType: "tv",
		Title:     "Test Show",
		Season:    2,
	}

	if err := store1.Set("fp002", entry); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Reopen from disk.
	store2, err := Open(path)
	if err != nil {
		t.Fatalf("Open (2): %v", err)
	}

	got := store2.Lookup("fp002")
	if got == nil {
		t.Fatal("expected entry after reopen, got nil")
	}
	if got.TMDBID != 67890 {
		t.Fatalf("TMDBID: got %d, want 67890", got.TMDBID)
	}
	if got.Season != 2 {
		t.Fatalf("Season: got %d, want 2", got.Season)
	}
}

func TestConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var wg sync.WaitGroup
	const workers = 10

	for i := range workers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			entry := Entry{
				TMDBID:    id,
				MediaType: "movie",
				Title:     "Concurrent Movie",
			}
			if err := store.Set("fp_concurrent", entry); err != nil {
				t.Errorf("Set from goroutine %d: %v", id, err)
			}
			_ = store.Lookup("fp_concurrent")
		}(i)
	}

	wg.Wait()

	got := store.Lookup("fp_concurrent")
	if got == nil {
		t.Fatal("expected entry after concurrent writes, got nil")
	}
}
