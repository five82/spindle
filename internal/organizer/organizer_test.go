package organizer

import (
	"math"
	"os"
	"path/filepath"
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
		key         string
		wantSeason  int
		wantEpisode int
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

func TestOverallBytePercent(t *testing.T) {
	if got := overallBytePercent(50, 200); math.Abs(got-25) > 1e-9 {
		t.Fatalf("overallBytePercent() = %f, want 25", got)
	}
	if got := overallBytePercent(250, 200); math.Abs(got-100) > 1e-9 {
		t.Fatalf("overallBytePercent clamp = %f, want 100", got)
	}
	if got := overallBytePercent(50, 0); got != 0 {
		t.Fatalf("overallBytePercent zero total = %f, want 0", got)
	}
}

func TestTotalCompletedStageBytes(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "one.mkv")
	file2 := filepath.Join(dir, "two.mkv")
	if err := os.WriteFile(file1, []byte("1234"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("123456"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &ripspec.Envelope{Assets: ripspec.Assets{Encoded: []ripspec.Asset{
		{EpisodeKey: "s01e01", Path: file1, Status: "completed"},
		{EpisodeKey: "s01e02", Path: file2, Status: "completed"},
		{EpisodeKey: "s01e03", Path: filepath.Join(dir, "missing.mkv"), Status: "completed"},
		{EpisodeKey: "s01e04", Path: "", Status: "failed"},
	}}}
	got := totalCompletedStageBytes(env, "encoded", []string{"s01e01", "s01e02", "s01e03", "s01e04"})
	if got != 10 {
		t.Fatalf("totalCompletedStageBytes() = %d, want 10", got)
	}
}
