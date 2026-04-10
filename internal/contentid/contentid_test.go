package contentid

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/opensubtitles"
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

func TestSelectReferenceCandidatePrefersSpecificEpisodeReleaseOverSeasonPack(t *testing.T) {
	season := &tmdb.Season{Episodes: []tmdb.Episode{
		{EpisodeNumber: 4, Name: "The Last Outpost"},
		{EpisodeNumber: 5, Name: "Where No One Has Gone Before"},
		{EpisodeNumber: 6, Name: "Lonely Among Us"},
		{EpisodeNumber: 7, Name: "Justice"},
	}}
	results := []opensubtitles.SubtitleResult{
		{
			ID: "season-pack",
			Attributes: opensubtitles.SubtitleAttributes{
				Release:       "Star Trek TNG S01E01-06",
				DownloadCount: 27788,
				Files:         []opensubtitles.SubtitleFile{{FileID: 1, FileName: "StarTrek_TNG_S01E05"}},
			},
		},
		{
			ID: "specific-release",
			Attributes: opensubtitles.SubtitleAttributes{
				Release:       "Star Trek TNG S01E05 Where No One Has Gone Before DVD NonHI",
				DownloadCount: 643,
				Files:         []opensubtitles.SubtitleFile{{FileID: 2, FileName: "Star Trek TNG S01E05 Where No One Has Gone Before.srt"}},
			},
		},
	}
	choice := selectReferenceCandidate(results, season, 1, 5)
	if choice.Result == nil {
		t.Fatal("choice.Result = nil")
	}
	if choice.Result.ID != "specific-release" {
		t.Fatalf("selected %q, want %q", choice.Result.ID, "specific-release")
	}
	if choice.Suspect {
		t.Fatal("specific release should not be suspect")
	}
}

func TestSelectReferenceCandidateRejectsConflictingEpisodeTitle(t *testing.T) {
	season := &tmdb.Season{Episodes: []tmdb.Episode{
		{EpisodeNumber: 6, Name: "Lonely Among Us"},
		{EpisodeNumber: 7, Name: "Justice"},
	}}
	results := []opensubtitles.SubtitleResult{
		{
			ID: "wrong-title",
			Attributes: opensubtitles.SubtitleAttributes{
				Release:       "Star Trek The Next Generation S01 AC3 DVDRip DivX AMC",
				DownloadCount: 30703,
				Files:         []opensubtitles.SubtitleFile{{FileID: 1, FileName: "Star Trek - The Next Generation - 1.07 - Lonely Among Us"}},
			},
		},
		{
			ID: "right-title",
			Attributes: opensubtitles.SubtitleAttributes{
				Release:       "Star Trek TNG S01E07 Justice DVD NonHI",
				DownloadCount: 234,
				Files:         []opensubtitles.SubtitleFile{{FileID: 2, FileName: "Star Trek TNG S01E07 Justice.srt"}},
			},
		},
	}
	choice := selectReferenceCandidate(results, season, 1, 7)
	if choice.Result == nil {
		t.Fatal("choice.Result = nil")
	}
	if choice.Result.ID != "right-title" {
		t.Fatalf("selected %q, want %q", choice.Result.ID, "right-title")
	}
}

func TestSelectReferenceCandidateMarksSuspectWhenNoGoodFallbackExists(t *testing.T) {
	season := &tmdb.Season{Episodes: []tmdb.Episode{{EpisodeNumber: 5, Name: "Where No One Has Gone Before"}}}
	results := []opensubtitles.SubtitleResult{
		{
			ID: "season-pack-only",
			Attributes: opensubtitles.SubtitleAttributes{
				Release:       "Star Trek TNG S01E01-10",
				DownloadCount: 5000,
				Files:         []opensubtitles.SubtitleFile{{FileID: 1, FileName: "Star Trek TNG S01E01-10 pack.srt"}},
			},
		},
	}
	choice := selectReferenceCandidate(results, season, 1, 5)
	if choice.Result == nil {
		t.Fatal("choice.Result = nil")
	}
	if !choice.Suspect {
		t.Fatal("expected single season-pack candidate to be suspect")
	}
}

func TestResolveEpisodeClaimsIgnoresDiscOrder(t *testing.T) {
	policy := DefaultPolicy()
	rips := []ripFingerprint{
		{EpisodeKey: "s01_001", TitleID: 1, Vector: textutil.NewFingerprint("justice edo rubicun wesley shore leave unique seven"), RawVector: textutil.NewFingerprint("justice edo rubicun wesley shore leave unique seven")},
		{EpisodeKey: "s01_002", TitleID: 2, Vector: textutil.NewFingerprint("battle ferengi bok stargazer unique eight"), RawVector: textutil.NewFingerprint("battle ferengi bok stargazer unique eight")},
		{EpisodeKey: "s01_003", TitleID: 3, Vector: textutil.NewFingerprint("lonely among us antikans selay unique six"), RawVector: textutil.NewFingerprint("lonely among us antikans selay unique six")},
		{EpisodeKey: "s01_004", TitleID: 4, Vector: textutil.NewFingerprint("where no one kosinski traveler unique five"), RawVector: textutil.NewFingerprint("where no one kosinski traveler unique five")},
		{EpisodeKey: "s01_005", TitleID: 5, Vector: textutil.NewFingerprint("last outpost ferengi portal tkon unique four"), RawVector: textutil.NewFingerprint("last outpost ferengi portal tkon unique four")},
	}
	refs := []referenceFingerprint{
		{EpisodeNumber: 4, Vector: textutil.NewFingerprint("last outpost ferengi portal tkon unique four"), RawVector: textutil.NewFingerprint("last outpost ferengi portal tkon unique four")},
		{EpisodeNumber: 5, Vector: textutil.NewFingerprint("where no one kosinski traveler unique five"), RawVector: textutil.NewFingerprint("where no one kosinski traveler unique five")},
		{EpisodeNumber: 6, Vector: textutil.NewFingerprint("lonely among us antikans selay unique six"), RawVector: textutil.NewFingerprint("lonely among us antikans selay unique six")},
		{EpisodeNumber: 7, Vector: textutil.NewFingerprint("justice edo rubicun wesley shore leave unique seven"), RawVector: textutil.NewFingerprint("justice edo rubicun wesley shore leave unique seven")},
		{EpisodeNumber: 8, Vector: textutil.NewFingerprint("battle ferengi bok stargazer unique eight"), RawVector: textutil.NewFingerprint("battle ferengi bok stargazer unique eight")},
	}
	resolution := resolveEpisodeClaims(rips, refs, policy)
	if len(resolution.Accepted) != 5 {
		t.Fatalf("expected 5 accepted matches, got %d", len(resolution.Accepted))
	}
	got := make(map[string]int, len(resolution.Accepted))
	for _, match := range resolution.Accepted {
		got[match.EpisodeKey] = match.TargetEpisode
	}
	want := map[string]int{
		"s01_001": 7,
		"s01_002": 8,
		"s01_003": 6,
		"s01_004": 5,
		"s01_005": 4,
	}
	for key, episode := range want {
		if got[key] != episode {
			t.Fatalf("%s matched to E%02d, want E%02d", key, got[key], episode)
		}
	}
}

func TestResolveEpisodeClaimsLeavesAdjacentAmbiguityForVerification(t *testing.T) {
	policy := DefaultPolicy()
	policy.MinSimilarityScore = 0.10
	rips := []ripFingerprint{{
		EpisodeKey: "s01_001",
		TitleID:    1,
		Vector:     textutil.NewFingerprint("alpha bravo charlie delta"),
		RawVector:  textutil.NewFingerprint("alpha bravo charlie delta"),
	}}
	refs := []referenceFingerprint{
		{EpisodeNumber: 7, Vector: textutil.NewFingerprint("alpha bravo charlie justice"), RawVector: textutil.NewFingerprint("alpha bravo charlie justice")},
		{EpisodeNumber: 8, Vector: textutil.NewFingerprint("alpha bravo delta battle"), RawVector: textutil.NewFingerprint("alpha bravo delta battle")},
	}
	resolution := resolveEpisodeClaims(rips, refs, policy)
	if len(resolution.Accepted) != 0 {
		t.Fatalf("expected 0 clear matches, got %d: %+v", len(resolution.Accepted), resolution.Accepted)
	}
	pending := resolution.PendingByRip["s01_001"]
	if len(pending) == 0 {
		t.Fatalf("expected pending verification candidates, got resolution=%+v", resolution)
	}
	if !pending[0].NeedsVerification {
		t.Fatal("expected top candidate to require verification")
	}
}

func TestVerifyMatchesConfirmsPairWithoutInflatingConfidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": `{"same_episode":true,"confidence":0.99,"explanation":"same dialogue"}`,
				},
			}},
		})
	}))
	defer server.Close()

	client := llm.New("test-key", server.URL, "test-model", "", "", 5, nil)
	ripPath := writeTestSRT(t, "1\n00:10:00,000 --> 00:10:02,000\nJustice dialogue\n")
	refPath := writeTestSRT(t, "1\n00:10:00,000 --> 00:10:02,000\nJustice dialogue\n")
	candidate := matchResult{
		EpisodeKey:    "s01_001",
		TargetEpisode: 7,
		Score:         0.82,
		Confidence:    0.81,
		Strength:      0.81,
	}
	accepted, remaining, result := verifyMatches(context.Background(), client, nil, map[string][]matchResult{
		"s01_001": {candidate},
	}, []ripFingerprint{{EpisodeKey: "s01_001", Path: ripPath}}, []referenceFingerprint{{EpisodeNumber: 7, CachePath: refPath}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if result == nil || result.Verified != 1 {
		t.Fatalf("expected one verified match, got %+v", result)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected no remaining pending candidates, got %+v", remaining)
	}
	if len(accepted) != 1 {
		t.Fatalf("expected one accepted match, got %d", len(accepted))
	}
	if accepted[0].Confidence != 0.81 {
		t.Fatalf("expected confidence to remain 0.81, got %.2f", accepted[0].Confidence)
	}
	if accepted[0].AcceptedBy != "llm_verified" {
		t.Fatalf("accepted_by = %q, want llm_verified", accepted[0].AcceptedBy)
	}
}

func TestReconcileSingleHoleFillsObviousMissingEpisode(t *testing.T) {
	policy := DefaultPolicy()
	matches := []matchResult{
		{EpisodeKey: "s01_001", TargetEpisode: 4, Confidence: 0.92},
		{EpisodeKey: "s01_002", TargetEpisode: 5, Confidence: 0.93},
		{EpisodeKey: "s01_003", TargetEpisode: 6, Confidence: 0.94},
		{EpisodeKey: "s01_005", TargetEpisode: 8, Confidence: 0.91},
	}
	pending := map[string][]matchResult{
		"s01_004": {
			{EpisodeKey: "s01_004", TargetEpisode: 7, Score: 0.78, Confidence: 0.80},
		},
	}
	refs := []referenceFingerprint{{EpisodeNumber: 7}}
	reconciled, ok := reconcileSingleHole(matches, pending, refs, policy)
	if !ok {
		t.Fatal("expected single-hole reconciliation to succeed")
	}
	if len(reconciled) != 5 {
		t.Fatalf("expected 5 matches after reconciliation, got %d", len(reconciled))
	}
	found := false
	for _, match := range reconciled {
		if match.EpisodeKey == "s01_004" {
			found = true
			if match.TargetEpisode != 7 {
				t.Fatalf("reconciled episode = %d, want 7", match.TargetEpisode)
			}
			if match.AcceptedBy != "single_hole_reconciliation" {
				t.Fatalf("accepted_by = %q, want single_hole_reconciliation", match.AcceptedBy)
			}
		}
	}
	if !found {
		t.Fatal("reconciled match for s01_004 not found")
	}
}

func TestReconcileSingleHoleRefusesStrongContradiction(t *testing.T) {
	policy := DefaultPolicy()
	matches := []matchResult{
		{EpisodeKey: "s01_001", TargetEpisode: 4, Confidence: 0.92},
		{EpisodeKey: "s01_002", TargetEpisode: 5, Confidence: 0.93},
		{EpisodeKey: "s01_003", TargetEpisode: 6, Confidence: 0.94},
		{EpisodeKey: "s01_005", TargetEpisode: 8, Confidence: 0.91},
	}
	pending := map[string][]matchResult{
		"s01_004": {
			{EpisodeKey: "s01_004", TargetEpisode: 8, Score: 0.92, Confidence: 0.90},
			{EpisodeKey: "s01_004", TargetEpisode: 7, Score: 0.70, Confidence: 0.72},
		},
	}
	refs := []referenceFingerprint{{EpisodeNumber: 7}}
	_, ok := reconcileSingleHole(matches, pending, refs, policy)
	if ok {
		t.Fatal("expected strong contradictory content to block reconciliation")
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

func TestApplyMatchesInfersOpeningDoubleEpisode(t *testing.T) {
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

func writeTestSRT(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.srt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
