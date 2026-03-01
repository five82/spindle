package contentid

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/ripspec"
)

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

func buildStrategyAttempts(plan candidateEpisodePlan, anchor anchorSelection, hasAnchor bool, allSeasonEpisodes []int) []strategyAttempt {
	attempts := make([]strategyAttempt, 0, 4)
	if len(plan.Episodes) > 0 {
		reason := "derived_from_ripspec"
		if len(plan.RipSpecEpisodes) == 0 && len(plan.DiscBlockEpisodes) > 0 {
			reason = "disc_block_estimate"
		}
		attempts = append(attempts, strategyAttempt{
			Name:     "ripspec_seed",
			Reason:   reason,
			Episodes: append([]int(nil), plan.Episodes...),
		})
	}
	if hasAnchor {
		attempts = append(attempts, strategyAttempt{
			Name:     "anchor_window",
			Reason:   anchor.Reason,
			Episodes: buildEpisodeRange(anchor.WindowStart, anchor.WindowEnd),
		})
	}
	if len(plan.DiscBlockEpisodes) > 0 {
		attempts = append(attempts, strategyAttempt{
			Name:     "disc_block",
			Reason:   "disc_number_window",
			Episodes: append([]int(nil), plan.DiscBlockEpisodes...),
		})
	}
	if len(allSeasonEpisodes) > 0 {
		attempts = append(attempts, strategyAttempt{
			Name:     "full_season",
			Reason:   "season_fallback",
			Episodes: append([]int(nil), allSeasonEpisodes...),
		})
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
	if len(episodes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		parts = append(parts, strconv.Itoa(episode))
	}
	return strings.Join(parts, ",")
}

func compactInts(values []int) []int {
	if len(values) == 0 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value == out[len(out)-1] {
			continue
		}
		out = append(out, value)
	}
	return out
}

func (m *Matcher) evaluateStrategy(
	ctx context.Context,
	info episodeContext,
	season *tmdb.SeasonDetails,
	ripPrints []ripFingerprint,
	allSeasonRefs []referenceFingerprint,
	attempt strategyAttempt,
	progress func(phase string, current, total int, episodeKey string),
) (strategyOutcome, error) {
	out := strategyOutcome{Attempt: attempt}
	refs := filterReferencesByEpisodes(allSeasonRefs, attempt.Episodes)
	if len(refs) == 0 {
		fetched, err := m.fetchReferenceFingerprints(ctx, info, season, attempt.Episodes, progress)
		if err != nil {
			return out, err
		}
		for i := range fetched {
			if fetched[i].RawVector == nil {
				fetched[i].RawVector = fetched[i].Vector
			}
		}
		refs = fetched
	}
	if len(refs) == 0 {
		return out, nil
	}
	for i := range refs {
		if refs[i].RawVector == nil {
			refs[i].RawVector = refs[i].Vector
		}
	}

	applyIDFWeighting(ripPrints, refs)
	matches := resolveEpisodeMatches(ripPrints, refs, m.policy.MinSimilarityScore)
	refinement := blockRefinement{}
	if len(matches) > 0 {
		matches, refinement = refineMatchBlock(matches, refs, ripPrints, len(season.Episodes), info.DiscNumber, m.policy)
	}

	out.References = refs
	out.Matches = matches
	out.Refinement = refinement
	out.AverageScore = averageMatchScore(matches)
	return out, nil
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

func (m *Matcher) attachStrategyAttributes(env *ripspec.Envelope, selected strategyOutcome, outcomes []strategyOutcome) {
	if env == nil {
		return
	}
	if strings.TrimSpace(selected.Attempt.Name) != "" {
		env.Attributes.ContentIDSelectedStrategy = selected.Attempt.Name
	}
	scores := make([]ripspec.StrategyScore, 0, len(outcomes))
	for _, outcome := range outcomes {
		scores = append(scores, ripspec.StrategyScore{
			Strategy:     outcome.Attempt.Name,
			Reason:       outcome.Attempt.Reason,
			EpisodeCount: len(outcome.Attempt.Episodes),
			References:   len(outcome.References),
			Matches:      len(outcome.Matches),
			AvgScore:     outcome.AverageScore,
			NeedsReview:  outcome.Refinement.NeedsReview,
		})
	}
	if len(scores) > 0 {
		env.Attributes.ContentIDStrategyScores = scores
	}
}

func logStrategySummary(logger LoggerLike, outcomes []strategyOutcome, selected strategyOutcome) {
	if logger == nil {
		return
	}
	for _, outcome := range outcomes {
		result := "evaluated"
		if outcome.Attempt.Name == selected.Attempt.Name {
			result = "selected"
		}
		logger.Info("content id strategy evaluation",
			logging.String(logging.FieldEventType, "decision_summary"),
			logging.String(logging.FieldDecisionType, "contentid_strategy"),
			logging.String("decision_result", result),
			logging.String("decision_reason", outcome.Attempt.Reason),
			logging.String("strategy", outcome.Attempt.Name),
			logging.Int("episode_window_size", len(outcome.Attempt.Episodes)),
			logging.Int("references", len(outcome.References)),
			logging.Int("matches", len(outcome.Matches)),
			logging.Float64("average_score", outcome.AverageScore),
			logging.Bool("needs_review", outcome.Refinement.NeedsReview),
		)
	}
}

// LoggerLike captures the slog methods used by logStrategySummary.
type LoggerLike interface {
	Info(msg string, args ...any)
}
