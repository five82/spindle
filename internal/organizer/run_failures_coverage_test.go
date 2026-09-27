package organizer

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func TestRunReportsMissingDeliveryAssetsBeforeFinalizing(t *testing.T) {
	root := t.TempDir()
	store, err := queue.Open(filepath.Join(root, "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	item, err := store.NewDisc("Example", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	item.MetadataJSON = `{"title":"Example","show_title":"Example","media_type":"movie"}`
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Paths: config.PathsConfig{LibraryDir: filepath.Join(root, "library"), ReviewDir: filepath.Join(root, "review"), StagingDir: filepath.Join(root, "staging")}, Library: config.LibraryConfig{MoviesDir: "Movies", TVDir: "TV"}}
	h := New(cfg, nil, nil)
	for _, tc := range []struct {
		name, media string
		review      bool
		episodes    []ripspec.Episode
	}{
		{name: "movie library", media: "movie"},
		{name: "movie review", media: "movie", review: true},
		{name: "TV all review", media: "tv", review: true, episodes: []ripspec.Episode{{Key: "s01e01", Season: 1, Episode: 1, NeedsReview: true}}},
		{name: "TV split", media: "tv", review: true, episodes: []ripspec.Episode{{Key: "s01e01", Season: 1, Episode: 1}, {Key: "s01e02", Season: 1, Episode: 2, NeedsReview: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item.NeedsReview = 0
			if tc.review {
				item.NeedsReview = 1
			}
			sess.SetEnvelope(&ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: tc.media}, Episodes: tc.episodes})
			if err := h.Run(context.Background(), sess); err == nil || !strings.Contains(err.Error(), "no completed subtitled or encoded asset") {
				t.Fatalf("missing delivery asset: %v", err)
			}
			if len(sess.Env.Assets.Final) != 0 {
				t.Fatalf("no assets should be finalized: %+v", sess.Env.Assets.Final)
			}
		})
	}
}
