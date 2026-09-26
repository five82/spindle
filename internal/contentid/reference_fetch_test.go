package contentid

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/tmdb"
)

func TestFetchReferencesDownloadAndReuse(t *testing.T) {
	var searches, downloads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subtitles":
			searches++
			if r.URL.Query().Get("episode_number") != "1" {
				t.Errorf("episode: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []opensubtitles.SubtitleResult{{ID: "one", Attributes: opensubtitles.SubtitleAttributes{Language: "en", Release: "Show S01E01 Pilot", Files: []opensubtitles.SubtitleFile{{FileID: 17, FileName: "Show.S01E01.Pilot.srt"}}}}}})
		case "/download":
			downloads++
			_ = json.NewEncoder(w).Encode(map[string]any{"link": "http://" + r.Host + "/file.srt"})
		case "/file.srt":
			_, _ = w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nThe pilot lands safely.\n"))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}}, nil, opensubtitles.New(opensubtitles.Params{APIKey: "test", BaseURL: srv.URL}, nil), nil, nil)
	item := &queue.Item{ID: 4, DiscFingerprint: "fp"}
	season := &tmdb.Season{Episodes: []tmdb.Episode{{EpisodeNumber: 1, Name: "Pilot"}}}
	cache := make(map[int]referenceFingerprint)
	refs, err := h.fetchReferenceFingerprints(context.Background(), nil, item, 1, 42, season, []int{1, 1}, cache)
	if err != nil || len(refs) != 1 || refs[0].FileID != 17 || refs[0].Vector == nil {
		t.Fatalf("fetch: %+v %v", refs, err)
	}
	if _, err := os.Stat(refs[0].CachePath); err != nil {
		t.Fatalf("reference file: %v", err)
	}
	again, err := h.fetchReferenceFingerprints(context.Background(), nil, item, 1, 42, season, []int{1}, cache)
	if err != nil || len(again) != 1 || again[0].CachePath != refs[0].CachePath || searches != 1 || downloads != 1 {
		t.Fatalf("cache reuse: %+v %v, searches=%d downloads=%d", again, err, searches, downloads)
	}
}

func TestFetchReferencesRequiresClient(t *testing.T) {
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}}, nil, nil, nil, nil)
	_, err := h.fetchReferenceFingerprints(context.Background(), nil, &queue.Item{ID: 4}, 1, 42, nil, []int{1}, nil)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("missing client: %v", err)
	}
}
