package identification

import (
	"strings"
	"unicode"

	"log/slog"

	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
)

func selectBestResult(logger *slog.Logger, query string, response *tmdb.Response) *tmdb.Result {
	if response == nil || len(response.Results) == 0 {
		return nil
	}
	queryLower := strings.ToLower(query)
	queryNormalized := normalizeForComparison(query)
	var best *tmdb.Result
	bestScore := -1.0

	logger.Info("confidence scoring analysis",
		logging.String("query", query),
		logging.String("query_normalized", queryNormalized),
		logging.Int("total_results", len(response.Results)))

	for idx := range response.Results {
		res := response.Results[idx]
		score := scoreResult(queryLower, res)

		title := pickTitle(res)
		titleLower := strings.ToLower(title)
		titleNormalized := normalizeForComparison(title)
		exactMatch := titleLower == queryLower || titleNormalized == queryNormalized

		logger.Info("calculating confidence score",
			logging.Int("result_index", idx),
			logging.Int64("tmdb_id", res.ID),
			logging.String("title", title),
			logging.String("title_normalized", titleNormalized),
			logging.Float64("calculated_score", score),
			logging.Float64("vote_average", res.VoteAverage),
			logging.Int64("vote_count", res.VoteCount),
			logging.Bool("exact_title_match", exactMatch),
			logging.String("match_type", matchType(titleLower, queryLower)))

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

	logger.Info("best result before confidence thresholds",
		logging.Int64("tmdb_id", best.ID),
		logging.String("title", title),
		logging.String("title_normalized", titleNormalized),
		logging.Float64("best_score", bestScore),
		logging.Float64("vote_average", best.VoteAverage),
		logging.Int64("vote_count", best.VoteCount),
		logging.Bool("exact_title_match", exactMatch))

	if exactMatch {
		if best.VoteAverage < 2.0 {
			logger.Warn("exact match rejected: vote average too low",
				logging.Float64("vote_average", best.VoteAverage),
				logging.Float64("threshold", 2.0))
			return nil
		}
		logger.Info("exact match accepted: confidence passed",
			logging.Float64("vote_average", best.VoteAverage),
			logging.Float64("threshold", 2.0))
		return best
	}

	if best.VoteAverage < 3.0 {
		logger.Warn("partial match rejected: vote average too low",
			logging.Float64("vote_average", best.VoteAverage),
			logging.Float64("threshold", 3.0))
		return nil
	}

	minExpectedScore := 1.3 + float64(best.VoteCount)/1000.0
	if bestScore < minExpectedScore {
		logger.Warn("partial match rejected: confidence score too low",
			logging.Float64("best_score", bestScore),
			logging.Float64("min_expected_score", minExpectedScore),
			logging.String("formula", "1.3 + (vote_count/1000.0)"))
		return nil
	}

	logger.Info("partial match accepted: confidence passed",
		logging.Float64("best_score", bestScore),
		logging.Float64("min_expected_score", minExpectedScore))

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
