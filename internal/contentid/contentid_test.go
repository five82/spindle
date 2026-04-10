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
		Metadata: ripspec.Metadata{DiscNumber: 2},
		Episodes: []ripspec.Episode{{Key: "s03_001", Season: 3}, {Key: "s03_002", Season: 3}},
		Assets:   ripspec.Assets{Ripped: []ripspec.Asset{{EpisodeKey: "s03_001", Path: "/rip/1.mkv", Status: ripspec.AssetStatusCompleted}, {EpisodeKey: "s03_002", Path: "/rip/2.mkv", Status: ripspec.AssetStatusCompleted}}},
	}
	season := &tmdb.Season{Episodes: []tmdb.Episode{{EpisodeNumber: 3, Name: "Three"}, {EpisodeNumber: 4, Name: "Four"}}}
	h.applyMatches(logger, env, 3, season, []matchResult{{EpisodeKey: "s03_001", TargetEpisode: 3, Score: 0.91}, {EpisodeKey: "s03_002", TargetEpisode: 4, Score: 0.88}}, nil)
	if env.Episodes[0].Key != "s03e03" || env.Episodes[1].Key != "s03e04" {
		t.Fatalf("episode keys not remapped: %+v", env.Episodes)
	}
	if _, ok := env.Assets.FindAsset(ripspec.AssetKindRipped, "s03e03"); !ok {
		t.Fatal("ripped asset for s03e03 not found after remap")
	}
}

func TestApplyMatches_InfersOpeningDoubleEpisode(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{policy: DefaultPolicy()}
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{DiscNumber: 1},
		Episodes: []ripspec.Episode{
			{Key: "s01_001", Season: 1, RuntimeSeconds: 91 * 60},
			{Key: "s01_002", Season: 1, RuntimeSeconds: 45 * 60},
			{Key: "s01_003", Season: 1, RuntimeSeconds: 45 * 60},
		},
		Assets: ripspec.Assets{Ripped: []ripspec.Asset{
			{EpisodeKey: "s01_001", Path: "/rip/1.mkv", Status: ripspec.AssetStatusCompleted},
			{EpisodeKey: "s01_002", Path: "/rip/2.mkv", Status: ripspec.AssetStatusCompleted},
			{EpisodeKey: "s01_003", Path: "/rip/3.mkv", Status: ripspec.AssetStatusCompleted},
		}},
	}
	season := &tmdb.Season{Episodes: []tmdb.Episode{{EpisodeNumber: 1, Name: "Pilot Part 1"}, {EpisodeNumber: 2, Name: "Pilot Part 2"}, {EpisodeNumber: 3, Name: "Third"}, {EpisodeNumber: 4, Name: "Fourth"}}}
	h.applyMatches(logger, env, 1, season, []matchResult{{EpisodeKey: "s01_001", TargetEpisode: 1, Score: 0.91}, {EpisodeKey: "s01_002", TargetEpisode: 2, Score: 0.88}, {EpisodeKey: "s01_003", TargetEpisode: 3, Score: 0.89}}, nil)
	if env.Episodes[0].Key != "s01e01-e02" || env.Episodes[0].Episode != 1 || env.Episodes[0].EpisodeEnd != 2 {
		t.Fatalf("opening episode not converted to range: %+v", env.Episodes[0])
	}
	if env.Episodes[1].Episode != 3 || env.Episodes[2].Episode != 4 {
		t.Fatalf("later episodes not shifted: %+v", env.Episodes)
	}
	if _, ok := env.Assets.FindAsset(ripspec.AssetKindRipped, "s01e01-e02"); !ok {
		t.Fatal("ripped asset for s01e01-e02 not found after remap")
	}
}

func TestShouldVerifyMatchUsesDerivedConfidenceAndAmbiguity(t *testing.T) {
	if !shouldVerifyMatch(matchResult{EpisodeKey: "s01_002", Confidence: 0.95, NeedsVerification: true}, DefaultPolicy().LLMVerifyThreshold) {
		t.Fatal("expected ambiguity-flagged high-confidence match to be verified")
	}
	if shouldVerifyMatch(matchResult{EpisodeKey: "s01_003", Confidence: 0.95}, DefaultPolicy().LLMVerifyThreshold) {
		t.Fatal("unexpected verification for clear high-confidence match")
	}
	if !shouldVerifyMatch(matchResult{EpisodeKey: "s01_003", Confidence: 0.60}, DefaultPolicy().LLMVerifyThreshold) {
		t.Fatal("expected low-confidence match to be verified")
	}
}

func TestDeriveMatchConfidencePenalizesAdjacentAmbiguity(t *testing.T) {
	confidence, _, needsVerify, _ := deriveMatchConfidence(0.93, 0.01, 0.01, 0.005, 0.03, orderedPath{}, DefaultPolicy())
	if confidence >= 0.90 {
		t.Fatalf("expected ambiguous high-score match confidence < 0.90, got %.3f", confidence)
	}
	if !needsVerify {
		t.Fatal("expected ambiguous neighboring match to require verification")
	}
}

func TestDecodeOrderedEpisodeMatchesChoosesContiguousForwardWindow(t *testing.T) {
	policy := DefaultPolicy()
	rips := []ripFingerprint{
		{EpisodeKey: "s01_001", TitleID: 1, Vector: textutil.NewFingerprint("outpost alpha beta gamma unique four")},
		{EpisodeKey: "s01_002", TitleID: 2, Vector: textutil.NewFingerprint("where delta epsilon zeta unique five")},
		{EpisodeKey: "s01_003", TitleID: 3, Vector: textutil.NewFingerprint("lonely eta theta iota unique six")},
	}
	refs := []referenceFingerprint{
		{EpisodeNumber: 3, Vector: textutil.NewFingerprint("different episode three text")},
		{EpisodeNumber: 4, Vector: textutil.NewFingerprint("outpost alpha beta gamma unique four")},
		{EpisodeNumber: 5, Vector: textutil.NewFingerprint("where delta epsilon zeta unique five")},
		{EpisodeNumber: 6, Vector: textutil.NewFingerprint("lonely eta theta iota unique six")},
		{EpisodeNumber: 7, Vector: textutil.NewFingerprint("different episode seven text")},
	}
	matches, diag := decodeOrderedEpisodeMatches(rips, refs, 2, 26, policy)
	if len(matches) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(matches))
	}
	if diag.WindowStart != 4 || diag.WindowEnd != 6 {
		t.Fatalf("window = %d-%d, want 4-6", diag.WindowStart, diag.WindowEnd)
	}
	got := make(map[string]int, len(matches))
	for _, m := range matches {
		got[m.EpisodeKey] = m.TargetEpisode
	}
	want := map[string]int{"s01_001": 4, "s01_002": 5, "s01_003": 6}
	for key, episode := range want {
		if got[key] != episode {
			t.Fatalf("%s matched to E%02d, want E%02d", key, got[key], episode)
		}
	}
}
