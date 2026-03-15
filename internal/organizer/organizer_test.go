package organizer

import (
	"testing"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

func TestAssetKeys_Movie(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
	}

	keys := env.AssetKeys()
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if keys[0] != "main" {
		t.Errorf("expected key 'main', got %q", keys[0])
	}
}

func TestAssetKeys_TV(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01e01"},
			{Key: "s01e02"},
			{Key: "s01e03"},
		},
	}

	keys := env.AssetKeys()
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	expected := []string{"s01e01", "s01e02", "s01e03"}
	for i, want := range expected {
		if keys[i] != want {
			t.Errorf("key[%d]: expected %q, got %q", i, want, keys[i])
		}
	}
}

func TestAssetKeys_TVNoEpisodes(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
	}

	keys := env.AssetKeys()
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(keys))
	}
}

func TestDestFilename_Movie(t *testing.T) {
	meta := &queue.Metadata{
		Title:     "The Matrix",
		MediaType: "movie",
		Year:      "1999",
		Movie:     true,
	}

	got := destFilename(meta, "main", ".mkv")
	want := "The Matrix (1999).mkv"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestDestFilename_MovieWithEdition(t *testing.T) {
	meta := &queue.Metadata{
		Title:     "Blade Runner",
		MediaType: "movie",
		Year:      "1982",
		Movie:     true,
		Edition:   "Final Cut",
	}

	got := destFilename(meta, "main", ".mkv")
	want := "Blade Runner (1982) - Final Cut.mkv"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestDestFilename_TVEpisode(t *testing.T) {
	meta := &queue.Metadata{
		Title:        "Breaking Bad",
		ShowTitle:    "Breaking Bad",
		MediaType:    "tv",
		SeasonNumber: 1,
	}

	got := destFilename(meta, "s01e03", ".mkv")
	want := "Breaking Bad - S01E03.mkv"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestDestFilename_TVFallback(t *testing.T) {
	meta := &queue.Metadata{
		Title:        "Some Show",
		ShowTitle:    "Some Show",
		MediaType:    "tv",
		SeasonNumber: 1,
	}

	// Non-standard key that does not parse as sNNeNN.
	got := destFilename(meta, "s01_001", ".mkv")
	want := "Some Show - s01_001.mkv"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestParseEpisodeKey(t *testing.T) {
	tests := []struct {
		key            string
		wantSeason     int
		wantEpisode    int
	}{
		{"s01e03", 1, 3},
		{"S02E10", 2, 10},
		{"s01_001", 0, 0},
		{"main", 0, 0},
		{"", 0, 0},
	}
	for _, tt := range tests {
		s, e := parseEpisodeKey(tt.key)
		if s != tt.wantSeason || e != tt.wantEpisode {
			t.Errorf("parseEpisodeKey(%q) = (%d, %d), want (%d, %d)",
				tt.key, s, e, tt.wantSeason, tt.wantEpisode)
		}
	}
}
