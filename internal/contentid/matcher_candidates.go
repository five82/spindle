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
	DiscEstimateStart int
	PassSize          int
}

func (p candidateEpisodePlan) Options() map[string]any {
	return map[string]any{
		"rip_spec":        p.RipSpecEpisodes,
		"disc_block":      p.DiscBlockEpisodes,
		"season_fallback": p.SeasonFallback,
	}
}

func deriveCandidateEpisodes(env *ripspec.Envelope, season *tmdb.SeasonDetails, discNumber int, policy Policy) candidateEpisodePlan {
	policy = policy.normalized()
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
		block := discBlockSize(len(env.Episodes))
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
		block := discBlockSize(len(env.Episodes))
		plan.PassSize = block * 2
		estimateStart := (discNumber-1)*block + 1 - block/2
		if estimateStart < 1 {
			estimateStart = 1
		}
		maxStart := totalEpisodes - plan.PassSize + 1
		if maxStart < 1 {
			maxStart = 1
		}
		if estimateStart > maxStart {
			estimateStart = maxStart
		}
		plan.DiscEstimateStart = estimateStart
		padding := max(policy.DiscBlockPaddingMin, block/policy.DiscBlockPaddingDivisor)
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

func discBlockSize(discEpisodes int) int {
	if discEpisodes <= 0 {
		return 4
	}
	return discEpisodes
}

func buildEpisodePasses(plan candidateEpisodePlan, season *tmdb.SeasonDetails, discEpisodes int) [][]int {
	allEpisodes := seasonEpisodeNumbers(season)
	if len(allEpisodes) == 0 {
		return nil
	}
	width := plan.PassSize
	if width <= 0 {
		width = discEpisodes * 2
	}
	if width <= 0 {
		width = min(12, len(allEpisodes))
	}
	if width > len(allEpisodes) {
		width = len(allEpisodes)
	}

	startIdx := passStartIndex(plan, width, len(allEpisodes))

	passes := make([][]int, 0, 1+len(allEpisodes)/max(1, width))
	passes = append(passes, append([]int(nil), allEpisodes[startIdx:startIdx+width]...))

	left := startIdx
	right := startIdx + width
	for left > 0 || right < len(allEpisodes) {
		pass := make([]int, 0, width)
		leftStart := max(0, left-discEpisodes)
		if leftStart < left {
			pass = append(pass, allEpisodes[leftStart:left]...)
		}
		rightEnd := min(len(allEpisodes), right+discEpisodes)
		if right < rightEnd {
			pass = append(pass, allEpisodes[right:rightEnd]...)
		}
		if len(pass) == 0 {
			break
		}
		passes = append(passes, pass)
		left = leftStart
		right = rightEnd
	}

	return passes
}

func passStartIndex(plan candidateEpisodePlan, width, totalEpisodes int) int {
	startIdx := 0
	switch {
	case plan.DiscEstimateStart > 0:
		startIdx = plan.DiscEstimateStart - 1
	case len(plan.DiscBlockEpisodes) > 0:
		startIdx = plan.DiscBlockEpisodes[0] - 1
	}
	if startIdx+width > totalEpisodes {
		startIdx = totalEpisodes - width
	}
	return max(0, startIdx)
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

func missingEpisodesForReferences(refs []referenceFingerprint, episodes []int) []int {
	if len(episodes) == 0 {
		return nil
	}
	if len(refs) == 0 {
		return append([]int(nil), episodes...)
	}
	present := make(map[int]struct{}, len(refs))
	for _, ref := range refs {
		present[ref.EpisodeNumber] = struct{}{}
	}
	missing := make([]int, 0, len(episodes))
	for _, episode := range episodes {
		if _, ok := present[episode]; ok {
			continue
		}
		missing = append(missing, episode)
	}
	return missing
}
