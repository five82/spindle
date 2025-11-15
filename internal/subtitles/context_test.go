package subtitles

import (
	"testing"

	"spindle/internal/queue"
)

func TestBuildSubtitleContextFromMetadata(t *testing.T) {
	item := &queue.Item{
		DiscTitle: "Example Disc",
		MetadataJSON: `{
			"id": 12345,
			"title": "Example Movie",
			"media_type": "movie",
			"release_date": "2024-07-04",
			"language": "en",
			"imdb_id": "tt1234567"
		}`,
		RipSpecData: `{
			"content_key":"tmdb:movie:12345",
			"metadata":{"release_date":"2024-07-04"}
		}`,
	}

	ctx := BuildSubtitleContext(item)

	if ctx.Title != "Example Movie" {
		t.Fatalf("expected title Example Movie, got %q", ctx.Title)
	}
	if ctx.MediaType != "movie" {
		t.Fatalf("expected media type movie, got %q", ctx.MediaType)
	}
	if ctx.TMDBID != 12345 {
		t.Fatalf("expected TMDB id 12345, got %d", ctx.TMDBID)
	}
	if ctx.Year != "2024" {
		t.Fatalf("expected year 2024, got %q", ctx.Year)
	}
	if ctx.IMDBID != "tt1234567" {
		t.Fatalf("expected imdb id tt1234567, got %q", ctx.IMDBID)
	}
	if ctx.Language != "en" {
		t.Fatalf("expected language en, got %q", ctx.Language)
	}
	if ctx.ContentKey != "tmdb:movie:12345" {
		t.Fatalf("expected content key tmdb:movie:12345, got %q", ctx.ContentKey)
	}
	if ctx.ShowTitle != "" {
		t.Fatalf("expected empty show title for movie, got %q", ctx.ShowTitle)
	}
}

func TestBuildSubtitleContextFallsBackToDiscTitle(t *testing.T) {
	item := &queue.Item{
		DiscTitle: "Unknown Title (1999)",
	}

	ctx := BuildSubtitleContext(item)

	if ctx.Title != "Unknown Title (1999)" {
		t.Fatalf("expected fallback title, got %q", ctx.Title)
	}
	if ctx.Year != "1999" {
		t.Fatalf("expected derived year 1999, got %q", ctx.Year)
	}
	if ctx.TMDBID != 0 {
		t.Fatalf("expected tmdb id 0, got %d", ctx.TMDBID)
	}
	if ctx.Language != "" {
		t.Fatalf("expected empty language, got %q", ctx.Language)
	}
	if ctx.ContentKey != "" {
		t.Fatalf("expected empty content key, got %q", ctx.ContentKey)
	}
	if ctx.ShowTitle != "Unknown Title (1999)" {
		t.Fatalf("expected show title fallback, got %q", ctx.ShowTitle)
	}
}

func TestBuildSubtitleContextSetsShowTitleFromMetadata(t *testing.T) {
	item := &queue.Item{
		DiscTitle: "South Park",
		MetadataJSON: `{
			"title": "South Park – Disc 1",
			"media_type": "tv",
			"show_title": "South Park",
			"season_number": 5,
			"episode_numbers": [1]
		}`,
	}
	ctx := BuildSubtitleContext(item)
	if ctx.ShowTitle != "South Park" {
		t.Fatalf("expected show title South Park, got %q", ctx.ShowTitle)
	}
	if ctx.SeriesTitle() != "South Park" {
		t.Fatalf("expected series title South Park, got %q", ctx.SeriesTitle())
	}
	ctx.Title = "South Park – Terrance and Phillip"
	ctx.ShowTitle = ""
	if got := ctx.SeriesTitle(); got != "South Park" {
		t.Fatalf("expected derived series title South Park, got %q", got)
	}
}
