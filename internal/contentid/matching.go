package contentid

import (
	"math"
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

type blockRefinement struct {
	BlockStart   int
	BlockEnd     int
	Displaced    int
	Gaps         int
	Reassigned   int
	NeedsReview  bool
	ReviewReason string
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

func resolveEpisodeMatches(rips []ripFingerprint, refs []referenceFingerprint, minScore float64) []matchResult {
	if len(rips) == 0 || len(refs) == 0 {
		return nil
	}
	n := len(rips)
	m := len(refs)
	size := maxInt(n, m)
	const padCost = 2.0
	cost := make([][]float64, size)
	scores := make([][]float64, size)
	for i := 0; i < size; i++ {
		cost[i] = make([]float64, size)
		scores[i] = make([]float64, size)
		for j := 0; j < size; j++ {
			cost[i][j] = padCost
		}
	}
	for i, rip := range rips {
		for j, ref := range refs {
			score := textSimilarity(rip.Vector, ref.Vector)
			scores[i][j] = score
			if score <= 0 {
				continue
			}
			c := 1.0 - score
			if c < 0 {
				c = 0
			}
			cost[i][j] = c
		}
	}
	assign := hungarian(cost)
	results := make([]matchResult, 0, minInt(len(rips), len(refs)))
	for i, j := range assign {
		if i >= n || j < 0 || j >= m {
			continue
		}
		score := scores[i][j]
		if score < minScore {
			continue
		}
		runnerUpEpisode, runnerUpScore := bestAlternateReference(scores, refs, i, j)
		reverseRunnerUpKey, reverseRunnerUpScore := bestAlternateRip(scores, rips, i, j)
		scoreMargin := score - runnerUpScore
		reverseMargin := score - reverseRunnerUpScore
		results = append(results, matchResult{
			EpisodeKey:           rips[i].EpisodeKey,
			TitleID:              rips[i].TitleID,
			TargetEpisode:        refs[j].EpisodeNumber,
			Score:                score,
			ConfidenceQuality:    classifyConfidenceQuality(score, scoreMargin, reverseMargin),
			RunnerUpEpisode:      runnerUpEpisode,
			RunnerUpScore:        runnerUpScore,
			ScoreMargin:          scoreMargin,
			ReverseRunnerUpKey:   reverseRunnerUpKey,
			ReverseRunnerUpScore: reverseRunnerUpScore,
			ReverseScoreMargin:   reverseMargin,
			SubtitleFileID:       refs[j].FileID,
			SubtitleLanguage:     refs[j].Language,
			SubtitlePath:         refs[j].CachePath,
		})
	}
	return results
}

// hungarian solves the assignment problem for a square cost matrix (minimization).
// Returns assignment[row] = column, or -1 when unassigned.
func hungarian(cost [][]float64) []int {
	n := len(cost)
	if n == 0 {
		return nil
	}
	m := len(cost[0])
	if m != n {
		return nil
	}
	u := make([]float64, n+1)
	v := make([]float64, n+1)
	p := make([]int, n+1)
	way := make([]int, n+1)
	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		minv := make([]float64, n+1)
		used := make([]bool, n+1)
		for j := 0; j <= n; j++ {
			minv[j] = math.Inf(1)
		}
		for {
			used[j0] = true
			i0 := p[j0]
			delta := math.Inf(1)
			j1 := 0
			for j := 1; j <= n; j++ {
				if used[j] {
					continue
				}
				cur := cost[i0-1][j-1] - u[i0] - v[j]
				if cur < minv[j] {
					minv[j] = cur
					way[j] = j0
				}
				if minv[j] < delta {
					delta = minv[j]
					j1 = j
				}
			}
			for j := 0; j <= n; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
			if j0 == 0 {
				break
			}
		}
	}
	assign := make([]int, n)
	for i := range assign {
		assign[i] = -1
	}
	for j := 1; j <= n; j++ {
		if p[j] > 0 && p[j]-1 < n {
			assign[p[j]-1] = j - 1
		}
	}
	return assign
}

func refineMatchBlock(matches []matchResult, refs []referenceFingerprint, rips []ripFingerprint, totalSeasonEpisodes int, discNumber int, policy Policy) ([]matchResult, blockRefinement) {
	policy = policy.normalized()
	var info blockRefinement
	if len(matches) <= 1 {
		return matches, info
	}
	scores := make([]float64, len(matches))
	maxScore := 0.0
	for i, m := range matches {
		scores[i] = m.Score
		if m.Score > maxScore {
			maxScore = m.Score
		}
	}
	threshold := maxScore - policy.BlockHighConfidenceDelta
	sorted := make([]float64, len(scores))
	copy(sorted, scores)
	sort.Float64s(sorted)
	topIdx := len(sorted) - int(math.Ceil(float64(len(sorted))*policy.BlockHighConfidenceTopRatio))
	if topIdx < 0 {
		topIdx = 0
	}
	topThreshold := sorted[topIdx]
	if topThreshold > threshold {
		threshold = topThreshold
	}
	var highConf []matchResult
	for _, m := range matches {
		if m.Score >= threshold {
			highConf = append(highConf, m)
		}
	}
	if len(highConf) < 2 {
		return matches, info
	}
	hcMin, hcMax := highConf[0].TargetEpisode, highConf[0].TargetEpisode
	for _, m := range highConf[1:] {
		if m.TargetEpisode < hcMin {
			hcMin = m.TargetEpisode
		}
		if m.TargetEpisode > hcMax {
			hcMax = m.TargetEpisode
		}
	}
	numEpisodes := len(rips)
	validLow := hcMax - numEpisodes + 1
	if validLow < 1 {
		validLow = 1
	}
	validHigh := hcMin
	var blockStart int
	switch {
	case discNumber == 1:
		blockStart = 1
		if policy.Disc1MustStartAtEpisode1 && (1 < validLow || 1 > validHigh) {
			info.NeedsReview = true
			info.ReviewReason = "disc 1 anchor outside valid high-confidence range"
		}
	case discNumber >= 2:
		blockStart = validHigh
		highSet := make(map[int]struct{}, len(highConf))
		for _, m := range highConf {
			highSet[m.TargetEpisode] = struct{}{}
		}
		for _, m := range matches {
			if _, ok := highSet[m.TargetEpisode]; ok {
				continue
			}
			if m.TargetEpisode < hcMin {
				blockStart = validLow
				break
			}
			if m.TargetEpisode > hcMax {
				blockStart = validHigh
				break
			}
		}
		if blockStart < policy.Disc2PlusMinStartEpisode {
			blockStart = policy.Disc2PlusMinStartEpisode
		}
	default:
		blockStart = hcMin
	}
	blockEnd := blockStart + numEpisodes - 1
	if totalSeasonEpisodes > 0 && blockEnd > totalSeasonEpisodes {
		blockEnd = totalSeasonEpisodes
	}
	if blockStart < 1 {
		blockStart = 1
	}
	info.BlockStart = blockStart
	info.BlockEnd = blockEnd
	var valid, displaced []matchResult
	for _, m := range matches {
		if m.TargetEpisode >= blockStart && m.TargetEpisode <= blockEnd {
			valid = append(valid, m)
		} else {
			displaced = append(displaced, m)
		}
	}
	if len(displaced) == 0 {
		return matches, info
	}
	info.Displaced = len(displaced)
	validSet := make(map[int]struct{}, len(valid))
	for _, m := range valid {
		validSet[m.TargetEpisode] = struct{}{}
	}
	var gaps []int
	for ep := blockStart; ep <= blockEnd; ep++ {
		if _, ok := validSet[ep]; !ok {
			gaps = append(gaps, ep)
		}
	}
	info.Gaps = len(gaps)
	if len(gaps) == 0 {
		info.NeedsReview = true
		info.ReviewReason = "displaced matches with no gaps in block"
		return matches, info
	}
	if len(displaced) != len(gaps) {
		info.NeedsReview = true
		info.ReviewReason = "displaced count does not match gap count"
	}
	refByEp := make(map[int]*referenceFingerprint, len(refs))
	for i := range refs {
		refByEp[refs[i].EpisodeNumber] = &refs[i]
	}
	ripByKey := make(map[string]*ripFingerprint, len(rips))
	for i := range rips {
		ripByKey[rips[i].EpisodeKey] = &rips[i]
	}
	reassigned := make([]matchResult, 0, minInt(len(displaced), len(gaps)))
	if len(gaps) > 0 && len(displaced) > 0 {
		n := maxInt(len(displaced), len(gaps))
		cost := make([][]float64, n)
		scoreMatrix := make([][]float64, n)
		const padCost = 2.0
		for i := 0; i < n; i++ {
			cost[i] = make([]float64, n)
			scoreMatrix[i] = make([]float64, n)
			for j := 0; j < n; j++ {
				cost[i][j] = padCost
			}
		}
		for i := range displaced {
			rip := ripByKey[displaced[i].EpisodeKey]
			for j := range gaps {
				ref := refByEp[gaps[j]]
				var score float64
				if rip != nil && rip.Vector != nil && ref != nil && ref.Vector != nil {
					score = textSimilarity(rip.Vector, ref.Vector)
				}
				scoreMatrix[i][j] = score
				if score > 0 {
					cost[i][j] = 1.0 - score
				}
			}
		}
		assign := hungarian(cost)
		for i, j := range assign {
			if i >= len(displaced) || j < 0 || j >= len(gaps) {
				continue
			}
			m := displaced[i]
			m.TargetEpisode = gaps[j]
			m.Score = scoreMatrix[i][j]
			if ref := refByEp[gaps[j]]; ref != nil {
				m.SubtitleFileID = ref.FileID
				m.SubtitleLanguage = ref.Language
				m.SubtitlePath = ref.CachePath
			}
			reassigned = append(reassigned, m)
		}
	}
	info.Reassigned = len(reassigned)
	result := make([]matchResult, 0, len(valid)+len(reassigned))
	result = append(result, valid...)
	result = append(result, reassigned...)
	return result, info
}

func checkContiguity(matches []matchResult) bool {
	if len(matches) < 2 {
		return true
	}
	eps := make([]int, len(matches))
	for i, m := range matches {
		eps[i] = m.TargetEpisode
	}
	sort.Ints(eps)
	for i := 1; i < len(eps); i++ {
		if eps[i]-eps[i-1] != 1 {
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

func classifyConfidenceQuality(score, scoreMargin, reverseMargin float64) string {
	switch {
	case score >= 0.75 && scoreMargin >= 0.05 && reverseMargin >= 0.05:
		return "clear"
	case score < DefaultPolicy().LowConfidenceReviewThreshold || (scoreMargin < 0.02 && reverseMargin < 0.02):
		return "contested"
	default:
		return "ambiguous"
	}
}

func textSimilarity(a, b *textutil.Fingerprint) float64 {
	return textutil.CosineSimilarity(a, b)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
