package mediameta

import "testing"

func TestDestFilenameForMovieTVAndUnresolvedRip(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		meta                  *Metadata
		key                   string
		season, episode, last int
		want                  string
	}{
		{"nil metadata", nil, "My: File", 0, 0, 0, "My File.mkv"},
		{"movie", &Metadata{Title: "Movie", MediaType: "movie", Year: "2024", ID: 42}, "main", 0, 0, 0, "Movie (2024) [tmdbid-42].mkv"},
		{"tv", &Metadata{ShowTitle: "Show", MediaType: "tv", SeasonNumber: 1}, "s01_001", 1, 2, 2, "Show - S01E02.mkv"},
		{"unresolved", &Metadata{ShowTitle: "Show", MediaType: "tv"}, "s01_001", 0, 0, 0, "Show - s01_001.mkv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DestFilename(tc.meta, tc.key, ".mkv", tc.season, tc.episode, tc.last)
			if got != tc.want {
				t.Fatalf("destination = %q, want %q", got, tc.want)
			}
		})
	}
}
