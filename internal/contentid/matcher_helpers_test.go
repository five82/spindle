package contentid

import (
	"testing"

	"spindle/internal/identification/tmdb"
	"spindle/internal/ripspec"
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

func TestDeriveCandidateEpisodesUsesDiscBlocks(t *testing.T) {
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s01e00"},
			{Key: "s01e00"},
			{Key: "s01e00"},
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

func TestBuildEpisodeBasename(t *testing.T) {
	name := buildEpisodeBasename("Show Name", 1, 2)
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
