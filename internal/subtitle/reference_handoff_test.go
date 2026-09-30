package subtitle

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/srtutil"
)

func TestContentIDReferenceStillVerifiedWhenSearchFails(t *testing.T) {
	for _, partial := range []bool{false, true} {
		name := "complete reference adopted"
		if partial {
			name = "matching excerpt is not a display subtitle"
		}
		t.Run(name, func(t *testing.T) {
			stubAdoptEnvironment(t, "2000")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/subtitles" {
					t.Errorf("cached reference caused a download: %s", r.URL.Path)
				}
				http.Error(w, "search unavailable", 400)
			}))
			defer server.Close()
			h := &Handler{
				cfg:      &config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}, Subtitles: config.SubtitlesConfig{Enabled: true}},
				osClient: opensubtitles.New(opensubtitles.Params{APIKey: "test", BaseURL: server.URL}, discardLogger()),
			}
			reference := dialogueCues(199, 10, 10)
			candidate := reference
			if partial {
				candidate = reference[85:115]
			}
			sess, job := newAdoptSession(t, h, reference, []byte(srtutil.Format(candidate)))
			outcome, err := h.processSubtitleJob(context.Background(), sess, job)
			if err != nil {
				t.Fatal(err)
			}
			want := subtitleOutcomeAdopted
			if partial {
				want = subtitleOutcomeSkipped
			}
			if outcome != want {
				t.Fatalf("reference handoff outcome = %v, want %v", outcome, want)
			}
		})
	}
}
