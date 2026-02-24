package ripping

import (
	"os"
	"path/filepath"
	"testing"

	"spindle/internal/ripspec"
)

func TestCacheHasAllEpisodeFiles_Complete(t *testing.T) {
	dir := t.TempDir()
	// Create MKV files for title IDs 0, 1, 2
	for _, name := range []string{"disc_t00.mkv", "disc_t01.mkv", "disc_t02.mkv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s01e01", TitleID: 0},
			{Key: "s01e02", TitleID: 1},
			{Key: "s01e03", TitleID: 2},
		},
	}
	missing := cacheHasAllEpisodeFiles(env, dir)
	if len(missing) != 0 {
		t.Fatalf("expected no missing episodes, got %v", missing)
	}
}

func TestCacheHasAllEpisodeFiles_Missing(t *testing.T) {
	dir := t.TempDir()
	// Only create files for title IDs 0 and 2 (skip 1 and 3)
	for _, name := range []string{"disc_t00.mkv", "disc_t02.mkv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s01e01", TitleID: 0},
			{Key: "s01e02", TitleID: 1},
			{Key: "s01e03", TitleID: 2},
			{Key: "s01e04", TitleID: 3},
		},
	}
	missing := cacheHasAllEpisodeFiles(env, dir)
	if len(missing) != 2 {
		t.Fatalf("expected 2 missing episodes, got %d: %v", len(missing), missing)
	}
	if missing[0] != "s01e02" || missing[1] != "s01e04" {
		t.Fatalf("expected [s01e02 s01e04], got %v", missing)
	}
}

func TestCacheHasAllEpisodeFiles_NegativeTitleID(t *testing.T) {
	dir := t.TempDir()
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s01e01", TitleID: -1},
		},
	}
	missing := cacheHasAllEpisodeFiles(env, dir)
	if len(missing) != 1 || missing[0] != "s01e01" {
		t.Fatalf("expected [s01e01] for negative TitleID, got %v", missing)
	}
}

func TestCacheHasAllEpisodeFiles_NilEnvelope(t *testing.T) {
	missing := cacheHasAllEpisodeFiles(nil, t.TempDir())
	if missing != nil {
		t.Fatalf("expected nil for nil envelope, got %v", missing)
	}
}

func TestCacheHasAllEpisodeFiles_NoEpisodes(t *testing.T) {
	env := &ripspec.Envelope{}
	missing := cacheHasAllEpisodeFiles(env, t.TempDir())
	if missing != nil {
		t.Fatalf("expected nil for empty episodes, got %v", missing)
	}
}

func TestCacheHasAllEpisodeFiles_UnreadableDir(t *testing.T) {
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s01e01", TitleID: 0},
			{Key: "s01e02", TitleID: 1},
		},
	}
	missing := cacheHasAllEpisodeFiles(env, "/nonexistent/path")
	if len(missing) != 2 {
		t.Fatalf("expected all episodes missing for unreadable dir, got %v", missing)
	}
}

func TestCacheHasAllEpisodeFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s01e01", TitleID: 0},
		},
	}
	missing := cacheHasAllEpisodeFiles(env, dir)
	if len(missing) != 1 || missing[0] != "s01e01" {
		t.Fatalf("expected [s01e01] for empty dir, got %v", missing)
	}
}
