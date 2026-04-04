package contentid

import (
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/tmdb"
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

type strategyAttempt struct {
	Name     string
	Reason   string
	Episodes []int
}

type strategyOutcome struct {
	Attempt      strategyAttempt
	References   []referenceFingerprint
	Matches      []matchResult
	Refinement   blockRefinement
	AverageScore float64
}

func deriveCandidateEpisodes(env *ripspec.Envelope, season *tmdb.Season, discNumber int, policy Policy) candidateEpisodePlan {
	policy = policy.normalized()
	plan := candidateEpisodePlan{}
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
		padding := maxInt(policy.DiscBlockPaddingMin, block/policy.DiscBlockPaddingDivisor)
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

func buildEpisodePasses(plan candidateEpisodePlan, season *tmdb.Season, discEpisodes int) [][]int {
	allEpisodes := seasonEpisodeNumbers(season)
	if len(allEpisodes) == 0 {
		return nil
	}
	width := plan.PassSize
	if width <= 0 {
		width = discEpisodes * 2
	}
	if width <= 0 {
		width = minInt(12, len(allEpisodes))
	}
	if width > len(allEpisodes) {
		width = len(allEpisodes)
	}
	startIdx := passStartIndex(plan, width, len(allEpisodes))
	passes := make([][]int, 0, 1+len(allEpisodes)/maxInt(1, width))
	passes = append(passes, append([]int(nil), allEpisodes[startIdx:startIdx+width]...))

	left := startIdx
	right := startIdx + width
	for left > 0 || right < len(allEpisodes) {
		pass := make([]int, 0, width)
		leftStart := maxInt(0, left-discEpisodes)
		if leftStart < left {
			pass = append(pass, allEpisodes[leftStart:left]...)
		}
		rightEnd := minInt(len(allEpisodes), right+discEpisodes)
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
	return maxInt(0, startIdx)
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
		if _, ok := present[episode]; !ok {
			missing = append(missing, episode)
		}
	}
	return missing
}

func buildStrategyAttempts(plan candidateEpisodePlan, anchor anchorSelection, hasAnchor bool, allSeasonEpisodes []int) []strategyAttempt {
	attempts := make([]strategyAttempt, 0, 4)
	if len(plan.Episodes) > 0 {
		reason := "derived_from_ripspec"
		if len(plan.RipSpecEpisodes) == 0 && len(plan.DiscBlockEpisodes) > 0 {
			reason = "disc_block_estimate"
		}
		attempts = append(attempts, strategyAttempt{Name: "ripspec_seed", Reason: reason, Episodes: append([]int(nil), plan.Episodes...)})
	}
	if hasAnchor {
		attempts = append(attempts, strategyAttempt{Name: "anchor_window", Reason: anchor.Reason, Episodes: buildEpisodeRange(anchor.WindowStart, anchor.WindowEnd)})
	}
	if len(plan.DiscBlockEpisodes) > 0 {
		attempts = append(attempts, strategyAttempt{Name: "disc_block", Reason: "disc_number_window", Episodes: append([]int(nil), plan.DiscBlockEpisodes...)})
	}
	if len(allSeasonEpisodes) > 0 {
		attempts = append(attempts, strategyAttempt{Name: "full_season", Reason: "season_fallback", Episodes: append([]int(nil), allSeasonEpisodes...)})
	}
	seen := make(map[string]struct{}, len(attempts))
	out := make([]strategyAttempt, 0, len(attempts))
	for _, attempt := range attempts {
		sort.Ints(attempt.Episodes)
		attempt.Episodes = compactInts(attempt.Episodes)
		if len(attempt.Episodes) == 0 {
			continue
		}
		key := episodeListKey(attempt.Episodes)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, attempt)
	}
	return out
}

func episodeListKey(episodes []int) string {
	parts := make([]string, 0, len(episodes))
	for _, ep := range episodes {
		parts = append(parts, strconv.Itoa(ep))
	}
	return strings.Join(parts, ",")
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

func averageMatchScore(matches []matchResult) float64 {
	if len(matches) == 0 {
		return 0
	}
	total := 0.0
	for _, match := range matches {
		total += match.Score
	}
	return total / float64(len(matches))
}

func betterOutcome(candidate, current strategyOutcome) bool {
	if len(candidate.Matches) != len(current.Matches) {
		return len(candidate.Matches) > len(current.Matches)
	}
	if candidate.AverageScore != current.AverageScore {
		return candidate.AverageScore > current.AverageScore
	}
	if candidate.Refinement.NeedsReview != current.Refinement.NeedsReview {
		return !candidate.Refinement.NeedsReview
	}
	return false
}

func logStrategySummary(logger *slog.Logger, outcomes []strategyOutcome, selected strategyOutcome) {
	if logger == nil {
		return
	}
	for _, outcome := range outcomes {
		result := "evaluated"
		if outcome.Attempt.Name == selected.Attempt.Name {
			result = "selected"
		}
		logger.Info("content id strategy evaluation",
			"decision_type", "contentid_strategy",
			"decision_result", result,
			"decision_reason", outcome.Attempt.Reason,
			"strategy", outcome.Attempt.Name,
			"episode_window_size", len(outcome.Attempt.Episodes),
			"references", len(outcome.References),
			"matches", len(outcome.Matches),
			"average_score", outcome.AverageScore,
			"needs_review", outcome.Refinement.NeedsReview,
		)
	}
}
