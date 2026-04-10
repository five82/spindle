package contentid

import (
	"sort"
	"strings"

	"github.com/five82/spindle/internal/textutil"
)

type anchorSelection struct {
	RipIndex        int
	TargetEpisode   int
	BestScore       float64
	SecondBestScore float64
	ScoreMargin     float64
	WindowStart     int
	WindowEnd       int
	Reason          string
}

func selectAnchorWindow(rips []ripFingerprint, refs []referenceFingerprint, totalSeasonEpisodes int, minScore, minMargin float64) (anchorSelection, bool) {
	if len(rips) == 0 || len(refs) == 0 {
		return anchorSelection{Reason: "anchor_inputs_unavailable"}, false
	}
	indices := []int{0}
	if len(rips) > 1 {
		indices = append(indices, 1)
	}
	var last anchorSelection
	for _, idx := range indices {
		attempt, ok := evaluateAnchorSelection(rips, refs, idx, totalSeasonEpisodes, minScore, minMargin)
		if ok {
			return attempt, true
		}
		last = attempt
	}
	if strings.TrimSpace(last.Reason) == "" {
		last.Reason = "anchor_not_selected"
	}
	return last, false
}

func evaluateAnchorSelection(rips []ripFingerprint, refs []referenceFingerprint, ripIndex int, totalSeasonEpisodes int, minScore, minMargin float64) (anchorSelection, bool) {
	sel := anchorSelection{RipIndex: ripIndex}
	if ripIndex < 0 || ripIndex >= len(rips) {
		sel.Reason = "anchor_index_out_of_range"
		return sel, false
	}
	rip := rips[ripIndex]
	if rip.Vector == nil {
		sel.Reason = "anchor_vector_missing"
		return sel, false
	}
	bestScore := -1.0
	secondScore := -1.0
	bestEpisode := 0
	for _, ref := range refs {
		if ref.Vector == nil {
			continue
		}
		score := textSimilarity(rip.Vector, ref.Vector)
		if score > bestScore {
			secondScore = bestScore
			bestScore = score
			bestEpisode = ref.EpisodeNumber
			continue
		}
		if score > secondScore {
			secondScore = score
		}
	}
	if bestEpisode <= 0 {
		sel.Reason = "anchor_no_scored_references"
		return sel, false
	}
	if secondScore < 0 {
		secondScore = 0
	}
	sel.TargetEpisode = bestEpisode
	sel.BestScore = bestScore
	sel.SecondBestScore = secondScore
	sel.ScoreMargin = bestScore - secondScore
	if bestScore < minScore {
		sel.Reason = "anchor_score_below_threshold"
		return sel, false
	}
	if sel.ScoreMargin < minMargin {
		sel.Reason = "anchor_score_ambiguous"
		return sel, false
	}
	windowStart := bestEpisode - ripIndex
	if windowStart < 1 {
		windowStart = 1
	}
	if totalSeasonEpisodes > 0 {
		maxStart := totalSeasonEpisodes - len(rips) + 1
		if maxStart < 1 {
			maxStart = 1
		}
		if windowStart > maxStart {
			windowStart = maxStart
		}
	}
	windowEnd := windowStart + len(rips) - 1
	if totalSeasonEpisodes > 0 && windowEnd > totalSeasonEpisodes {
		windowEnd = totalSeasonEpisodes
	}
	sel.WindowStart = windowStart
	sel.WindowEnd = windowEnd
	if ripIndex == 0 {
		sel.Reason = "first_anchor"
	} else {
		sel.Reason = "second_anchor"
	}
	return sel, true
}

func checkContiguity(matches []matchResult) bool {
	if len(matches) < 2 {
		return true
	}
	episodes := make([]int, len(matches))
	for i, match := range matches {
		episodes[i] = match.TargetEpisode
	}
	sort.Ints(episodes)
	for i := 1; i < len(episodes); i++ {
		if episodes[i]-episodes[i-1] != 1 {
			return false
		}
	}
	return true
}

func bestAlternateReference(scores [][]float64, refs []referenceFingerprint, ripIdx, assignedRefIdx int) (int, float64) {
	bestIdx := -1
	bestScore := 0.0
	for j := range refs {
		if j == assignedRefIdx {
			continue
		}
		if scores[ripIdx][j] > bestScore {
			bestScore = scores[ripIdx][j]
			bestIdx = j
		}
	}
	if bestIdx < 0 {
		return 0, 0
	}
	return refs[bestIdx].EpisodeNumber, bestScore
}

func bestAlternateRip(scores [][]float64, rips []ripFingerprint, assignedRipIdx, refIdx int) (string, float64) {
	bestIdx := -1
	bestScore := 0.0
	for i := range rips {
		if i == assignedRipIdx {
			continue
		}
		if scores[i][refIdx] > bestScore {
			bestScore = scores[i][refIdx]
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return "", 0
	}
	return rips[bestIdx].EpisodeKey, bestScore
}

func textSimilarity(a, b *textutil.Fingerprint) float64 {
	return textutil.CosineSimilarity(a, b)
}
