package mediameta

import "testing"

func TestMetadataIsMovie(t *testing.T) {
	tests := []struct {
		mediaType string
		movieBool bool
		want      bool
	}{
		{"movie", false, true},
		{"film", false, true},
		{"tv", true, false},
		{"tv_show", true, false},
		{"television", false, false},
		{"series", false, false},
		{"", true, true},
		{"", false, false},
		{"unknown_type", true, true},
	}

	for _, tt := range tests {
		m := Metadata{MediaType: tt.mediaType, Movie: tt.movieBool}
		got := m.IsMovie()
		if got != tt.want {
			t.Errorf("IsMovie(mediaType=%q, movie=%v) = %v, want %v",
				tt.mediaType, tt.movieBool, got, tt.want)
		}
	}
}

func TestFilenameMovie(t *testing.T) {
	m := Metadata{
		Title:     "The Matrix",
		MediaType: "movie",
		Year:      "1999",
	}
	got := m.Filename()
	want := "The Matrix (1999)"
	if got != want {
		t.Errorf("Filename() = %q, want %q", got, want)
	}
}

func TestFilenameTV(t *testing.T) {
	m := Metadata{
		ShowTitle:    "Breaking Bad",
		MediaType:    "tv",
		SeasonNumber: 1,
	}
	got := m.Filename()
	want := "Breaking Bad - Season 01"
	if got != want {
		t.Errorf("Filename() no eps = %q, want %q", got, want)
	}

	m.Episodes = []Episode{{Season: 1, Episode: 3}}
	got = m.Filename()
	want = "Breaking Bad - S01E03"
	if got != want {
		t.Errorf("Filename() single = %q, want %q", got, want)
	}

	m.Episodes = []Episode{{Season: 1, Episode: 1, EpisodeEnd: 2}}
	got = m.Filename()
	want = "Breaking Bad - S01E01-E02"
	if got != want {
		t.Errorf("Filename() range = %q, want %q", got, want)
	}

	m.Episodes = []Episode{
		{Season: 1, Episode: 3},
		{Season: 1, Episode: 4},
		{Season: 1, Episode: 5},
	}
	got = m.Filename()
	want = "Breaking Bad - S01E03-E05"
	if got != want {
		t.Errorf("Filename() multi = %q, want %q", got, want)
	}
}

func TestLibraryPathMovie(t *testing.T) {
	m := Metadata{
		Title:     "Inception",
		MediaType: "movie",
		Year:      "2010",
	}
	got, err := m.LibraryPath("/media", "movies", "tv")
	if err != nil {
		t.Fatalf("LibraryPath: %v", err)
	}
	want := "/media/movies/Inception (2010)"
	if got != want {
		t.Errorf("LibraryPath() = %q, want %q", got, want)
	}
}

func TestLibraryPathTV(t *testing.T) {
	m := Metadata{
		ShowTitle:    "The Office",
		MediaType:    "tv",
		SeasonNumber: 3,
	}
	got, err := m.LibraryPath("/media", "movies", "tv")
	if err != nil {
		t.Fatalf("LibraryPath: %v", err)
	}
	want := "/media/tv/The Office/Season 03"
	if got != want {
		t.Errorf("LibraryPath() = %q, want %q", got, want)
	}
}

func TestFromJSON(t *testing.T) {
	data := `{"title":"Test Movie","media_type":"movie","year":"2020"}`
	m := FromJSON(data, "Fallback")
	if m.Title != "Test Movie" {
		t.Errorf("title = %q, want %q", m.Title, "Test Movie")
	}
	if m.Year != "2020" {
		t.Errorf("year = %q, want %q", m.Year, "2020")
	}

	m = FromJSON("", "Fallback Title")
	if m.Title != "Fallback Title" {
		t.Errorf("fallback title = %q, want %q", m.Title, "Fallback Title")
	}

	m = FromJSON("{invalid", "Fallback")
	if m.Title != "Fallback" {
		t.Errorf("invalid json title = %q, want %q", m.Title, "Fallback")
	}
}
