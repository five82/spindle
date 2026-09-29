package contentid

import (
	"fmt"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/tmdb"
	"math"
	"slices"
)

// structuralReviewReasons checks assignments without inventing episode ranges
// or shifting canonical TMDB numbers. Runtime can trigger review, never prove
// that a title covers multiple episodes (or that it contains only one).
func structuralReviewReasons(episodes []ripspec.Episode, discNumber int, season *tmdb.Season) []string {
	runtimes := make(map[int]int, len(season.Episodes))
	for _, ep := range season.Episodes {
		runtimes[ep.EpisodeNumber] = ep.Runtime * 60
	}
	numbers := make([]int, 0, len(episodes))
	reasons := make([]string, 0)
	for _, ep := range episodes {
		if ep.Episode <= 0 {
			continue
		}
		expected, known := 0, true
		for n := ep.Episode; n <= ep.EpisodeLast(); n++ {
			numbers = append(numbers, n)
			expected += runtimes[n]
			known = known && runtimes[n] > 0
		}
		if !known || ep.RuntimeSeconds <= 0 {
			reasons = append(reasons, fmt.Sprintf("%s: source or TMDB episode runtime unavailable", ep.Key))
		} else if math.Abs(float64(ep.RuntimeSeconds-expected)) > max(300, float64(expected)*0.25) {
			// Same generous runtime tolerance as physical title selection: TMDB
			// runtimes can be rounded, but a double cannot pass as one half.
			reasons = append(reasons, fmt.Sprintf("%s: runtime %ds inconsistent with TMDB %ds; possible partial or composite episode", ep.Key, ep.RuntimeSeconds, expected))
		}
	}
	if len(numbers) == 0 {
		return nil
	}
	slices.Sort(numbers)
	unique := slices.Compact(numbers)
	if len(unique) != len(numbers) {
		reasons = append(reasons, "accepted episode assignments overlap")
	}
	numbers = unique
	if discNumber == 1 && numbers[0] > 1 {
		reasons = append(reasons, fmt.Sprintf("disc 1 matched subset starts at episode %d", numbers[0]))
	}
	if fragmentedEpisodeSubset(numbers) {
		reasons = append(reasons, "accepted episode subset is fragmented")
	}
	return reasons
}

func fragmentedEpisodeSubset(episodes []int) bool {
	if len(episodes) < 3 {
		return false
	}
	gaps := 0
	for i := 1; i < len(episodes); i++ {
		if episodes[i]-episodes[i-1] > 1 {
			gaps++
		}
	}
	return gaps > 1
}
