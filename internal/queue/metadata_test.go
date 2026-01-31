package queue

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestMetadataGetLibraryPathMovieUsesTitleFolder(t *testing.T) {
	fallback := "50 First Dates (2004)"
	payload := map[string]any{"media_type": "movie", "movie": true}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal movie metadata: %v", err)
	}
	meta := MetadataFromJSON(string(data), fallback)
	got := meta.GetLibraryPath("/library", "movies", "tv")
	want := filepath.Join("/library", "movies", "50 First Dates (2004)")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestMetadataGetLibraryPathTvBuildsHierarchy(t *testing.T) {
	meta := Metadata{
		MediaType:    "tv",
		ShowTitle:    "South Park",
		SeasonNumber: 5,
	}
	got := meta.GetLibraryPath("/library", "movies", "tv")
	want := filepath.Join("/library", "tv", "South Park", "Season 05")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestMetadataGetFilenameSanitizes(t *testing.T) {
	meta := MetadataFromJSON("", "Batman: The Long / Short")
	want := "Batman - The Long - Short"
	if meta.GetFilename() != want {
		t.Fatalf("expected sanitized filename %q, got %q", want, meta.GetFilename())
	}
}

func TestNewTVMetadataSetsEpisodeRange(t *testing.T) {
	meta := NewTVMetadata("South Park", 5, []int{3, 4}, "South Park Season 5 - Disc 1")
	if meta.MediaType != "tv" {
		t.Fatalf("expected tv media type, got %q", meta.MediaType)
	}
	if got := meta.GetFilename(); got != "South Park - S05E03-E04" {
		t.Fatalf("unexpected filename %q", got)
	}
	gotPath := meta.GetLibraryPath("/library", "movies", "tv")
	wantPath := filepath.Join("/library", "tv", "South Park", "Season 05")
	if gotPath != wantPath {
		t.Fatalf("expected path %q, got %q", wantPath, gotPath)
	}
}

func TestMetadataIsMoviePrefersMediaType(t *testing.T) {
	movieData, err := json.Marshal(map[string]any{"media_type": "movie"})
	if err != nil {
		t.Fatalf("marshal movie metadata: %v", err)
	}
	movieMeta := MetadataFromJSON(string(movieData), "Example")
	if !movieMeta.IsMovie() {
		t.Fatal("expected metadata with media_type movie to be treated as movie")
	}

	tvData, err := json.Marshal(map[string]any{"media_type": "tv", "movie": true})
	if err != nil {
		t.Fatalf("marshal tv metadata: %v", err)
	}
	tvMeta := MetadataFromJSON(string(tvData), "Example")
	if tvMeta.IsMovie() {
		t.Fatal("expected metadata with media_type tv to be treated as not movie")
	}
}
