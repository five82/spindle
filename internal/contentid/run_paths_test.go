package contentid

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/tmdb"
	"github.com/five82/spindle/internal/transcription"
)

func TestRunSeasonAcquisitionAndNoTranscripts(t *testing.T) {
	for _, tc := range []struct {
		name, response, want string
		status               int
		degraded             bool
	}{
		{"lookup failed", `{}`, "tmdb season acquisition", 400, false},
		{"empty season", `{"episodes":[]}`, "season contains no episodes", 200, true},
		{"unripped episode", `{"episodes":[{"episode_number":1,"name":"Pilot"}]}`, "no valid transcriptions", 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/subtitles" {
					_, _ = w.Write([]byte(`{"data":[]}`))
					return
				}
				if r.URL.Path != "/tv/42/season/1" {
					t.Errorf("unexpected TMDB path: %s", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.response))
			}))
			defer srv.Close()
			store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			item, err := store.NewDisc("Show", "fp")
			if err != nil {
				t.Fatal(err)
			}
			env := ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "tv", ID: 42}, Episodes: []ripspec.Episode{{Key: "one", TitleID: 1}}}
			item.RipSpecData, err = env.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateWorkState(item); err != nil {
				t.Fatal(err)
			}
			sess, err := stage.NewSession(context.Background(), store, item, nil)
			if err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}}
			h := New(cfg, nil, opensubtitles.New(opensubtitles.Params{APIKey: "test", BaseURL: srv.URL}, nil), tmdb.New("test", srv.URL, "en-US", nil), transcription.New(transcription.Params{}, nil))
			err = h.Run(context.Background(), sess)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run: %v, want %s", err, tc.want)
			}
			var degraded *stage.ErrDegraded
			if errors.As(err, &degraded) != tc.degraded {
				t.Fatalf("degraded error: %v", err)
			}
			if tc.degraded {
				fresh, err := store.GetByID(item.ID)
				if err != nil {
					t.Fatal(err)
				}
				saved, err := ripspec.Parse(fresh.RipSpecData)
				if err != nil {
					t.Fatal(err)
				}
				if saved.Attributes.ContentID == nil || saved.Attributes.ContentID.Completed {
					t.Fatalf("degraded summary: %+v", saved.Attributes.ContentID)
				}
			}
		})
	}
}
