package contentid

import (
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
	got := deriveCandidateEpisodes(env, season, 0).Episodes
	expect := []int{2, 4}
	if !intSlicesEqual(got, expect) {
		t.Fatalf("expected %v, got %v", expect, got)
	}
}

func TestDeriveCandidateEpisodesPlaceholdersFallBackToSeason(t *testing.T) {
	// With Episode=0 placeholders, Tier 1 (rip_spec) produces nothing,
	// set is empty so Tier 2 (disc_block) is skipped,
	// and Tier 3 (season_fallback) returns all season episodes.
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
	got := deriveCandidateEpisodes(env, season, 2).Episodes
	expect := []int{1, 2, 3, 4, 5, 6, 7, 8}
	if !intSlicesEqual(got, expect) {
		t.Fatalf("expected %v, got %v", expect, got)
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
	got := deriveCandidateEpisodes(env, season, 2).Episodes
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
	got := deriveCandidateEpisodes(env, season, 0).Episodes
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

func TestUniqueInts(t *testing.T) {
	if uniqueInts(nil) != nil {
		t.Fatalf("expected nil output for nil input")
	}
	input := []int{1, 1, 2, 2, 3}
	got := uniqueInts(input)
	expect := []int{1, 2, 3}
	if !intSlicesEqual(got, expect) {
		t.Fatalf("expected %v, got %v", expect, got)
	}
}

func TestMarkEpisodesSynchronized(t *testing.T) {
	env := &ripspec.Envelope{}
	markEpisodesSynchronized(env)
	if env.Attributes == nil {
		t.Fatalf("expected attributes to be initialized")
	}
	if value, ok := env.Attributes["episodes_synchronized"].(bool); !ok || !value {
		t.Fatalf("expected episodes_synchronized true, got %#v", env.Attributes["episodes_synchronized"])
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
		raw, ok := env.Attributes["content_id_transcripts"]
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
			if _, ok := env.Attributes["content_id_transcripts"]; ok {
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
		paths := env.Attributes["content_id_transcripts"].(map[string]string)
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
		got, idx := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if idx != 0 || got.FileID != 100 {
			t.Fatalf("expected first candidate (idx=0, FileID=100), got idx=%d, FileID=%d", idx, got.FileID)
		}
	})

	t.Run("skips candidate with different episode title", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "South Park 5x02 It Hits The Fan", Downloads: 500},
			{FileID: 101, Release: "South Park 5x02 Cripple Fight", Downloads: 200},
		}
		got, idx := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if idx != 1 || got.FileID != 101 {
			t.Fatalf("expected second candidate (idx=1, FileID=101), got idx=%d, FileID=%d", idx, got.FileID)
		}
	})

	t.Run("falls back to first when all candidates suspect", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "South Park 5x02 It Hits The Fan", Downloads: 500},
			{FileID: 101, Release: "South Park 5x02 Super Best Friends", Downloads: 200},
		}
		got, idx := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if idx != 0 || got.FileID != 100 {
			t.Fatalf("expected fallback to first (idx=0, FileID=100), got idx=%d, FileID=%d", idx, got.FileID)
		}
	})

	t.Run("accepts candidate with both current and other title", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "South Park It Hits The Fan and Cripple Fight", Downloads: 500},
			{FileID: 101, Release: "South Park 5x02 Cripple Fight", Downloads: 200},
		}
		got, idx := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if idx != 0 || got.FileID != 100 {
			t.Fatalf("expected first candidate (ambiguous, not clearly wrong), got idx=%d, FileID=%d", idx, got.FileID)
		}
	})

	t.Run("accepts candidate with empty release", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "", Downloads: 500},
			{FileID: 101, Release: "South Park 5x02 Cripple Fight", Downloads: 200},
		}
		got, idx := selectReferenceCandidate(candidates, "Cripple Fight", season)
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
		got, idx := selectReferenceCandidate(candidates, "Dos", shortSeason)
		if idx != 0 || got.FileID != 100 {
			t.Fatalf("expected first candidate (short titles bypass check), got idx=%d, FileID=%d", idx, got.FileID)
		}
	})

	t.Run("single candidate returns it", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "South Park 5x02 It Hits The Fan", Downloads: 500},
		}
		got, idx := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if idx != 0 || got.FileID != 100 {
			t.Fatalf("expected only candidate returned, got idx=%d, FileID=%d", idx, got.FileID)
		}
	})

	t.Run("case insensitive matching", func(t *testing.T) {
		candidates := []opensubtitles.Subtitle{
			{FileID: 100, Release: "SOUTH PARK 5X02 IT HITS THE FAN", Downloads: 500},
			{FileID: 101, Release: "south park 5x02 cripple fight", Downloads: 200},
		}
		got, idx := selectReferenceCandidate(candidates, "Cripple Fight", season)
		if idx != 1 || got.FileID != 101 {
			t.Fatalf("expected second candidate (case-insensitive skip), got idx=%d, FileID=%d", idx, got.FileID)
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
