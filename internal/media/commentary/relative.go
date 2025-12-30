package commentary

import (
	"math"
	"sort"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/logging"
)

func indicesFromDecisions(decisions []Decision) []int {
	indices := make([]int, 0)
	for _, decision := range decisions {
		if decision.Include {
			indices = append(indices, decision.Index)
		}
	}
	sort.Ints(indices)
	return indices
}

func applyRelativeScoring(cfg config.CommentaryDetection, decisions []Decision, logger *slog.Logger) []Decision {
	if len(decisions) < 2 {
		return decisions
	}
	included := 0
	ambiguous := make([]int, 0)
	for idx, decision := range decisions {
		if decision.Include {
			included++
		}
		if !decision.Include && decision.Reason == "ambiguous" {
			ambiguous = append(ambiguous, idx)
		}
	}
	if included > 0 || len(ambiguous) < 2 {
		return decisions
	}

	maxSilence := -1.0
	secondSilence := -1.0
	maxSilenceIdx := -1

	minSimilarity := math.MaxFloat64
	secondMinSimilarity := math.MaxFloat64
	minSimilarityIdx := -1

	for _, idx := range ambiguous {
		silence := decisions[idx].Metrics.SpeechInPrimarySilence
		if silence > maxSilence {
			secondSilence = maxSilence
			maxSilence = silence
			maxSilenceIdx = idx
		} else if silence > secondSilence {
			secondSilence = silence
		}

		similarity := decisions[idx].Metrics.FingerprintSimilarity
		if similarity < minSimilarity {
			secondMinSimilarity = minSimilarity
			minSimilarity = similarity
			minSimilarityIdx = idx
		} else if similarity < secondMinSimilarity {
			secondMinSimilarity = similarity
		}
	}

	if maxSilenceIdx < 0 || minSimilarityIdx < 0 || maxSilenceIdx != minSimilarityIdx {
		return decisions
	}
	if secondSilence < 0 || secondMinSimilarity == math.MaxFloat64 {
		return decisions
	}

	silenceDelta := maxSilence - secondSilence
	similarityDelta := secondMinSimilarity - minSimilarity
	if maxSilence < cfg.SpeechInSilenceMax ||
		silenceDelta < minRelativeSilenceDelta ||
		similarityDelta < minRelativeSimilarityDelta {
		return decisions
	}

	for _, idx := range ambiguous {
		if idx == maxSilenceIdx {
			decisions[idx].Include = false
			decisions[idx].Reason = "audio_description_outlier"
		} else {
			decisions[idx].Include = true
			decisions[idx].Reason = "commentary_relative"
		}
	}

	if logger != nil {
		logger.Debug("commentary relative scoring",
			logging.Int("outlier_stream", decisions[maxSilenceIdx].Index),
			logging.Float64("silence_delta", silenceDelta),
			logging.Float64("similarity_delta", similarityDelta),
		)
	}

	return decisions
}
