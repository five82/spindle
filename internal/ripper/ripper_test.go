package ripper

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func TestMapRippedAssets_Movie(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "title00.mkv"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
	}

	h := &Handler{}
	h.mapRippedAssets(env, dir)

	if len(env.Assets.Ripped) != 1 {
		t.Fatalf("expected 1 ripped asset, got %d", len(env.Assets.Ripped))
	}
	asset := env.Assets.Ripped[0]
	if asset.EpisodeKey != "main" {
		t.Errorf("expected episode key 'main', got %q", asset.EpisodeKey)
	}
	if asset.Status != "completed" {
		t.Errorf("expected status 'completed', got %q", asset.Status)
	}
	if filepath.Base(asset.Path) != "title00.mkv" {
		t.Errorf("expected path ending in title00.mkv, got %q", asset.Path)
	}
}

func TestMapRippedAssets_TVEpisodes(t *testing.T) {
	dir := t.TempDir()
	files := []string{"title00.mkv", "title01.mkv", "title02.mkv"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01e01"},
			{Key: "s01e02"},
			{Key: "s01e03"},
		},
	}

	h := &Handler{}
	h.mapRippedAssets(env, dir)

	if len(env.Assets.Ripped) != 3 {
		t.Fatalf("expected 3 ripped assets, got %d", len(env.Assets.Ripped))
	}

	expectedKeys := []string{"s01e01", "s01e02", "s01e03"}
	for i, want := range expectedKeys {
		got := env.Assets.Ripped[i].EpisodeKey
		if got != want {
			t.Errorf("asset[%d]: expected episode key %q, got %q", i, want, got)
		}
	}
}

func TestMapRippedAssets_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
	}

	h := &Handler{}
	h.mapRippedAssets(env, dir)

	if len(env.Assets.Ripped) != 0 {
		t.Fatalf("expected 0 ripped assets, got %d", len(env.Assets.Ripped))
	}
}
