package contentid

import (
	"testing"

	"spindle/internal/identification/tmdb"
	"spindle/internal/ripspec"
)

func TestResolveEpisodeMatchesPrefersHighestScore(t *testing.T) {
	rips := []ripFingerprint{
		{EpisodeKey: "s05e01", TitleID: 1, Vector: newFingerprint("alpha beta gamma alpha beta gamma theme park laughter cheer")},
		{EpisodeKey: "s05e02", TitleID: 2, Vector: newFingerprint("delta epsilon zeta karate alley fight delta epsilon zeta")},
		{EpisodeKey: "s05e03", TitleID: 3, Vector: newFingerprint("eta theta iota magic heroes eta theta iota rescue mission")},
	}
	refs := []referenceFingerprint{
		{EpisodeNumber: 2, Vector: newFingerprint("delta epsilon zeta alley fight delta epsilon zeta karate victory")},
		{EpisodeNumber: 3, Vector: newFingerprint("eta theta iota magic heroes eta theta iota summon help")},
		{EpisodeNumber: 1, Vector: newFingerprint("alpha beta gamma alpha beta gamma amusement park fun")},
	}
	matches := resolveEpisodeMatches(rips, refs)
	if len(matches) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(matches))
	}
	want := map[string]int{
		"s05e01": 1,
		"s05e02": 2,
		"s05e03": 3,
	}
	for _, match := range matches {
		if wantEpisode, ok := want[match.EpisodeKey]; !ok || wantEpisode != match.TargetEpisode {
			t.Fatalf("unexpected match %+v", match)
		}
		if match.Score <= 0 {
			t.Fatalf("expected positive score for %+v", match)
		}
	}
}

func TestDeriveCandidateEpisodesUsesDiscNumber(t *testing.T) {
	season := &tmdb.SeasonDetails{
		SeasonNumber: 5,
		Episodes: []tmdb.Episode{
			{EpisodeNumber: 1}, {EpisodeNumber: 2}, {EpisodeNumber: 3},
			{EpisodeNumber: 4}, {EpisodeNumber: 5}, {EpisodeNumber: 6},
			{EpisodeNumber: 7}, {EpisodeNumber: 8}, {EpisodeNumber: 9},
		},
	}
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Episode: 1}, {Episode: 0}, {Episode: 0},
		},
	}
	candidates := deriveCandidateEpisodes(env, season, 2)
	if len(candidates) == 0 {
		t.Fatal("expected candidates when disc number present")
	}
	want := []int{1, 4, 5, 6}
	for _, episode := range want {
		if !containsEpisode(candidates, episode) {
			t.Fatalf("expected episode %d in candidates %v", episode, candidates)
		}
	}
}

func containsEpisode(list []int, value int) bool {
	for _, entry := range list {
		if entry == value {
			return true
		}
	}
	return false
}
