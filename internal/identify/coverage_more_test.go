package identify

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/tmdb"
)

func TestExpectedEpisodesSeasonLookup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/42/season/2" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"episodes":[{"episode_number":1,"name":"Pilot"},{"episode_number":2,"name":"Next"}]}`))
	}))
	defer server.Close()
	h := &Handler{tmdbClient: tmdb.New("test", server.URL, "en-US", nil)}
	if got := h.fetchExpectedEpisodes(context.Background(), slog.Default(), 42, 2); len(got) != 2 || got[1].EpisodeNumber != 2 {
		t.Fatalf("episodes: %+v", got)
	}
	if got := h.fetchExpectedEpisodes(context.Background(), slog.Default(), 0, 2); got != nil {
		t.Fatalf("invalid ID fetched: %+v", got)
	}
	server.Close()
	if got := h.fetchExpectedEpisodes(context.Background(), slog.Default(), 42, 2); got != nil {
		t.Fatalf("lookup failure: %+v", got)
	}
}

func TestTVTitleSelectionHelpers(t *testing.T) {
	candidates := []tvTitleCandidate{{title: ripspec.Title{ID: 4, Duration: 200}}, {title: ripspec.Title{ID: 3, Duration: 200}}, {title: ripspec.Title{ID: 2, Duration: 199}}}
	if got := longestCandidate(candidates); got.title.ID != 3 {
		t.Fatalf("longest: %+v", got)
	}
	if got := summarizeAmbiguity(nil); got != "" {
		t.Fatal(got)
	}
	if got := summarizeAmbiguity([]string{"same runtime", "shared segments"}); got != "same runtime, shared segments" {
		t.Fatal(got)
	}
	if _, ok := parseSegmentSet("1,broken"); ok {
		t.Fatal("invalid segment accepted")
	}
}
