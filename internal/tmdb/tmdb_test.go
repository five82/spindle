package tmdb

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDisplayTitle(t *testing.T) {
	tests := []struct {
		name   string
		result SearchResult
		want   string
	}{
		{
			name:   "movie with title",
			result: SearchResult{Title: "Inception", Name: ""},
			want:   "Inception",
		},
		{
			name:   "tv with name only",
			result: SearchResult{Title: "", Name: "Breaking Bad"},
			want:   "Breaking Bad",
		},
		{
			name:   "both set prefers title",
			result: SearchResult{Title: "Movie Title", Name: "TV Name"},
			want:   "Movie Title",
		},
		{
			name:   "both empty",
			result: SearchResult{},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.result.DisplayTitle()
			if got != tt.want {
				t.Errorf("DisplayTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestYear(t *testing.T) {
	tests := []struct {
		name   string
		result SearchResult
		want   string
	}{
		{
			name:   "movie release date",
			result: SearchResult{ReleaseDate: "2010-07-16"},
			want:   "2010",
		},
		{
			name:   "tv first air date",
			result: SearchResult{FirstAirDate: "2008-01-20"},
			want:   "2008",
		},
		{
			name:   "prefers release date",
			result: SearchResult{ReleaseDate: "2010-07-16", FirstAirDate: "2008-01-20"},
			want:   "2010",
		},
		{
			name:   "empty dates",
			result: SearchResult{},
			want:   "",
		},
		{
			name:   "short date",
			result: SearchResult{ReleaseDate: "20"},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.result.Year()
			if got != tt.want {
				t.Errorf("Year() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectBestResult_ExactMatchAccepted(t *testing.T) {
	results := []SearchResult{
		{ID: 1, Title: "Other Movie", VoteAverage: 7.0, VoteCount: 100},
		{ID: 2, Title: "Inception", ReleaseDate: "2010-07-16", VoteAverage: 8.4, VoteCount: 5000},
	}
	best := SelectBestResult(slog.Default(), results, "Inception", 0, 5)
	if best == nil {
		t.Fatal("expected a result, got nil")
	}
	if best.ID != 2 {
		t.Errorf("expected ID 2, got %d", best.ID)
	}
}

func TestSelectBestResult_ExactMatchRejectedLowVotes(t *testing.T) {
	results := []SearchResult{
		{ID: 1, Title: "Munich", ReleaseDate: "1972-01-01", VoteAverage: 5.0, VoteCount: 0},
	}
	best := SelectBestResult(slog.Default(), results, "Munich", 0, 5)
	if best != nil {
		t.Errorf("expected nil (below vote threshold), got ID %d", best.ID)
	}
}

func TestSelectBestResult_NonExactAccepted(t *testing.T) {
	// "Inception" contains "inception" — match = 1.0.
	// score = 1.0 + 8.4/10 + 5000/1000 = 1.0 + 0.84 + 5.0 = 6.84
	// threshold = 1.3 + 5000/1000 = 6.3 — passes.
	results := []SearchResult{
		{ID: 1, Title: "Inception: The Beginning", VoteAverage: 8.4, VoteCount: 5000},
	}
	best := SelectBestResult(slog.Default(), results, "Inception", 0, 5)
	if best == nil {
		t.Fatal("expected a result, got nil")
	}
	if best.ID != 1 {
		t.Errorf("expected ID 1, got %d", best.ID)
	}
}

func TestSelectBestResult_NonExactRejectedLowAverage(t *testing.T) {
	results := []SearchResult{
		{ID: 1, Title: "Inception: The Beginning", VoteAverage: 2.0, VoteCount: 5000},
	}
	best := SelectBestResult(slog.Default(), results, "Inception", 0, 5)
	if best != nil {
		t.Errorf("expected nil (vote_average below 3.0), got ID %d", best.ID)
	}
}

func TestSelectBestResult_ExactPreferredOverHigherScore(t *testing.T) {
	results := []SearchResult{
		// Non-exact but higher score due to massive vote count.
		{ID: 1, Title: "Munich: The Documentary", VoteAverage: 9.0, VoteCount: 50000},
		// Exact match with lower score.
		{ID: 2, Title: "Munich", ReleaseDate: "2005-12-23", VoteAverage: 7.0, VoteCount: 3000},
	}
	best := SelectBestResult(slog.Default(), results, "Munich", 0, 5)
	if best == nil {
		t.Fatal("expected a result, got nil")
	}
	if best.ID != 2 {
		t.Errorf("expected exact match ID 2, got %d", best.ID)
	}
}

func TestSelectBestResult_YearDisambiguation(t *testing.T) {
	results := []SearchResult{
		{ID: 1, Title: "Dune", ReleaseDate: "1984-12-14", VoteAverage: 6.0, VoteCount: 500},
		{ID: 2, Title: "Dune", ReleaseDate: "2021-10-22", VoteAverage: 7.8, VoteCount: 8000},
	}
	best := SelectBestResult(slog.Default(), results, "Dune", 2021, 5)
	if best == nil {
		t.Fatal("expected a result, got nil")
	}
	if best.ID != 2 {
		t.Errorf("expected ID 2 (2021 version), got %d", best.ID)
	}
}

func TestSelectBestResult_NoResults(t *testing.T) {
	best := SelectBestResult(slog.Default(), nil, "Inception", 0, 5)
	if best != nil {
		t.Errorf("expected nil, got %+v", best)
	}
}

func TestNormalizeForComparison(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Munich", "munich"},
		{"Rock & Roll", "rockandroll"},
		{"C++ Programming", "candandprogramming"},
		{"Hello, World!", "helloworld"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeForComparison(tt.input)
		if got != tt.want {
			t.Errorf("normalizeForComparison(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSearchMovie_HTTPTest(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")

		resp := searchResponse{
			Results: []SearchResult{
				{ID: 27205, Title: "Inception", ReleaseDate: "2010-07-16"},
			},
			TotalPages: 1,
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	defer srv.Close()

	client := New("test-api-key", srv.URL, "en-US")
	results, err := client.SearchMovie(context.Background(), "Inception", "2010")
	if err != nil {
		t.Fatalf("SearchMovie() error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ID != 27205 {
		t.Errorf("expected ID 27205, got %d", results[0].ID)
	}
	if results[0].Title != "Inception" {
		t.Errorf("expected title Inception, got %q", results[0].Title)
	}

	// Verify auth header is set.
	if capturedAuth != "Bearer test-api-key" {
		t.Errorf("expected Authorization header %q, got %q", "Bearer test-api-key", capturedAuth)
	}
}

func TestGetMovie_HTTPTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movie/27205" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		detail := MovieDetail{
			ID:          27205,
			Title:       "Inception",
			ReleaseDate: "2010-07-16",
			IMDBID:      "tt1375666",
			Runtime:     148,
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(detail); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	defer srv.Close()

	client := New("test-key", srv.URL, "")
	movie, err := client.GetMovie(context.Background(), 27205)
	if err != nil {
		t.Fatalf("GetMovie() error: %v", err)
	}
	if movie.IMDBID != "tt1375666" {
		t.Errorf("expected IMDB ID tt1375666, got %q", movie.IMDBID)
	}
	if movie.Runtime != 148 {
		t.Errorf("expected runtime 148, got %d", movie.Runtime)
	}
}

func TestGetSeason_HTTPTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/1396/season/1" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		season := Season{
			SeasonNumber: 1,
			Episodes: []Episode{
				{EpisodeNumber: 1, Name: "Pilot", Runtime: 58},
				{EpisodeNumber: 2, Name: "Cat's in the Bag...", Runtime: 48},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(season); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	defer srv.Close()

	client := New("test-key", srv.URL, "en-US")
	season, err := client.GetSeason(context.Background(), 1396, 1)
	if err != nil {
		t.Fatalf("GetSeason() error: %v", err)
	}
	if season.SeasonNumber != 1 {
		t.Errorf("expected season 1, got %d", season.SeasonNumber)
	}
	if len(season.Episodes) != 2 {
		t.Fatalf("expected 2 episodes, got %d", len(season.Episodes))
	}
	if season.Episodes[0].Name != "Pilot" {
		t.Errorf("expected first episode Pilot, got %q", season.Episodes[0].Name)
	}
}

func TestAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"results":[],"total_pages":0}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	defer srv.Close()

	client := New("my-secret-key", srv.URL, "")
	_, err := client.SearchMulti(context.Background(), "test")
	if err != nil {
		t.Fatalf("SearchMulti() error: %v", err)
	}

	expected := "Bearer my-secret-key"
	if gotAuth != expected {
		t.Errorf("Authorization header = %q, want %q", gotAuth, expected)
	}
}
