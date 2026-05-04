package main

import (
	"reflect"
	"testing"

	"github.com/five82/spindle/internal/tmdb"
)

func TestInferStandaloneSubtitleMetadata(t *testing.T) {
	tests := []struct {
		name string
		path string
		want standaloneSubtitleMetadata
	}{
		{
			name: "movie with parenthesized year",
			path: "/media/Munich (2005).mkv",
			want: standaloneSubtitleMetadata{MediaType: "movie", Title: "Munich", Year: "2005"},
		},
		{
			name: "movie release tags after year",
			path: "/media/Blade.Runner.2049.2017.2160p.BluRay.mkv",
			want: standaloneSubtitleMetadata{MediaType: "movie", Title: "Blade Runner 2049", Year: "2017"},
		},
		{
			name: "tv episode",
			path: "/media/Fringe.S02E05.1080p.WEB-DL.mkv",
			want: standaloneSubtitleMetadata{MediaType: "tv", Title: "Fringe", Season: 2, Episode: 5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferStandaloneSubtitleMetadata(tt.path)
			if got != tt.want {
				t.Fatalf("inferStandaloneSubtitleMetadata() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestFilterStandaloneTMDBResultsPrefersExpectedMediaType(t *testing.T) {
	results := []tmdb.SearchResult{
		{ID: 1, Title: "Fringe", MediaType: "movie"},
		{ID: 2, Name: "Fringe", MediaType: "tv"},
		{ID: 3, Name: "Someone", MediaType: "person"},
	}

	got := filterStandaloneTMDBResults(results, "tv")
	want := []tmdb.SearchResult{{ID: 2, Name: "Fringe", MediaType: "tv"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterStandaloneTMDBResults() = %#v, want %#v", got, want)
	}
}

func TestBuildCLISubtitleMuxArgsRegularAndForced(t *testing.T) {
	got := buildCLISubtitleMuxArgs("/tmp/out.mkv", "/media/movie.mkv", []cliSubtitleMuxTrack{
		{Path: "/tmp/movie.en.srt", Language: "en"},
		{Path: "/tmp/movie.en.forced.srt", Language: "en", Forced: true},
	})
	want := []string{
		"-o", "/tmp/out.mkv", "--no-subtitles", "/media/movie.mkv",
		"--language", "0:eng", "--track-name", "0:English", "--default-track-flag", "0:no", "--forced-track", "0:no", "/tmp/movie.en.srt",
		"--language", "0:eng", "--track-name", "0:English (Forced)", "--default-track-flag", "0:yes", "--forced-track", "0:yes", "/tmp/movie.en.forced.srt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildCLISubtitleMuxArgs() = %#v, want %#v", got, want)
	}
}
