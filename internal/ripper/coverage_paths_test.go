package ripper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func ripCoverageSession(t *testing.T, env ripspec.Envelope) *stage.Session {
	t.Helper()
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Disc", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	env.Version = ripspec.CurrentVersion
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
	return sess
}

func TestMapAndValidateAssetsMissingAndInvalid(t *testing.T) {
	for _, tc := range []struct {
		name, media, file, want string
		episodes                []ripspec.Episode
		failed                  int
	}{
		{"no tv matches", "tv", "", "zero matches", []ripspec.Episode{{Key: "one", TitleID: 1}}, 0},
		{"all tv invalid", "tv", "title_t01.mkv", "all 1 ripped episodes failed validation", []ripspec.Episode{{Key: "one", TitleID: 1}}, 1},
		{"partial tv invalid", "tv", "title_t01.mkv", "", []ripspec.Episode{{Key: "one", TitleID: 1}, {Key: "two", TitleID: 2}, {Key: "three", TitleID: 3}}, 2},
		{"movie invalid", "movie", "movie_t01.mkv", "ripped artifact invalid", nil, 0},
		{"movie absent directory", "movie", "", "asset mapping: read dir", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := ripCoverageSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: tc.media}, Episodes: tc.episodes})
			dir := filepath.Join(t.TempDir(), "rips")
			if tc.name != "movie absent directory" {
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.file != "" {
				if err := os.WriteFile(filepath.Join(dir, tc.file), []byte("too small"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "partial tv invalid" {
				bin := t.TempDir()
				probe := "#!/bin/sh\nprintf '%s\\n' '{\"streams\":[{\"codec_type\":\"video\"},{\"codec_type\":\"audio\"}],\"format\":{\"duration\":\"120\"}}'\n"
				if err := os.WriteFile(filepath.Join(bin, "ffprobe"), []byte(probe), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				f, err := os.Create(filepath.Join(dir, "title_t02.mkv"))
				if err != nil {
					t.Fatal(err)
				}
				if err := f.Truncate(minRipFileSizeBytes + 1); err != nil {
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
			h := &Handler{}
			err := h.mapAndValidateAssets(context.Background(), testLogger(), sess, dir, nil)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("map error: %v; want %q", err, tc.want)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(sess.Env.Assets.Ripped) != tc.failed && tc.name != "movie invalid" {
				t.Fatalf("assets: %+v", sess.Env.Assets.Ripped)
			}
			if tc.failed > 0 && !sess.Env.Assets.Ripped[0].IsFailed() {
				t.Fatalf("invalid episode not marked failed: %+v", sess.Env.Assets.Ripped)
			}
			if tc.name == "partial tv invalid" {
				if !strings.Contains(strings.Join(sess.Item.ReviewReasons(), " "), "missing 1 episode") {
					t.Fatalf("missing review: %v", sess.Item.ReviewReasons())
				}
				if !sess.Env.Episodes[0].NeedsReview || !sess.Env.Episodes[2].NeedsReview || sess.Env.Episodes[1].NeedsReview {
					t.Fatalf("episode reviews: %+v", sess.Env.Episodes)
				}
			}
		})
	}
}

func TestRipCacheIncompleteAndStats(t *testing.T) {
	env := ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "tv"}, Episodes: []ripspec.Episode{{Key: "one", TitleID: 1}, {Key: "two", TitleID: 2}}}
	sess := ripCoverageSession(t, env)
	dir := t.TempDir()
	cache := ripcache.New(t.TempDir(), 1)
	h := New(&config.Config{MakeMKV: config.MakeMKVConfig{OpticalDrive: "disc:0"}}, nil, cache, nil, NoTitleOverride)
	if restored, err := h.restoreFromRipCache(context.Background(), sess, dir); restored || err != nil {
		t.Fatalf("cache miss: %t %v", restored, err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "show_t01.mkv"), []byte("incomplete"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cache.Register(sess.Item.DiscFingerprint, src, nil); err != nil {
		t.Fatal(err)
	}
	if err := cache.WriteMetadata(sess.Item.DiscFingerprint, ripcache.EntryMetadata{TitleCount: 1, TotalBytes: 10}); err != nil {
		t.Fatal(err)
	}
	if restored, err := h.restoreFromRipCache(context.Background(), sess, dir); restored || err != nil {
		t.Fatalf("incomplete cache: %t %v", restored, err)
	}
	h.recordRipStats(testLogger(), sess, dir, 2, 3.5)
	fresh, err := sess.Store.GetByID(sess.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ripspec.Parse(fresh.RipSpecData)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Attributes.Rip == nil || saved.Attributes.Rip.Bytes != 10 || saved.Attributes.Rip.Titles != 2 || saved.Attributes.Rip.Seconds != 3.5 {
		t.Fatalf("rip stats: %+v", saved.Attributes.Rip)
	}
}

func TestRipTitlesResumeAndCancelled(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "show_t01.mkv")
	if err := os.WriteFile(file, []byte("preserved"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := ripCoverageSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "tv"}, Episodes: []ripspec.Episode{{Key: "one", TitleID: 1}}})
	sess.Env.Assets.AddAsset(ripspec.AssetKindRipped, ripspec.Asset{EpisodeKey: "one", TitleID: 1, Path: file, Status: ripspec.AssetStatusCompleted})
	h := &Handler{}
	if err := h.ripTitles(context.Background(), sess, dir, []ripspec.Title{{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "preserved" {
		t.Fatalf("resume overwrote rip: %q %v", data, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.ripTitles(ctx, sess, dir, []ripspec.Title{{ID: 1}}); err != context.Canceled {
		t.Fatalf("cancel: %v", err)
	}
}
