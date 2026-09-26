package contentid

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/textutil"
	"github.com/five82/spindle/internal/tmdb"
)

func TestMatchEpisodesPersistsClaimsAndReviewsExtra(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Show", "fp")
	if err != nil {
		t.Fatal(err)
	}
	env := ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "tv", SeasonNumber: 1}, Episodes: []ripspec.Episode{{Key: "one", TitleID: 1}, {Key: "extra", TitleID: 2}}}
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
	h := New(&config.Config{}, nil, nil, nil, nil)
	text := "unique red balloon travels across a deep blue ocean"
	ref := textutil.NewFingerprint(text)
	ripPrints := []ripFingerprint{{EpisodeKey: "one", TitleID: 1, Vector: ref, RawVector: ref}, {EpisodeKey: "extra", TitleID: 2, Vector: textutil.NewFingerprint("zzyzx quibbling blorf plonk gribble wonkabar"), RawVector: textutil.NewFingerprint("zzyzx quibbling blorf plonk gribble wonkabar")}}
	refs := []referenceFingerprint{{EpisodeNumber: 1, Title: "Pilot", Vector: ref, RawVector: ref}}
	season := &tmdb.Season{Episodes: []tmdb.Episode{{EpisodeNumber: 1, Name: "Pilot"}}}
	plan := candidateEpisodePlan{InitialEpisodes: []int{1}, ExpandedEpisodes: []int{1}}
	if err := h.matchEpisodes(context.Background(), sess, sess.Env, season, 1, plan, ripPrints, refs, nil); err != nil {
		t.Fatal(err)
	}
	fresh, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ripspec.Parse(fresh.RipSpecData)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Episodes[0].Episode != 1 || saved.Attributes.ContentID == nil || saved.Attributes.ContentID.MatchedEpisodes != 1 {
		t.Fatalf("matched result: %+v", saved)
	}
	if !saved.Episodes[1].NeedsReview {
		t.Fatalf("extra not flagged: %+v", saved.Episodes[1])
	}
}

func TestFetchReferenceFingerprintsCachedAndMissing(t *testing.T) {
	cfg := &config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}}
	h := New(cfg, nil, opensubtitles.New(opensubtitles.Params{APIKey: "test"}, nil), nil, nil)
	item := &queue.Item{ID: 7, DiscFingerprint: "fp"}
	if _, err := h.fetchReferenceFingerprints(context.Background(), nil, item, 1, 1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "reference.srt")
	if err := os.WriteFile(path, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello there.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cached := referenceFingerprint{EpisodeNumber: 4, CachePath: path, Vector: textutil.NewFingerprint("hello there")}
	refs, err := h.fetchReferenceFingerprints(context.Background(), nil, item, 1, 1, nil, []int{4, 4}, map[int]referenceFingerprint{4: cached})
	if err != nil || len(refs) != 1 || refs[0].EpisodeNumber != 4 {
		t.Fatalf("cached references: %+v %v", refs, err)
	}
	if _, err := loadPlainText(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("missing subtitle accepted")
	}
	if _, err := normalizeSubtitlePayload("1\n00:00:01,000 --> 00:00:02,000\n\n"); err == nil || !strings.Contains(err.Error(), "no text") {
		t.Fatalf("empty subtitle: %v", err)
	}
	text, err := normalizeSubtitlePayload("1\n00:00:01,000 --> 00:00:02,000\nHello there.\n")
	if err != nil || text != "Hello there." {
		t.Fatalf("normalized: %q %v", text, err)
	}
}
