package identification

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"log/slog"

	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
)

type scoredCandidate struct {
	ID           int64
	Title        string
	Score        float64
	VoteAverage  float64
	VoteCount    int64
	ExactMatch   bool
	MatchType    string
	TitleCleaned string
}

func selectBestResult(logger *slog.Logger, query string, response *tmdb.Response) *tmdb.Result {
	if response == nil || len(response.Results) == 0 {
		return nil
	}
	queryLower := strings.ToLower(query)
	queryNormalized := normalizeForComparison(query)
	var best *tmdb.Result
	bestScore := -1.0
	candidates := make([]scoredCandidate, 0, len(response.Results))

	for idx := range response.Results {
		res := response.Results[idx]
		score := scoreResult(queryLower, res)

		title := pickTitle(res)
		titleLower := strings.ToLower(title)
		titleNormalized := normalizeForComparison(title)
		exactMatch := titleLower == queryLower || titleNormalized == queryNormalized
		match := matchType(titleLower, queryLower)

		logger.Debug("calculating confidence score",
			logging.Int("result_index", idx),
			logging.Int64("tmdb_id", res.ID),
			logging.String("title", title),
			logging.String("title_normalized", titleNormalized),
			logging.Float64("calculated_score", score),
			logging.Float64("vote_average", res.VoteAverage),
			logging.Int64("vote_count", res.VoteCount),
			logging.Bool("exact_title_match", exactMatch),
			logging.String("match_type", match))

		candidates = append(candidates, scoredCandidate{
			ID:           res.ID,
			Title:        title,
			Score:        score,
			VoteAverage:  res.VoteAverage,
			VoteCount:    res.VoteCount,
			ExactMatch:   exactMatch,
			MatchType:    match,
			TitleCleaned: titleNormalized,
		})

		if score > bestScore {
			best = &response.Results[idx]
			bestScore = score
		}
	}

	if best == nil {
		return nil
	}

	title := pickTitle(*best)
	titleLower := strings.ToLower(title)
	titleNormalized := normalizeForComparison(title)
	exactMatch := titleLower == queryLower || titleNormalized == queryNormalized
	match := matchType(titleLower, queryLower)

	decisionResult := "rejected"
	decisionReason := ""
	rejects := []string{}
	var minExpectedScore float64
	if exactMatch {
		if best.VoteAverage < 2.0 {
			decisionReason = "vote_average_below_threshold"
			rejects = append(rejects, fmt.Sprintf("%d:vote_average_below_2.0", best.ID))
		} else {
			decisionResult = "accepted"
			decisionReason = "exact_match"
		}
	} else {
		if best.VoteAverage < 3.0 {
			decisionReason = "vote_average_below_threshold"
			rejects = append(rejects, fmt.Sprintf("%d:vote_average_below_3.0", best.ID))
		} else {
			minExpectedScore = 1.3 + float64(best.VoteCount)/1000.0
			if bestScore < minExpectedScore {
				decisionReason = "confidence_below_threshold"
				rejects = append(rejects, fmt.Sprintf("%d:confidence_below_threshold", best.ID))
			} else {
				decisionResult = "accepted"
				decisionReason = "confidence_passed"
			}
		}
	}

	if decisionReason == "" {
		decisionReason = "no_match"
	}
	topCandidates := summarizeCandidates(candidates, 3)
	attrs := []logging.Attr{
		logging.String(logging.FieldEventType, "decision_summary"),
		logging.String(logging.FieldDecisionType, "tmdb_confidence"),
		logging.String("decision_result", decisionResult),
		logging.String("decision_reason", decisionReason),
		logging.String("decision_options", "accept, reject"),
		logging.Int("candidate_count", len(topCandidates)),
		logging.String("decision_selected", fmt.Sprintf("%d:%s", best.ID, title)),
		logging.Int("total_results", len(response.Results)),
		logging.Float64("best_score", bestScore),
		logging.Float64("vote_average", best.VoteAverage),
		logging.Int64("vote_count", best.VoteCount),
		logging.Bool("exact_title_match", exactMatch),
		logging.String("match_type", match),
		logging.String("query", query),
		logging.String("query_normalized", queryNormalized),
	}
	if len(candidates) > len(topCandidates) {
		attrs = append(attrs, logging.Int("candidate_hidden_count", len(candidates)-len(topCandidates)))
	}
	for idx, candidate := range topCandidates {
		key := fmt.Sprintf("candidate_%d", idx+1)
		if id, ok := decisionItemID(candidate); ok {
			key = fmt.Sprintf("candidate_%d", id)
		}
		attrs = append(attrs, logging.String(key, candidate))
	}
	if exactMatch {
		attrs = append(attrs, logging.Float64("vote_average_threshold", 2.0))
	} else {
		attrs = append(attrs, logging.Float64("vote_average_threshold", 3.0))
		if minExpectedScore > 0 {
			attrs = append(attrs, logging.Float64("min_expected_score", minExpectedScore))
		}
	}
	if len(rejects) > 0 {
		attrs = append(attrs, logging.Int("rejected_count", len(rejects)))
		for idx, reject := range rejects {
			key := fmt.Sprintf("rejected_%d", idx+1)
			if id, ok := decisionItemID(reject); ok {
				key = fmt.Sprintf("rejected_%d", id)
			}
			attrs = append(attrs, logging.String(key, reject))
		}
	}
	logger.Info("tmdb confidence decision", logging.Args(attrs...)...)

	if decisionResult != "accepted" {
		return nil
	}
	return best
}

func matchType(titleLower, queryLower string) string {
	if titleLower == queryLower {
		return "exact"
	}
	if strings.Contains(titleLower, queryLower) {
		return "contains"
	}
	return "partial"
}

func scoreResult(query string, result tmdb.Result) float64 {
	title := pickTitle(result)
	if title == "" {
		return 0
	}
	titleLower := strings.ToLower(title)
	match := 0.0
	if strings.Contains(titleLower, query) {
		match = 1.0
	}
	return match + (result.VoteAverage / 10.0) + float64(result.VoteCount)/1000.0
}

func pickTitle(result tmdb.Result) string {
	if result.Title != "" {
		return result.Title
	}
	if result.Name != "" {
		return result.Name
	}
	return ""
}

func normalizeForComparison(input string) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}
	// Replace common symbols with word equivalents first
	normalized := strings.ToLower(input)
	normalized = strings.ReplaceAll(normalized, "&", "and")
	normalized = strings.ReplaceAll(normalized, "+", "and")

	var builder strings.Builder
	for _, r := range normalized {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func summarizeCandidates(candidates []scoredCandidate, limit int) []string {
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].VoteCount > candidates[j].VoteCount
		}
		return candidates[i].Score > candidates[j].Score
	})
	if limit <= 0 || limit > len(candidates) {
		limit = len(candidates)
	}
	out := make([]string, 0, limit)
	for _, cand := range candidates[:limit] {
		label := strings.TrimSpace(cand.Title)
		if label == "" {
			label = "untitled"
		}
		out = append(out, fmt.Sprintf("%d:%s (score=%.2f votes=%.1f/%d match=%s)", cand.ID, label, cand.Score, cand.VoteAverage, cand.VoteCount, cand.MatchType))
	}
	return out
}

func decisionItemID(value string) (int, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}
	parts := strings.SplitN(trimmed, ":", 2)
	if len(parts) == 0 {
		return 0, false
	}
	id, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, false
	}
	return id, true
}
