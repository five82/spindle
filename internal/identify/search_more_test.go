package identify

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/makemkv"
	"github.com/five82/spindle/internal/tmdb"
)

func TestTVSearchFallsBackToMultiAndHandlesSearchFailures(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.Contains(r.URL.Path, "/search/tv") {
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"id":77,"name":"Example Show","media_type":"tv","first_air_date":"2021-01-01","vote_average":9,"vote_count":5000}]}`))
	}))
	defer server.Close()
	h := &Handler{tmdbClient: tmdb.New("key", server.URL, "en-US", nil)}
	result := &IdentifyResult{QueryTitle: "Example Show", MediaHint: "tv"}
	if err := h.searchTMDB(context.Background(), result, slog.Default()); err != nil || result.Best == nil || result.Best.ID != 77 || len(paths) != 2 {
		t.Fatalf("fallback search: %+v %v paths=%v", result, err, paths)
	}
	if got := canonicalTitle(*result.Best, "tv", "Example Show Season 3", nil); got != "Example Show Season 03 (2021)" {
		t.Fatalf("title: %q", got)
	}
	if got := canonicalTitle(*result.Best, "tv", "Example Show", &makemkv.DiscInfo{Name: "Example Show S02"}); !strings.Contains(got, "Season 02") {
		t.Fatalf("disc season: %q", got)
	}
	server.Close()
	result = &IdentifyResult{QueryTitle: "Example Show", MediaHint: "tv"}
	if err := h.searchTMDB(context.Background(), result, slog.Default()); err == nil || !strings.Contains(err.Error(), "tmdb search (tv)") {
		t.Fatalf("TV error: %v", err)
	}
	result.MediaHint = "movie"
	if err := h.searchTMDB(context.Background(), result, slog.Default()); err == nil || !strings.Contains(err.Error(), "tmdb search:") {
		t.Fatalf("multi error: %v", err)
	}
}
