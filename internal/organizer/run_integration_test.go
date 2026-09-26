package organizer

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/mediameta"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

// Exercise delivery with a real queue session and small files: the final
// asset record must agree with the actual routing, including a mixed TV disc.
func TestRunRoutesCompletedAssets(t *testing.T) {
	for _, tc := range []struct {
		name, media string
		review      bool
		episodes    []ripspec.Episode
		wantReview  []bool
	}{
		{name: "movie library", media: "movie", wantReview: []bool{false}},
		{name: "movie review", media: "movie", review: true, wantReview: []bool{true}},
		{name: "tv split", media: "tv", review: true, episodes: []ripspec.Episode{{Key: "s01e01", Season: 1, Episode: 1}, {Key: "s01e02", Season: 1, Episode: 2, NeedsReview: true}}, wantReview: []bool{false, true}},
		{name: "tv all review", media: "tv", review: true, episodes: []ripspec.Episode{{Key: "s01e01", Season: 1, Episode: 1, NeedsReview: true}}, wantReview: []bool{true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := &config.Config{Paths: config.PathsConfig{LibraryDir: filepath.Join(root, "library"), ReviewDir: filepath.Join(root, "review"), StagingDir: filepath.Join(root, "staging")}, Library: config.LibraryConfig{MoviesDir: "Movies", TVDir: "TV"}}
			store, err := queue.Open(filepath.Join(root, "queue.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			item, err := store.NewDisc("Example", "fingerprint1234")
			if err != nil {
				t.Fatal(err)
			}
			if tc.review {
				item.NeedsReview = 1
				item.ReviewReason = "needs inspection"
				if err := store.UpdateWorkState(item); err != nil {
					t.Fatal(err)
				}
			}
			item.MetadataJSON = `{"title":"Example","show_title":"Example","media_type":"` + tc.media + `","year":"2020","season_number":1}`
			if err := store.UpdateWorkState(item); err != nil {
				t.Fatal(err)
			}
			sess, err := stage.NewSession(context.Background(), store, item, nil)
			if err != nil {
				t.Fatal(err)
			}
			env := &ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: tc.media}, Episodes: tc.episodes}
			keys := env.AssetKeys()
			for i, key := range keys {
				src := filepath.Join(root, key+".mkv")
				if err := os.WriteFile(src, []byte("video "+key), 0o644); err != nil {
					t.Fatal(err)
				}
				env.Assets.Encoded = append(env.Assets.Encoded, ripspec.Asset{EpisodeKey: key, Path: src, Status: ripspec.AssetStatusCompleted})
				if !tc.wantReview[i] && i == 0 {
					if err := os.WriteFile(strings.TrimSuffix(src, ".mkv")+".en.srt", []byte("subtitle"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			sess.SetEnvelope(env)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			sess.Logger = logger
			if err := New(cfg, nil, nil).Run(context.Background(), sess); err != nil {
				t.Fatal(err)
			}
			for i, key := range keys {
				final, ok := sess.Env.Assets.FindAsset(ripspec.AssetKindFinal, key)
				if !ok || !final.IsCompleted() {
					t.Fatalf("missing final asset for %s: %+v", key, final)
				}
				expected := cfg.Paths.LibraryDir
				if tc.wantReview[i] {
					expected = cfg.Paths.ReviewDir
				}
				if !pathWithinRoot(final.Path, expected) {
					t.Errorf("%s routed to %s, expected under %s", key, final.Path, expected)
				}
				if b, err := os.ReadFile(final.Path); err != nil || string(b) != "video "+key {
					t.Errorf("delivered %s: %q %v", key, b, err)
				}
				if tc.wantReview[i] {
					if _, err := os.Stat(filepath.Join(root, key+".mkv")); !os.IsNotExist(err) {
						t.Errorf("review source not moved: %v", err)
					}
				}
				if !tc.wantReview[i] && i == 0 {
					if b, err := os.ReadFile(strings.TrimSuffix(final.Path, ".mkv") + ".en.srt"); err != nil || string(b) != "subtitle" {
						t.Errorf("sidecar: %q %v", b, err)
					}
				}
			}
			if tc.media == "movie" && !tc.review {
				// An existing complete delivery is retained when overwrite is disabled.
				meta := mediameta.FromJSON(item.MetadataJSON, item.DiscTitle)
				if _, copied, err := New(cfg, nil, nil).copyAssetsToDir(context.Background(), logger, sess, &meta, filepath.Dir(sess.Env.Assets.Final[0].Path), keys, "library"); err != nil || copied != 0 {
					t.Fatalf("existing destination: copied=%d err=%v", copied, err)
				}
			}
		})
	}
}
