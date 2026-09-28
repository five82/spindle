package mediameta

import (
	"strings"
	"testing"
)

func TestLibraryNamingFallbacksAndUnsafeRoots(t *testing.T) {
	meta := NewBasic("", true)
	if got := meta.BaseFilename(); got != "Manual Import" {
		t.Fatalf("untitled movie: %q", got)
	}
	meta.DisplayTitle = "Operator Edition"
	meta.Year = "2024"
	if got := meta.BaseFilename(); !strings.Contains(got, "Operator Edition (2024)") {
		t.Fatalf("display title: %q", got)
	}
	if _, err := meta.LibraryPath("/library", "../outside", "TV"); err == nil {
		t.Fatal("escaping movies dir accepted")
	}
	meta.MediaType = "tv"
	meta.Movie = false
	meta.ShowTitle = ""
	if _, err := meta.LibraryPath("/library", "Movies", "../outside"); err == nil {
		t.Fatal("escaping TV dir accepted")
	}
	meta.Title = ""
	meta.DisplayTitle = ""
	if got := meta.Filename(); got != "Manual Import - Season 00" {
		t.Fatalf("untitled TV: %q", got)
	}
	meta.Episodes = []Episode{{Season: 1, Episode: 1}, {Season: 1, Episode: 2, EpisodeEnd: 3}}
	if got := meta.Filename(); got != "Manual Import - S01E01-E03" {
		t.Fatalf("range: %q", got)
	}
	if m := FromJSON(`{"media_type":"movie"}`, "Fallback"); m.Title != "Fallback" {
		t.Fatalf("fallback title: %+v", m)
	}
}
