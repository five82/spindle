package contentid

import (
	"sort"
)

const minSimilarityScore = 0.58

type matchCandidate struct {
	ripIdx int
	refIdx int
	score  float64
}

func resolveEpisodeMatches(rips []ripFingerprint, refs []referenceFingerprint) []matchResult {
	if len(rips) == 0 || len(refs) == 0 {
		return nil
	}
	pairs := make([]matchCandidate, 0, len(rips)*len(refs))
	for i, rip := range rips {
		for j, ref := range refs {
			score := cosineSimilarity(rip.Vector, ref.Vector)
			if score <= 0 {
				continue
			}
			pairs = append(pairs, matchCandidate{ripIdx: i, refIdx: j, score: score})
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].score > pairs[j].score
	})
	assignedRip := make([]bool, len(rips))
	assignedRef := make([]bool, len(refs))
	results := make([]matchResult, 0, min(len(rips), len(refs)))
	for _, candidate := range pairs {
		if assignedRip[candidate.ripIdx] || assignedRef[candidate.refIdx] {
			continue
		}
		if candidate.score < minSimilarityScore {
			continue
		}
		assignedRip[candidate.ripIdx] = true
		assignedRef[candidate.refIdx] = true
		results = append(results, matchResult{
			EpisodeKey:    rips[candidate.ripIdx].EpisodeKey,
			TitleID:       rips[candidate.ripIdx].TitleID,
			TargetEpisode: refs[candidate.refIdx].EpisodeNumber,
			Score:         candidate.score,
		})
	}
	return results
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
