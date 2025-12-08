package identification

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"spindle/internal/identification/tmdb"
)

func TestLookupTMDBByTitleReturnsBestMatch(t *testing.T) {
	var captured url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/movie" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		captured = r.URL.Query()
		payload := map[string]any{
			"page":          1,
			"total_pages":   1,
			"total_results": 1,
			"results": []map[string]any{
				{
					"id":           862,
					"title":        "Toy Story",
					"media_type":   "movie",
					"release_date": "1995-11-19",
					"vote_average": 8.3,
					"vote_count":   14569,
					"popularity":   65.4,
					"overview":     "A cowboy doll is profoundly threatened...",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	client, err := tmdb.New("key", server.URL, "en-US")
	if err != nil {
		t.Fatalf("tmdb.New failed: %v", err)
	}

	match, err := LookupTMDBByTitle(context.Background(), client, nil, "Toy Story", tmdb.SearchOptions{Year: 1995})
	if err != nil {
		t.Fatalf("LookupTMDBByTitle returned error: %v", err)
	}
	if match == nil {
		t.Fatal("expected match, got nil")
		return
	}
	if match.TMDBID != 862 {
		t.Fatalf("expected tmdb id 862, got %d", match.TMDBID)
	}
	if match.Title != "Toy Story" {
		t.Fatalf("expected title Toy Story, got %q", match.Title)
	}
	if match.MediaType != "movie" {
		t.Fatalf("expected media type movie, got %q", match.MediaType)
	}
	if match.Year != "1995" {
		t.Fatalf("expected year 1995, got %q", match.Year)
	}

	if captured == nil {
		t.Fatal("expected query parameters to be captured")
		return
	}
	if captured.Get("query") != "Toy Story" {
		t.Fatalf("expected query parameter Toy Story, got %q", captured.Get("query"))
	}
	if captured.Get("primary_release_year") != "1995" {
		t.Fatalf("expected year parameter 1995, got %q", captured.Get("primary_release_year"))
	}
}

func TestLookupTMDBByTitleNoMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/movie" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		payload := map[string]any{
			"page":          1,
			"total_pages":   0,
			"total_results": 0,
			"results":       []map[string]any{},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	client, err := tmdb.New("key", server.URL, "en-US")
	if err != nil {
		t.Fatalf("tmdb.New failed: %v", err)
	}

	match, err := LookupTMDBByTitle(context.Background(), client, nil, "Unknown", tmdb.SearchOptions{})
	if err != nil {
		t.Fatalf("LookupTMDBByTitle returned error: %v", err)
	}
	if match != nil {
		t.Fatalf("expected nil match, got %+v", match)
	}
}
