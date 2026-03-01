package contentid

import (
	"slices"
	"sort"

	"spindle/internal/identification/tmdb"
	"spindle/internal/ripspec"
)

type candidateEpisodePlan struct {
	Episodes          []int
	Sources           []string
	RipSpecEpisodes   []int
	DiscBlockEpisodes []int
	SeasonFallback    []int
}

func (p candidateEpisodePlan) Options() map[string]any {
	return map[string]any{
		"rip_spec":        p.RipSpecEpisodes,
		"disc_block":      p.DiscBlockEpisodes,
		"season_fallback": p.SeasonFallback,
	}
}

func deriveCandidateEpisodes(env *ripspec.Envelope, season *tmdb.SeasonDetails, discNumber int) candidateEpisodePlan {
	plan := candidateEpisodePlan{}
	// Tier 1: collect resolved episode numbers from the rip spec.
	set := make(map[int]struct{}, len(env.Episodes)*2)
	for _, episode := range env.Episodes {
		if episode.Episode > 0 {
			set[episode.Episode] = struct{}{}
			plan.RipSpecEpisodes = append(plan.RipSpecEpisodes, episode.Episode)
		}
	}
	sort.Ints(plan.RipSpecEpisodes)
	if len(plan.RipSpecEpisodes) > 0 {
		plan.Sources = append(plan.Sources, "rip_spec")
	}

	// Tier 2: supplement resolved episodes with disc-block neighbors.
	// Only runs when Tier 1 found at least one resolved episode.
	totalEpisodes := len(season.Episodes)
	if len(set) > 0 && discNumber > 0 && totalEpisodes > 0 {
		block := len(env.Episodes)
		if block == 0 {
			block = 4
		}
		start := (discNumber - 1) * block
		if start >= totalEpisodes {
			start = totalEpisodes - block
		}
		if start < 0 {
			start = 0
		}
		for idx := start; idx < totalEpisodes && idx < start+block; idx++ {
			number := season.Episodes[idx].EpisodeNumber
			set[number] = struct{}{}
			plan.DiscBlockEpisodes = append(plan.DiscBlockEpisodes, number)
		}
		sort.Ints(plan.DiscBlockEpisodes)
		if len(plan.DiscBlockEpisodes) > 0 {
			plan.Sources = append(plan.Sources, "disc_block")
		}
	}

	// Tier 2b: disc-block estimate for placeholder episodes.
	// When Tier 1 found no resolved episodes but we know the disc number,
	// estimate which episodes belong on this disc rather than searching
	// the entire season.
	if len(set) == 0 && discNumber > 0 && totalEpisodes > 0 {
		block := len(env.Episodes)
		if block == 0 {
			block = 4
		}
		padding := max(2, block/4)
		start := (discNumber-1)*block - padding
		end := discNumber*block + padding
		if start < 0 {
			start = 0
		}
		if end > totalEpisodes {
			end = totalEpisodes
		}
		for idx := start; idx < end; idx++ {
			number := season.Episodes[idx].EpisodeNumber
			set[number] = struct{}{}
			plan.DiscBlockEpisodes = append(plan.DiscBlockEpisodes, number)
		}
		sort.Ints(plan.DiscBlockEpisodes)
		if len(plan.DiscBlockEpisodes) > 0 {
			plan.Sources = append(plan.Sources, "disc_block")
		}
	}

	// Tier 3: fall back to full season when no episodes were resolved.
	if len(set) == 0 {
		for _, episode := range season.Episodes {
			plan.SeasonFallback = append(plan.SeasonFallback, episode.EpisodeNumber)
			set[episode.EpisodeNumber] = struct{}{}
		}
		sort.Ints(plan.SeasonFallback)
		plan.Sources = append(plan.Sources, "season_fallback")
	}
	list := make([]int, 0, len(set))
	for number := range set {
		list = append(list, number)
	}
	sort.Ints(list)
	plan.Episodes = list
	return plan
}

func seasonEpisodeNumbers(season *tmdb.SeasonDetails) []int {
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
	return slices.Compact(episodes)
}

func buildEpisodeRange(start, end int) []int {
	if start <= 0 || end < start {
		return nil
	}
	episodes := make([]int, 0, end-start+1)
	for episode := start; episode <= end; episode++ {
		episodes = append(episodes, episode)
	}
	return episodes
}

func episodeWindow(episodes []int, fallbackMax int) (int, int) {
	if len(episodes) == 0 {
		if fallbackMax > 0 {
			return 1, fallbackMax
		}
		return 0, 0
	}
	low := episodes[0]
	high := episodes[0]
	for _, episode := range episodes[1:] {
		if episode < low {
			low = episode
		}
		if episode > high {
			high = episode
		}
	}
	if low < 1 {
		low = 1
	}
	if fallbackMax > 0 && high > fallbackMax {
		high = fallbackMax
	}
	return low, high
}

func filterReferencesByEpisodes(refs []referenceFingerprint, episodes []int) []referenceFingerprint {
	if len(refs) == 0 || len(episodes) == 0 {
		return nil
	}
	include := make(map[int]struct{}, len(episodes))
	for _, episode := range episodes {
		include[episode] = struct{}{}
	}
	filtered := make([]referenceFingerprint, 0, len(episodes))
	for _, ref := range refs {
		if _, ok := include[ref.EpisodeNumber]; ok {
			filtered = append(filtered, ref)
		}
	}
	return filtered
}

func missingEpisodeReferences(refs []referenceFingerprint, episodes []int) []int {
	if len(episodes) == 0 {
		return nil
	}
	existing := make(map[int]struct{}, len(refs))
	for _, ref := range refs {
		existing[ref.EpisodeNumber] = struct{}{}
	}
	missing := make([]int, 0)
	for _, episode := range episodes {
		if _, ok := existing[episode]; !ok {
			missing = append(missing, episode)
		}
	}
	return missing
}
