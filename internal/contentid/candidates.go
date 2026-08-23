package contentid

import (
	"sort"

	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/tmdb"
)

type candidateEpisodePlan struct {
	InitialEpisodes []int
	ExpandedEpisodes []int
	InitialReason   string
}

func deriveCandidateEpisodes(env *ripspec.Envelope, season *tmdb.Season, discNumber int) candidateEpisodePlan {
	allEpisodes := seasonEpisodeNumbers(season)
	if len(allEpisodes) == 0 {
		return candidateEpisodePlan{}
	}

	plan := candidateEpisodePlan{
		ExpandedEpisodes: append([]int(nil), allEpisodes...),
	}

	if initial := resolvedEpisodeScope(env, allEpisodes); len(initial) > 0 {
		plan.InitialEpisodes = initial
		plan.InitialReason = "resolved_episode_scope"
		return plan
	}

	if discScoped := discBlockScope(env, allEpisodes, discNumber); len(discScoped) > 0 {
		plan.InitialEpisodes = discScoped
		plan.InitialReason = "disc_block_estimate"
		return plan
	}

	width := min(len(allEpisodes), max(len(env.Episodes)*2, 1))
	plan.InitialEpisodes = append([]int(nil), allEpisodes[:width]...)
	plan.InitialReason = "season_prefix_fallback"
	return plan
}

func shouldExpandCandidateScope(plan candidateEpisodePlan, resolution matchResolution, ripCount int) (bool, string) {
	if len(plan.ExpandedEpisodes) == 0 || sameEpisodeSet(plan.InitialEpisodes, plan.ExpandedEpisodes) {
		return false, ""
	}
	if len(resolution.Accepted) < ripCount {
		return true, "initial_scope_left_unresolved_titles"
	}
	if resolution.SuspectReferenceCount > 0 {
		return true, "initial_scope_contains_suspect_references"
	}
	return false, ""
}

func resolvedEpisodeScope(env *ripspec.Envelope, allEpisodes []int) []int {
	if env == nil || len(env.Episodes) == 0 || len(allEpisodes) == 0 {
		return nil
	}
	resolved := make([]int, 0, len(env.Episodes))
	for _, episode := range env.Episodes {
		if episode.Episode > 0 {
			resolved = append(resolved, episode.Episode)
		}
	}
	if len(resolved) == 0 {
		return nil
	}
	sort.Ints(resolved)
	resolved = compactInts(resolved)
	width := max(len(env.Episodes), len(resolved))
	start := resolved[0]
	end := resolved[len(resolved)-1]
	for end-start+1 < width {
		if end < allEpisodes[len(allEpisodes)-1] {
			end++
		}
		if end-start+1 >= width {
			break
		}
		if start > allEpisodes[0] {
			start--
		}
		if start == allEpisodes[0] && end == allEpisodes[len(allEpisodes)-1] {
			break
		}
	}
	return intersectEpisodeRange(allEpisodes, start, end)
}

func discBlockScope(env *ripspec.Envelope, allEpisodes []int, discNumber int) []int {
	if len(allEpisodes) == 0 || discNumber <= 0 {
		return nil
	}
	block := discBlockSize(env.Episodes)
	padding := max(2, block/4)
	start := (discNumber-1)*block + 1 - padding
	end := discNumber*block + padding
	if start < allEpisodes[0] {
		start = allEpisodes[0]
	}
	if end > allEpisodes[len(allEpisodes)-1] {
		end = allEpisodes[len(allEpisodes)-1]
	}
	return intersectEpisodeRange(allEpisodes, start, end)
}

func discBlockSize(episodes []ripspec.Episode) int {
	discEpisodes := len(episodes)
	if discEpisodes <= 0 {
		return 4
	}
	if probableOpeningDoubleEpisode(episodes) {
		return discEpisodes + 1
	}
	return discEpisodes
}

// probableOpeningDoubleEpisode reports whether the first episode in disc
// order looks double-length; ripspec.IsDoubleLength keeps the ratio band in
// sync with the ordering identify applies.
func probableOpeningDoubleEpisode(episodes []ripspec.Episode) bool {
	if len(episodes) < 3 {
		return false
	}
	rest := make([]int, 0, len(episodes)-1)
	for _, ep := range episodes[1:] {
		rest = append(rest, ep.RuntimeSeconds)
	}
	return ripspec.IsDoubleLength(episodes[0].RuntimeSeconds, rest)
}

func seasonEpisodeNumbers(season *tmdb.Season) []int {
	if season == nil || len(season.Episodes) == 0 {
		return nil
	}
	episodes := make([]int, 0, len(season.Episodes))
	for _, episode := range season.Episodes {
		if episode.EpisodeNumber > 0 {
			episodes = append(episodes, episode.EpisodeNumber)
		}
	}
	sort.Ints(episodes)
	return compactInts(episodes)
}

func intersectEpisodeRange(allEpisodes []int, start, end int) []int {
	out := make([]int, 0, end-start+1)
	for _, episode := range allEpisodes {
		if episode >= start && episode <= end {
			out = append(out, episode)
		}
	}
	return out
}

func compactInts(values []int) []int {
	if len(values) == 0 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func sameEpisodeSet(a, b []int) bool {
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
