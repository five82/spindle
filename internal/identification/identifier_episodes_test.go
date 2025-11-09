package identification

import (
	"testing"

	"spindle/internal/disc"
	"spindle/internal/identification/tmdb"
)

func TestMapEpisodesToTitlesUsesDiscNumber(t *testing.T) {
	titles := []disc.Title{
		{ID: 0, Duration: 22 * 60},
		{ID: 1, Duration: 22 * 60},
		{ID: 2, Duration: 22 * 60},
	}
	episodes := []tmdb.Episode{
		{SeasonNumber: 5, EpisodeNumber: 1, Runtime: 22},
		{SeasonNumber: 5, EpisodeNumber: 2, Runtime: 22},
		{SeasonNumber: 5, EpisodeNumber: 3, Runtime: 22},
		{SeasonNumber: 5, EpisodeNumber: 4, Runtime: 22},
		{SeasonNumber: 5, EpisodeNumber: 5, Runtime: 22},
		{SeasonNumber: 5, EpisodeNumber: 6, Runtime: 22},
	}

	matchesDisc1, numbers := mapEpisodesToTitles(titles, episodes, 1)
	if len(matchesDisc1) != 3 || len(numbers) != 3 {
		t.Fatalf("expected three matches for disc 1, got %d", len(matchesDisc1))
	}
	if match, ok := matchesDisc1[0]; !ok || match.Episode != 1 {
		t.Fatalf("title 0 expected episode 1, got %+v", match)
	}
	if match, ok := matchesDisc1[2]; !ok || match.Episode != 3 {
		t.Fatalf("title 2 expected episode 3, got %+v", match)
	}

	matchesDisc2, numbers2 := mapEpisodesToTitles(titles, episodes, 2)
	if len(matchesDisc2) != 3 || len(numbers2) != 3 {
		t.Fatalf("expected three matches for disc 2, got %d", len(matchesDisc2))
	}
	if match, ok := matchesDisc2[0]; !ok || match.Episode != 4 {
		t.Fatalf("title 0 expected episode 4 on disc 2, got %+v", match)
	}
	if match, ok := matchesDisc2[2]; !ok || match.Episode != 6 {
		t.Fatalf("title 2 expected episode 6 on disc 2, got %+v", match)
	}
}

func TestMapEpisodesToTitlesSkipsNonEpisodeDurations(t *testing.T) {
	titles := []disc.Title{
		{ID: 0, Duration: 9300}, // extras, should be skipped
		{ID: 1, Duration: 22 * 60},
	}
	episodes := []tmdb.Episode{
		{SeasonNumber: 5, EpisodeNumber: 1, Runtime: 22},
		{SeasonNumber: 5, EpisodeNumber: 2, Runtime: 22},
	}

	matches, numbers := mapEpisodesToTitles(titles, episodes, 1)
	if len(matches) != 1 || len(numbers) != 1 {
		t.Fatalf("expected one mapped episode, got matches=%d numbers=%d", len(matches), len(numbers))
	}
	if _, ok := matches[0]; ok {
		t.Fatalf("title 0 should not be mapped")
	}
	if match, ok := matches[1]; !ok || match.Episode != 1 {
		t.Fatalf("title 1 should map to episode 1, got %+v", match)
	}
}
