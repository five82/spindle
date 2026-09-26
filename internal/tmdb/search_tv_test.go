package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchTVFiltersYearAndMarksMediaType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/tv" || r.URL.Query().Get("query") != "The Show" || r.URL.Query().Get("first_air_date_year") != "2021" || r.URL.Query().Get("language") != "en-US" {
			t.Errorf("unexpected search: %s", r.URL.String())
		}
		_, _ = w.Write([]byte(`{"results":[{"id":7,"name":"The Show"}]}`))
	}))
	defer srv.Close()
	results, err := New("key", srv.URL, "en-US", nil).SearchTV(context.Background(), "The Show", "2021")
	if err != nil || len(results) != 1 || results[0].MediaType != "tv" || results[0].Name != "The Show" {
		t.Fatalf("SearchTV: %+v, %v", results, err)
	}
}

func TestSearchTVPropagatesHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer srv.Close()
	results, err := New("key", srv.URL, "en-US", nil).SearchTV(context.Background(), "missing", "")
	if err == nil || results != nil {
		t.Fatalf("SearchTV failure: %+v, %v", results, err)
	}
}
