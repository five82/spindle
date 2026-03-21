package tmdb

import (
	"context"
	"encoding/json"
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

func TestSelectBestResult_ExactMatch(t *testing.T) {
	results := []SearchResult{
		{ID: 1, Title: "Other Movie", ReleaseDate: "2010-01-01", VoteCount: 100},
		{ID: 2, Title: "Inception", ReleaseDate: "2010-07-16", VoteCount: 5000},
		{ID: 3, Title: "Inception Returns", ReleaseDate: "2015-01-01", VoteCount: 50},
	}

	best, score := SelectBestResult(results, "Inception", "2010", 100)
	if best == nil {
		t.Fatal("expected a result, got nil")
	}
	if best.ID != 2 {
		t.Errorf("expected ID 2, got %d", best.ID)
	}
	// exact title match (0.5) + year match (0.3) + vote count (0.2) = 1.0
	if score != 1.0 {
		t.Errorf("expected score 1.0, got %f", score)
	}
}

func TestSelectBestResult_NoResults(t *testing.T) {
	best, score := SelectBestResult(nil, "Inception", "2010", 100)
	if best != nil {
		t.Errorf("expected nil, got %+v", best)
	}
	if score != 0 {
		t.Errorf("expected score 0, got %f", score)
	}
}

func TestSelectBestResult_YearMatching(t *testing.T) {
	results := []SearchResult{
		{ID: 1, Title: "Dune", ReleaseDate: "1984-12-14", VoteCount: 500},
		{ID: 2, Title: "Dune", ReleaseDate: "2021-10-22", VoteCount: 500},
	}

	best, _ := SelectBestResult(results, "Dune", "2021", 100)
	if best == nil {
		t.Fatal("expected a result, got nil")
	}
	if best.ID != 2 {
		t.Errorf("expected ID 2 (2021 version), got %d", best.ID)
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
