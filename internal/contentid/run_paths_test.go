package contentid

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/tmdb"
)

func TestRunFetchesCanonicalSeasonAndSubtitleReferences(t *testing.T) {
	sess := episodeTestSession(t, "Program dialogue.")
	seasonCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/42/season/1" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		seasonCalls++
		_ = json.NewEncoder(w).Encode(episodeTestSeason())
	}))
	defer server.Close()
	h := episodeTestHandler(t, episodeTestClient(t, func(string) (string, float64) { return "E01", .95 }))
	h.tmdbClient = tmdb.New("test", server.URL, "en-US", nil)
	if err := h.Run(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if seasonCalls != 1 || sess.Env.Episodes[0].Episode != 1 || sess.Env.Episodes[0].NeedsReview {
		t.Fatalf("Run: %+v; lookups %d", sess.Env.Episodes, seasonCalls)
	}
}

func TestRunSeasonFailureRequiresRetry(t *testing.T) {
	sess := episodeTestSession(t, "Evidence.")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "missing", 404) }))
	defer server.Close()
	h := New(&config.Config{}, nil, tmdb.New("test", server.URL, "en-US", nil), nil, nil)
	if err := h.Run(context.Background(), sess); err == nil || !strings.Contains(err.Error(), "tmdb season acquisition") {
		t.Fatalf("lookup failure: %v", err)
	}
}

func TestGenerateEpisodeTranscriptsMissingAssetsAndCancellation(t *testing.T) {
	sess := episodeTestSession(t, "Evidence.")
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}}, nil, nil, nil, nil)
	if err := h.generateEpisodeTranscripts(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.generateEpisodeTranscripts(ctx, sess); err == nil {
		t.Fatal("canceled transcription succeeded")
	}
}
