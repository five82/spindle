package contentid

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/tmdb"
)

func TestReferenceDownloadAndCacheReuse(t *testing.T) {
	sess := episodeTestSession(t, "Evidence.")
	season := episodeTestSeason()
	season.Episodes = season.Episodes[:1]
	h := episodeTestHandler(t, nil, season)
	payload := "1\n00:00:00,000 --> 00:45:00,000\nFull downloaded dialogue.\n"
	downloads, searches, fetches := 0, 0, 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subtitles":
			searches++
			_, _ = w.Write([]byte(`{"data":[{"attributes":{"language":"en","release":"First","files":[{"file_id":77,"file_name":"First.srt"}]}}]}`))
		case "/download":
			downloads++
			var req struct {
				FileID int    `json:"file_id"`
				Format string `json:"sub_format"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.FileID != 77 || req.Format != "srt" {
				t.Errorf("download request = %+v, %v", req, err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"link": server.URL + "/file"})
		case "/file":
			fetches++
			_, _ = w.Write([]byte(payload))
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	}))
	defer server.Close()
	for range 2 {
		h.osClient = opensubtitles.New(opensubtitles.Params{APIKey: "test", BaseURL: server.URL}, nil)
		dir := filepath.Join(t.TempDir(), "references")
		refs, err := h.fetchReferences(context.Background(), sess, season, dir)
		if err != nil || refs["E01"].fileID != 77 || refs["E01"].text != "Full downloaded dialogue." {
			t.Fatalf("references = %+v, %v", refs, err)
		}
		for _, path := range []string{filepath.Join(dir, "s01e01-77.srt"), filepath.Join(h.cfg.OpenSubtitlesCacheDir(), "77.srt")} {
			if got, err := os.ReadFile(path); err != nil || string(got) != payload {
				t.Fatalf("full reference %s = %q, %v", path, got, err)
			}
		}
	}
	if downloads != 1 || fetches != 1 || searches != 2 {
		t.Fatalf("cache did not preserve quota or revalidate labels: searches=%d downloads=%d fetches=%d", searches, downloads, fetches)
	}
}

func TestPartialReferenceCatalogDoesNotFillMissingIdentity(t *testing.T) {
	sess := episodeTestSession(t, "E01", "Unavailable episode")
	h := episodeTestHandler(t, episodeTestClient(t, func(text string) (string, float64) {
		if text == "E01" {
			return "E01", 1
		}
		return "none", 1
	}))
	season := episodeTestSeason()
	season.Episodes = append(season.Episodes, tmdb.Episode{EpisodeNumber: 3, Name: "Third", Overview: "A tempting synopsis", Runtime: 45})
	if err := h.classifyEpisodes(context.Background(), sess, season); err != nil {
		t.Fatal(err)
	}
	if ep := sess.Env.Episodes[1]; ep.Episode != 0 || !ep.NeedsReview {
		t.Fatalf("unavailable reference guessed: %+v", ep)
	}
	if s := sess.Env.Attributes.ContentID; !s.Completed || s.ReferenceEpisodes != 2 || s.MatchedEpisodes != 1 || s.UnresolvedEpisodes != 1 {
		t.Fatalf("partial catalog summary: %+v", s)
	}
	root, err := sess.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(root, "contentid", "references", "s01e03-*.srt"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("unavailable reference staged: %v, %v", matches, err)
	}
}

func TestMovieIdentificationDoesNotAcquireReferences(t *testing.T) {
	sess := episodeTestSession(t, "Movie")
	sess.Env.Metadata.MediaType = "movie"
	h := episodeTestHandler(t, episodeTestClient(t, func(string) (string, float64) {
		t.Error("movie sent to episode classifier")
		return "none", 1
	}))
	h.osClient = nil
	if err := h.Run(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if sess.Env.Attributes.ContentID != nil || sess.Env.Episodes[0].NeedsReview {
		t.Fatal("movie acquired TV provenance/review")
	}
	root, err := sess.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "contentid")); !os.IsNotExist(err) {
		t.Fatalf("movie created reference staging: %v", err)
	}
}
