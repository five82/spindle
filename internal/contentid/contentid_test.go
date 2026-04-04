package contentid

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/textutil"
	"github.com/five82/spindle/internal/tmdb"
)

func TestReadSRTText(t *testing.T) {
	content := `1
00:00:01,000 --> 00:00:03,000
Hello world.

2
00:00:04,000 --> 00:00:06,000
This is a test.
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.srt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readSRTText(path)
	want := "Hello world. This is a test."
	if got != want {
		t.Fatalf("readSRTText got %q want %q", got, want)
	}
}

func TestHungarianIdentityMatrix(t *testing.T) {
	cost := [][]float64{{0, 1, 1}, {1, 0, 1}, {1, 1, 0}}
	assign := hungarian(cost)
	for i, col := range assign {
		if col != i {
			t.Fatalf("row %d assigned %d, want %d", i, col, i)
		}
	}
}

func TestSelectAnchorWindowFirstAnchor(t *testing.T) {
	rips := []ripFingerprint{
		{EpisodeKey: "s02_001", Vector: textutil.NewFingerprint("batman villain puzzler episode twenty five marker")},
		{EpisodeKey: "s02_002", Vector: textutil.NewFingerprint("robin riddle episode twenty six marker")},
		{EpisodeKey: "s02_003", Vector: textutil.NewFingerprint("alfred cave episode twenty seven marker")},
	}
	refs := []referenceFingerprint{
		{EpisodeNumber: 24, Vector: textutil.NewFingerprint("different content episode twenty four")},
		{EpisodeNumber: 25, Vector: textutil.NewFingerprint("batman villain puzzler episode twenty five marker")},
		{EpisodeNumber: 26, Vector: textutil.NewFingerprint("robin riddle episode twenty six marker")},
	}
	anchor, ok := selectAnchorWindow(rips, refs, 40, DefaultPolicy().AnchorMinScore, DefaultPolicy().AnchorMinScoreMargin)
	if !ok {
		t.Fatalf("expected anchor selection to succeed, got reason=%q", anchor.Reason)
	}
	if anchor.WindowStart != 25 || anchor.WindowEnd != 27 {
		t.Fatalf("window = %d-%d, want 25-27", anchor.WindowStart, anchor.WindowEnd)
	}
}

func TestResolveEpisodeMatchesOptimalAssignment(t *testing.T) {
	rip1 := textutil.NewFingerprint("alpha beta gamma intro theme park")
	rip2 := textutil.NewFingerprint("delta epsilon zeta magic heroes rescue mission")
	rips := []ripFingerprint{
		{EpisodeKey: "s05e01", TitleID: 1, Vector: rip1},
		{EpisodeKey: "s05e02", TitleID: 2, Vector: rip2},
	}
	refs := []referenceFingerprint{
		{EpisodeNumber: 2, Vector: textutil.NewFingerprint("delta epsilon zeta heroes rescue mission magic")},
		{EpisodeNumber: 1, Vector: textutil.NewFingerprint("alpha beta gamma park theme opening")},
		{EpisodeNumber: 3, Vector: textutil.NewFingerprint("alpha beta gamma park magic heroes rescue mission")},
	}
	matches := resolveEpisodeMatches(rips, refs, DefaultPolicy().MinSimilarityScore)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	want := map[string]int{"s05e01": 1, "s05e02": 2}
	for _, m := range matches {
		if want[m.EpisodeKey] != m.TargetEpisode {
			t.Fatalf("unexpected match: %+v", m)
		}
	}
}

func TestBuildContentIDSummary(t *testing.T) {
	env := &ripspec.Envelope{Episodes: []ripspec.Episode{
		{Key: "s01e01", Episode: 1, MatchConfidence: 0.91},
		{Key: "s01e02", Episode: 2, MatchConfidence: 0.64, NeedsReview: true},
		{Key: "s01_003", Episode: 0, NeedsReview: true},
	}}
	summary := buildContentIDSummary(env, []matchResult{{EpisodeKey: "s01_001", TargetEpisode: 1, Score: 0.91}, {EpisodeKey: "s01_002", TargetEpisode: 2, Score: 0.64}}, 3, 4, DefaultPolicy().LowConfidenceReviewThreshold)
	if summary == nil {
		t.Fatal("summary = nil")
	}
	if summary.MatchedEpisodes != 2 || summary.UnresolvedEpisodes != 1 {
		t.Fatalf("matched/unresolved = %d/%d", summary.MatchedEpisodes, summary.UnresolvedEpisodes)
	}
	if summary.LowConfidenceCount != 1 {
		t.Fatalf("LowConfidenceCount = %d, want 1", summary.LowConfidenceCount)
	}
}

func TestApplyMatchesRemapsAssetKeys(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{policy: DefaultPolicy()}
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{{Key: "s03_001", Season: 3}, {Key: "s03_002", Season: 3}},
		Assets:   ripspec.Assets{Ripped: []ripspec.Asset{{EpisodeKey: "s03_001", Path: "/rip/1.mkv", Status: "completed"}, {EpisodeKey: "s03_002", Path: "/rip/2.mkv", Status: "completed"}}},
	}
	season := &tmdb.Season{Episodes: []tmdb.Episode{{EpisodeNumber: 3, Name: "Three"}, {EpisodeNumber: 4, Name: "Four"}}}
	h.applyMatches(logger, env, 3, season, []matchResult{{EpisodeKey: "s03_001", TargetEpisode: 3, Score: 0.91}, {EpisodeKey: "s03_002", TargetEpisode: 4, Score: 0.88}}, nil)
	if env.Episodes[0].Key != "s03e03" || env.Episodes[1].Key != "s03e04" {
		t.Fatalf("episode keys not remapped: %+v", env.Episodes)
	}
	if _, ok := env.Assets.FindAsset("ripped", "s03e03"); !ok {
		t.Fatal("ripped asset for s03e03 not found after remap")
	}
}
