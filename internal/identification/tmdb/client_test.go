package tmdb_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"spindle/internal/identification/tmdb"
)

func TestNewRequiresAPIKey(t *testing.T) {
	t.Parallel()
	if _, err := tmdb.New("", "https://example.com", "en-US"); err == nil {
		t.Fatal("expected error when api key missing")
	}
}

func TestNewRequiresBaseURL(t *testing.T) {
	t.Parallel()
	if _, err := tmdb.New("key", "", "en-US"); err == nil {
		t.Fatal("expected error when base url missing")
	}
}

func TestSearchMovieSuccess(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	client, err := tmdb.New("key", "https://example.com", "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, err := client.SearchMovie(context.Background(), "  "); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestSearchTVSuccess(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/search/tv") {
			t.Fatalf("expected /search/tv path, got %q", r.URL.Path)
		}
		if r.URL.Query().Get("api_key") != "key" {
			t.Fatalf("expected api_key query parameter")
		}
		if r.URL.Query().Get("query") != "Breaking Bad" {
			t.Fatalf("expected query parameter 'Breaking Bad', got %q", r.URL.Query().Get("query"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"page": 1,
			"results": [
				{"id": 1396, "name": "Breaking Bad", "first_air_date": "2008-01-20", "vote_average": 9.5}
			],
			"total_results": 1
		}`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "en-US")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	resp, err := client.SearchTVWithOptions(context.Background(), "Breaking Bad", tmdb.SearchOptions{})
	if err != nil {
		t.Fatalf("SearchTVWithOptions returned error: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	if resp.Results[0].Name != "Breaking Bad" {
		t.Fatalf("expected name 'Breaking Bad', got %q", resp.Results[0].Name)
	}
	if resp.Results[0].ID != 1396 {
		t.Fatalf("expected ID 1396, got %d", resp.Results[0].ID)
	}
}

func TestSearchTVWithYearFilter(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("first_air_date_year") != "2008" {
			t.Fatalf("expected first_air_date_year=2008, got %q", r.URL.Query().Get("first_air_date_year"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"page":1,"results":[]}`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "en-US")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.SearchTVWithOptions(context.Background(), "Test", tmdb.SearchOptions{Year: 2008})
	if err != nil {
		t.Fatalf("SearchTVWithOptions returned error: %v", err)
	}
}

func TestSearchTVEmptyQuery(t *testing.T) {
	t.Parallel()
	client, err := tmdb.New("key", "https://example.com", "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, err := client.SearchTVWithOptions(context.Background(), "  ", tmdb.SearchOptions{}); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestSearchMultiSuccess(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/search/multi") {
			t.Fatalf("expected /search/multi path, got %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"page": 1,
			"results": [
				{"id": 1, "title": "Movie Result", "media_type": "movie"},
				{"id": 2, "name": "TV Result", "media_type": "tv"}
			]
		}`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "en-US")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	resp, err := client.SearchMultiWithOptions(context.Background(), "Test", tmdb.SearchOptions{})
	if err != nil {
		t.Fatalf("SearchMultiWithOptions returned error: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	if resp.Results[0].MediaType != "movie" {
		t.Fatalf("expected first result media_type 'movie', got %q", resp.Results[0].MediaType)
	}
	if resp.Results[1].MediaType != "tv" {
		t.Fatalf("expected second result media_type 'tv', got %q", resp.Results[1].MediaType)
	}
}

func TestGetSeasonDetailsSuccess(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/tv/1396/season/1") {
			t.Fatalf("expected /tv/1396/season/1 path, got %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": 3572,
			"name": "Season 1",
			"season_number": 1,
			"episodes": [
				{"id": 62085, "name": "Pilot", "episode_number": 1, "season_number": 1, "air_date": "2008-01-20"},
				{"id": 62086, "name": "Cat's in the Bag...", "episode_number": 2, "season_number": 1, "air_date": "2008-01-27"}
			]
		}`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "en-US")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	season, err := client.GetSeasonDetails(context.Background(), 1396, 1)
	if err != nil {
		t.Fatalf("GetSeasonDetails returned error: %v", err)
	}
	if season.SeasonNumber != 1 {
		t.Fatalf("expected season_number 1, got %d", season.SeasonNumber)
	}
	if len(season.Episodes) != 2 {
		t.Fatalf("expected 2 episodes, got %d", len(season.Episodes))
	}
	if season.Episodes[0].Name != "Pilot" {
		t.Fatalf("expected first episode 'Pilot', got %q", season.Episodes[0].Name)
	}
	if season.Episodes[1].EpisodeNumber != 2 {
		t.Fatalf("expected second episode number 2, got %d", season.Episodes[1].EpisodeNumber)
	}
}

func TestGetSeasonDetailsInvalidShowID(t *testing.T) {
	t.Parallel()
	client, err := tmdb.New("key", "https://example.com", "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, err := client.GetSeasonDetails(context.Background(), 0, 1); err == nil {
		t.Fatal("expected error for invalid show ID")
	}
	if _, err := client.GetSeasonDetails(context.Background(), -1, 1); err == nil {
		t.Fatal("expected error for negative show ID")
	}
}

func TestGetSeasonDetailsInvalidSeasonNumber(t *testing.T) {
	t.Parallel()
	client, err := tmdb.New("key", "https://example.com", "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, err := client.GetSeasonDetails(context.Background(), 1396, 0); err == nil {
		t.Fatal("expected error for invalid season number")
	}
	if _, err := client.GetSeasonDetails(context.Background(), 1396, -1); err == nil {
		t.Fatal("expected error for negative season number")
	}
}

func TestGetSeasonDetailsHTTPError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status_code":34,"status_message":"The resource you requested could not be found."}`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.GetSeasonDetails(context.Background(), 9999999, 1)
	if err == nil {
		t.Fatal("expected error for non-existent show")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 in error message, got %v", err)
	}
}

func TestSearchMovieWithYearFilter(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("primary_release_year") != "2010" {
			t.Fatalf("expected primary_release_year=2010, got %q", r.URL.Query().Get("primary_release_year"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"page":1,"results":[{"id":27205,"title":"Inception","release_date":"2010-07-16"}]}`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "en-US")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	resp, err := client.SearchMovieWithOptions(context.Background(), "Inception", tmdb.SearchOptions{Year: 2010})
	if err != nil {
		t.Fatalf("SearchMovieWithOptions returned error: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Title != "Inception" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestRateLimitResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"status_code":25,"status_message":"Your request count is over the allowed limit."}`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.SearchMovie(context.Background(), "Test")
	if err == nil {
		t.Fatal("expected error for rate limit response")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("expected 429 status in error, got %v", err)
	}
}

func TestContextTimeout(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"page":1,"results":[]}`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = client.SearchMovie(ctx, "Test")
	if err == nil {
		t.Fatal("expected error for timeout")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") && !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected context deadline error, got %v", err)
	}
}

func TestContextCancellation(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"page":1,"results":[]}`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately
	cancel()

	_, err = client.SearchMovie(ctx, "Test")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestMalformedJSONResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.SearchMovie(context.Background(), "Test")
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestLanguageParameter(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("language") != "de-DE" {
			t.Fatalf("expected language=de-DE, got %q", r.URL.Query().Get("language"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"page":1,"results":[]}`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "de-DE")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.SearchMovie(context.Background(), "Test")
	if err != nil {
		t.Fatalf("SearchMovie returned error: %v", err)
	}
}

func TestSearchOptionsKey(t *testing.T) {
	t.Parallel()

	opts1 := tmdb.SearchOptions{Year: 2020, Runtime: 120}
	opts2 := tmdb.SearchOptions{Year: 2020, Runtime: 120}
	opts3 := tmdb.SearchOptions{Year: 2021, Runtime: 120}

	if opts1.CacheKey() != opts2.CacheKey() {
		t.Error("expected identical options to produce same cache key")
	}
	if opts1.CacheKey() == opts3.CacheKey() {
		t.Error("expected different years to produce different cache keys")
	}
}

func TestEmptyResultsResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"page":1,"results":[],"total_results":0,"total_pages":0}`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	resp, err := client.SearchMovie(context.Background(), "NonexistentMovie12345")
	if err != nil {
		t.Fatalf("SearchMovie returned error: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Fatalf("expected empty results, got %d", len(resp.Results))
	}
	if resp.TotalResults != 0 {
		t.Fatalf("expected total_results 0, got %d", resp.TotalResults)
	}
}

func TestFullResponseParsing(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"page": 1,
			"results": [{
				"id": 27205,
				"title": "Inception",
				"overview": "A thief who steals corporate secrets...",
				"release_date": "2010-07-16",
				"popularity": 83.952,
				"vote_average": 8.4,
				"vote_count": 34567
			}],
			"total_results": 1,
			"total_pages": 1
		}`))
	}))
	t.Cleanup(server.Close)

	client, err := tmdb.New("key", server.URL, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	resp, err := client.SearchMovie(context.Background(), "Inception")
	if err != nil {
		t.Fatalf("SearchMovie returned error: %v", err)
	}

	if resp.Page != 1 {
		t.Errorf("expected page 1, got %d", resp.Page)
	}
	if resp.TotalResults != 1 {
		t.Errorf("expected total_results 1, got %d", resp.TotalResults)
	}
	if resp.TotalPages != 1 {
		t.Errorf("expected total_pages 1, got %d", resp.TotalPages)
	}

	result := resp.Results[0]
	if result.ID != 27205 {
		t.Errorf("expected ID 27205, got %d", result.ID)
	}
	if result.Title != "Inception" {
		t.Errorf("expected title 'Inception', got %q", result.Title)
	}
	if result.ReleaseDate != "2010-07-16" {
		t.Errorf("expected release_date '2010-07-16', got %q", result.ReleaseDate)
	}
	if result.VoteAverage != 8.4 {
		t.Errorf("expected vote_average 8.4, got %f", result.VoteAverage)
	}
	if result.VoteCount != 34567 {
		t.Errorf("expected vote_count 34567, got %d", result.VoteCount)
	}
}
