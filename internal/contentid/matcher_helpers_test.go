package contentid

import (
	"fmt"
	"testing"

	"spindle/internal/identification"
	"spindle/internal/identification/tmdb"
	"spindle/internal/ripspec"
	"spindle/internal/subtitles/opensubtitles"
)

func TestDeriveCandidateEpisodesUsesRipSpecEpisodes(t *testing.T) {
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s01e02", Episode: 2},
			{Key: "s01e04", Episode: 4},
		},
	}
	season := &tmdb.SeasonDetails{
		SeasonNumber: 1,
		Episodes: []tmdb.Episode{
			{EpisodeNumber: 1},
			{EpisodeNumber: 2},
			{EpisodeNumber: 3},
			{EpisodeNumber: 4},
		},
	}
	got := deriveCandidateEpisodes(env, season, 0, DefaultPolicy()).Episodes
	expect := []int{2, 4}
	if !intSlicesEqual(got, expect) {
		t.Fatalf("expected %v, got %v", expect, got)
	}
}

func TestDeriveCandidateEpisodesPlaceholdersWithDiscNumberUsesDiscBlock(t *testing.T) {
	// With Episode=0 placeholders and discNumber=2, Tier 2b (disc_block) should
	// estimate the disc's episode range instead of falling to full-season Tier 3.
	// 3 episodes on disc, discNumber=2: block starts at (2-1)*3=3, so indices 1..8
	// with padding=2: start=(2-1)*3-2=1, end=2*3+2=8 → episodes 2-8.
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s01_001", Episode: 0},
			{Key: "s01_002", Episode: 0},
			{Key: "s01_003", Episode: 0},
		},
	}
	season := &tmdb.SeasonDetails{
		SeasonNumber: 1,
		Episodes: []tmdb.Episode{
			{EpisodeNumber: 1},
			{EpisodeNumber: 2},
			{EpisodeNumber: 3},
			{EpisodeNumber: 4},
			{EpisodeNumber: 5},
			{EpisodeNumber: 6},
			{EpisodeNumber: 7},
			{EpisodeNumber: 8},
		},
	}
	plan := deriveCandidateEpisodes(env, season, 2, DefaultPolicy())
	// Should use disc_block, not season_fallback
	if len(plan.DiscBlockEpisodes) == 0 {
		t.Fatal("expected disc_block episodes, got none")
	}
	if len(plan.SeasonFallback) != 0 {
		t.Fatalf("expected no season_fallback, got %v", plan.SeasonFallback)
	}
	// Verify the result is a subset of the season (not the full season)
	if len(plan.Episodes) >= 8 {
		t.Fatalf("expected disc block to narrow candidates, got all %d", len(plan.Episodes))
	}
}

func TestDeriveCandidateEpisodesPlaceholdersDisc1(t *testing.T) {
	// Disc 1 with 12 episodes, 34-episode season (Batman scenario).
	// block=12, padding=max(2,3)=3, start=(1-1)*12-3=-3→0, end=1*12+3=15
	// → episodes 1-15
	eps := make([]ripspec.Episode, 12)
	for i := range eps {
		eps[i] = ripspec.Episode{Key: fmt.Sprintf("s01_%03d", i+1), Episode: 0}
	}
	env := &ripspec.Envelope{Episodes: eps}
	seasonEps := make([]tmdb.Episode, 34)
	for i := range seasonEps {
		seasonEps[i] = tmdb.Episode{EpisodeNumber: i + 1}
	}
	season := &tmdb.SeasonDetails{SeasonNumber: 1, Episodes: seasonEps}
	plan := deriveCandidateEpisodes(env, season, 1, DefaultPolicy())
	if len(plan.DiscBlockEpisodes) == 0 {
		t.Fatal("expected disc_block episodes for disc 1")
	}
	// Should include episodes 1-15 (not all 34)
	if plan.Episodes[0] != 1 {
		t.Fatalf("expected first candidate to be 1, got %d", plan.Episodes[0])
	}
	if len(plan.Episodes) > 20 {
		t.Fatalf("expected narrowed candidates, got %d", len(plan.Episodes))
	}
	if len(plan.Episodes) < 12 {
		t.Fatalf("expected at least 12 candidates (disc episodes), got %d", len(plan.Episodes))
	}
}

func TestDeriveCandidateEpisodesPlaceholdersNoDiscNumber(t *testing.T) {
	// With Episode=0 placeholders and discNumber=0, should fall back to
	// full-season Tier 3 since disc block can't be estimated.
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s01_001", Episode: 0},
			{Key: "s01_002", Episode: 0},
			{Key: "s01_003", Episode: 0},
		},
	}
	season := &tmdb.SeasonDetails{
		SeasonNumber: 1,
		Episodes: []tmdb.Episode{
			{EpisodeNumber: 1},
			{EpisodeNumber: 2},
			{EpisodeNumber: 3},
			{EpisodeNumber: 4},
			{EpisodeNumber: 5},
			{EpisodeNumber: 6},
			{EpisodeNumber: 7},
			{EpisodeNumber: 8},
		},
	}
	plan := deriveCandidateEpisodes(env, season, 0, DefaultPolicy())
	expect := []int{1, 2, 3, 4, 5, 6, 7, 8}
	if !intSlicesEqual(plan.Episodes, expect) {
		t.Fatalf("expected full season %v, got %v", expect, plan.Episodes)
	}
	if len(plan.SeasonFallback) == 0 {
		t.Fatal("expected season_fallback source")
	}
}

func TestDeriveCandidateEpisodesPaddingClampsToSeasonBounds(t *testing.T) {
	// Disc 3 of a 10-episode season with 3 episodes per disc.
	// block=3, padding=2, start=(3-1)*3-2=4, end=3*3+2=11→10 (clamped)
	// → episodes 5-10
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s01_001", Episode: 0},
			{Key: "s01_002", Episode: 0},
			{Key: "s01_003", Episode: 0},
		},
	}
	seasonEps := make([]tmdb.Episode, 10)
	for i := range seasonEps {
		seasonEps[i] = tmdb.Episode{EpisodeNumber: i + 1}
	}
	season := &tmdb.SeasonDetails{SeasonNumber: 1, Episodes: seasonEps}
	plan := deriveCandidateEpisodes(env, season, 3, DefaultPolicy())
	if len(plan.DiscBlockEpisodes) == 0 {
		t.Fatal("expected disc_block episodes")
	}
	// Last candidate should not exceed season size
	last := plan.Episodes[len(plan.Episodes)-1]
	if last > 10 {
		t.Fatalf("candidates should not exceed season bounds, got %d", last)
	}
	first := plan.Episodes[0]
	if first < 1 {
		t.Fatalf("candidates should not be below 1, got %d", first)
	}
}

func TestDeriveCandidateEpisodesUsesDiscBlocksWithResolved(t *testing.T) {
	// When some episodes ARE resolved, disc_block tier still contributes.
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s01e04", Episode: 4},
			{Key: "s01e05", Episode: 5},
			{Key: "s01e06", Episode: 6},
		},
	}
	season := &tmdb.SeasonDetails{
		SeasonNumber: 1,
		Episodes: []tmdb.Episode{
			{EpisodeNumber: 1},
			{EpisodeNumber: 2},
			{EpisodeNumber: 3},
			{EpisodeNumber: 4},
			{EpisodeNumber: 5},
			{EpisodeNumber: 6},
			{EpisodeNumber: 7},
			{EpisodeNumber: 8},
		},
	}
	got := deriveCandidateEpisodes(env, season, 2, DefaultPolicy()).Episodes
	expect := []int{4, 5, 6}
	if !intSlicesEqual(got, expect) {
		t.Fatalf("expected %v, got %v", expect, got)
	}
}

func TestDeriveCandidateEpisodesFallsBackToSeason(t *testing.T) {
	env := &ripspec.Envelope{}
	season := &tmdb.SeasonDetails{
		SeasonNumber: 1,
		Episodes: []tmdb.Episode{
			{EpisodeNumber: 1},
			{EpisodeNumber: 2},
		},
	}
	got := deriveCandidateEpisodes(env, season, 0, DefaultPolicy()).Episodes
	expect := []int{1, 2}
	if !intSlicesEqual(got, expect) {
		t.Fatalf("expected %v, got %v", expect, got)
	}
}

func TestEpisodeOutputBasename(t *testing.T) {
	name := identification.EpisodeOutputBasename("Show Name", 1, 2)
	if name != "Show Name - S01E02" {
		t.Fatalf("unexpected basename %q", name)
	}
}

func TestMarkEpisodesSynchronized(t *testing.T) {
	env := &ripspec.Envelope{}
	markEpisodesSynchronized(env)
	if env.Attributes == nil {
		t.Fatalf("expected attributes to be initialized")
	}
	if value, ok := env.Attributes[ripspec.AttrEpisodesSynchronized].(bool); !ok || !value {
		t.Fatalf("expected episodes_synchronized true, got %#v", env.Attributes[ripspec.AttrEpisodesSynchronized])
	}
}

func TestAttachTranscriptPaths(t *testing.T) {
	t.Run("stores paths correctly", func(t *testing.T) {
		env := &ripspec.Envelope{}
		fps := []ripFingerprint{
			{EpisodeKey: "s01e01", Path: "/tmp/s01e01.srt"},
			{EpisodeKey: "s01e02", Path: "/tmp/s01e02.srt"},
		}
		attachTranscriptPaths(env, fps)
		raw, ok := env.Attributes[ripspec.AttrContentIDTranscripts]
		if !ok {
			t.Fatal("expected content_id_transcripts attribute")
		}
		paths, ok := raw.(map[string]string)
		if !ok {
			t.Fatalf("expected map[string]string, got %T", raw)
		}
		if paths["s01e01"] != "/tmp/s01e01.srt" {
			t.Fatalf("unexpected path for s01e01: %q", paths["s01e01"])
		}
		if paths["s01e02"] != "/tmp/s01e02.srt" {
			t.Fatalf("unexpected path for s01e02: %q", paths["s01e02"])
		}
	})

	t.Run("empty fingerprints", func(t *testing.T) {
		env := &ripspec.Envelope{}
		attachTranscriptPaths(env, nil)
		if env.Attributes != nil {
			if _, ok := env.Attributes[ripspec.AttrContentIDTranscripts]; ok {
				t.Fatal("expected no content_id_transcripts for empty fingerprints")
			}
		}
	})

	t.Run("nil envelope", func(t *testing.T) {
		// Should not panic.
		attachTranscriptPaths(nil, []ripFingerprint{
			{EpisodeKey: "s01e01", Path: "/tmp/s01e01.srt"},
		})
	})

	t.Run("skips blank paths", func(t *testing.T) {
		env := &ripspec.Envelope{}
		fps := []ripFingerprint{
			{EpisodeKey: "s01e01", Path: "/tmp/s01e01.srt"},
			{EpisodeKey: "s01e02", Path: ""},
		}
		attachTranscriptPaths(env, fps)
		paths := env.Attributes[ripspec.AttrContentIDTranscripts].(map[string]string)
		if len(paths) != 1 {
			t.Fatalf("expected 1 path, got %d", len(paths))
		}
		if _, ok := paths["s01e02"]; ok {
			t.Fatal("expected blank path to be skipped")
		}
	})
}

func TestSelectReferenceCandidate(t *testing.T) {
	season := &tmdb.SeasonDetails{
		SeasonNumber: 5,
		Episodes: []tmdb.Episode{
			{EpisodeNumber: 1, Name: "It Hits the Fan"},
			{EpisodeNumber: 2, Name: "Cripple Fight"},
			{EpisodeNumber: 3, Name: "Super Best Friends"},
			{EpisodeNumber: 4, Name: "Scott Tenorman Must Die"},
		},
	}

	t.Run("picks first candidate when no mislabeling", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "South Park S05E02 Cripple Fight 720p", Downloads: 500},
			{FileID: 101, Release: "South Park 5x02 Cripple Fight", Downloads: 200},
		}
		got, idx, _ := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if idx != 0 || got.FileID != 100 {
			t.Fatalf("expected first candidate (idx=0, FileID=100), got idx=%d, FileID=%d", idx, got.FileID)
		}
	})

	t.Run("skips candidate with different episode title", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "South Park 5x02 It Hits The Fan", Downloads: 500},
			{FileID: 101, Release: "South Park 5x02 Cripple Fight", Downloads: 200},
		}
		got, idx, _ := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if idx != 1 || got.FileID != 101 {
			t.Fatalf("expected second candidate (idx=1, FileID=101), got idx=%d, FileID=%d", idx, got.FileID)
		}
	})

	t.Run("falls back to first when all candidates suspect", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "South Park 5x02 It Hits The Fan", Downloads: 500},
			{FileID: 101, Release: "South Park 5x02 Super Best Friends", Downloads: 200},
		}
		got, idx, _ := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if idx != 0 || got.FileID != 100 {
			t.Fatalf("expected fallback to first (idx=0, FileID=100), got idx=%d, FileID=%d", idx, got.FileID)
		}
	})

	t.Run("accepts candidate with both current and other title", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "South Park It Hits The Fan and Cripple Fight", Downloads: 500},
			{FileID: 101, Release: "South Park 5x02 Cripple Fight", Downloads: 200},
		}
		got, idx, _ := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if idx != 0 || got.FileID != 100 {
			t.Fatalf("expected first candidate (ambiguous, not clearly wrong), got idx=%d, FileID=%d", idx, got.FileID)
		}
	})

	t.Run("accepts candidate with empty release", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "", Downloads: 500},
			{FileID: 101, Release: "South Park 5x02 Cripple Fight", Downloads: 200},
		}
		got, idx, _ := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if idx != 0 || got.FileID != 100 {
			t.Fatalf("expected first candidate (empty release = no evidence), got idx=%d, FileID=%d", idx, got.FileID)
		}
	})

	t.Run("skips check for short episode titles", func(t *testing.T) {
		shortSeason := &tmdb.SeasonDetails{
			SeasonNumber: 1,
			Episodes: []tmdb.Episode{
				{EpisodeNumber: 1, Name: "Uno"},
				{EpisodeNumber: 2, Name: "Dos"},
			},
		}
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "Show S01E02 Uno", Downloads: 500},
			{FileID: 101, Release: "Show S01E02 Dos", Downloads: 200},
		}
		got, idx, _ := selectReferenceCandidate(candidates, "Dos", shortSeason)
		if idx != 0 || got.FileID != 100 {
			t.Fatalf("expected first candidate (short titles bypass check), got idx=%d, FileID=%d", idx, got.FileID)
		}
	})

	t.Run("single candidate returns it", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "South Park 5x02 It Hits The Fan", Downloads: 500},
		}
		got, idx, _ := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if idx != 0 || got.FileID != 100 {
			t.Fatalf("expected only candidate returned, got idx=%d, FileID=%d", idx, got.FileID)
		}
	})

	t.Run("case insensitive matching", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "SOUTH PARK 5X02 IT HITS THE FAN", Downloads: 500},
			{FileID: 101, Release: "south park 5x02 cripple fight", Downloads: 200},
		}
		got, idx, _ := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if idx != 1 || got.FileID != 101 {
			t.Fatalf("expected second candidate (case-insensitive skip), got idx=%d, FileID=%d", idx, got.FileID)
		}
	})

	t.Run("prefers non-HI over HI at same title-consistency level", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "South Park S05E02 Cripple Fight", Downloads: 500, HearingImpaired: true},
			{FileID: 101, Release: "South Park 5x02 Cripple Fight", Downloads: 200, HearingImpaired: false},
		}
		got, idx, reason := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if got.FileID != 101 {
			t.Fatalf("expected non-HI candidate (FileID=101), got FileID=%d", got.FileID)
		}
		if idx != 1 {
			t.Fatalf("expected idx=1, got %d", idx)
		}
		if reason != "non_hi_preferred" {
			t.Fatalf("expected reason=non_hi_preferred, got %q", reason)
		}
	})

	t.Run("falls back to HI when all candidates are HI", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "South Park S05E02 Cripple Fight", Downloads: 500, HearingImpaired: true},
			{FileID: 101, Release: "South Park 5x02 Cripple Fight", Downloads: 200, HearingImpaired: true},
		}
		got, idx, reason := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if got.FileID != 100 {
			t.Fatalf("expected first HI candidate (FileID=100), got FileID=%d", got.FileID)
		}
		if idx != 0 {
			t.Fatalf("expected idx=0, got %d", idx)
		}
		if reason != "hi_fallback" {
			t.Fatalf("expected reason=hi_fallback, got %q", reason)
		}
	})

	t.Run("prefers non-HI even with lower download count", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "South Park S05E02 Cripple Fight", Downloads: 838, HearingImpaired: true},
			{FileID: 101, Release: "South Park 5x02 Cripple Fight", Downloads: 42, HearingImpaired: false},
		}
		got, _, reason := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if got.FileID != 101 {
			t.Fatalf("expected non-HI candidate despite fewer downloads (FileID=101), got FileID=%d", got.FileID)
		}
		if reason != "non_hi_preferred" {
			t.Fatalf("expected reason=non_hi_preferred, got %q", reason)
		}
	})

	t.Run("title consistency still takes priority over HI avoidance", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "South Park 5x02 It Hits The Fan", Downloads: 500, HearingImpaired: false},
			{FileID: 101, Release: "South Park 5x02 Cripple Fight", Downloads: 200, HearingImpaired: true},
		}
		got, idx, _ := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if got.FileID != 101 {
			t.Fatalf("expected title-consistent HI candidate (FileID=101), got FileID=%d", got.FileID)
		}
		if idx != 1 {
			t.Fatalf("expected idx=1, got %d", idx)
		}
	})
}

func intSlicesEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
