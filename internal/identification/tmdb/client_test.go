package tmdb_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"spindle/internal/identification/tmdb"
)

func TestNewRequiresAPIKey(t *testing.T) {
	if _, err := tmdb.New("", "https://example.com", "en-US"); err == nil {
		t.Fatal("expected error when api key missing")
	}
}

func TestSearchMovieSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "key" {
			t.Fatalf("expected api_key query parameter, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"page":1,"results":[{"id":1,"title":"Example"}]}`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "en-US")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	resp, err := client.SearchMovie(context.Background(), "Example")
	if err != nil {
		t.Fatalf("SearchMovie returned error: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Title != "Example" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestSearchMovieHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status_code":500}`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if _, err := client.SearchMovie(context.Background(), "fail"); err == nil {
		t.Fatal("expected error when TMDB returns non-200")
	}
}

func TestSearchMovieEmptyQuery(t *testing.T) {
	client, err := tmdb.New("key", "https://example.com", "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, err := client.SearchMovie(context.Background(), "  "); err == nil {
		t.Fatal("expected error for empty query")
	}
}
