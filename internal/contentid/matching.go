package contentid

import (
	"math"
	"sort"
	"strings"
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

// selectAnchorWindow tries a first-anchor and then second-anchor strategy.
// When successful, it returns a contiguous episode window derived from the
// confident anchor episode.
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
		score := cosineSimilarity(rip.Vector, ref.Vector)
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

// resolveEpisodeMatches computes the maximum-weight bipartite matching between ripped
// episode transcripts and OpenSubtitles references using the Hungarian algorithm.
// This avoids greedy mis-assignments when multiple pairs have very similar scores.
func resolveEpisodeMatches(rips []ripFingerprint, refs []referenceFingerprint, minScore float64) []matchResult {
	if len(rips) == 0 || len(refs) == 0 {
		return nil
	}

	n := len(rips)
	m := len(refs)
	size := max(n, m)

	// Build cost matrix for minimization; cost = 1 - similarity (bounded to [0,1]).
	// Padded rows/cols use a high cost so they won't be chosen unless necessary.
	const padCost = 2.0
	cost := make([][]float64, size)
	scores := make([][]float64, size)
	for i := range size {
		cost[i] = make([]float64, size)
		scores[i] = make([]float64, size)
		for j := range size {
			cost[i][j] = padCost
		}
	}
	for i, rip := range rips {
		for j, ref := range refs {
			score := cosineSimilarity(rip.Vector, ref.Vector)
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

	results := make([]matchResult, 0, min(len(rips), len(refs)))
	for i, j := range assign {
		if i >= n || j < 0 || j >= m {
			continue
		}
		score := scores[i][j]
		if score < minScore {
			continue
		}
		results = append(results, matchResult{
			EpisodeKey:        rips[i].EpisodeKey,
			TitleID:           rips[i].TitleID,
			TargetEpisode:     refs[j].EpisodeNumber,
			Score:             score,
			SubtitleFileID:    refs[j].FileID,
			SubtitleLanguage:  refs[j].Language,
			SubtitleCachePath: refs[j].CachePath,
		})
	}
	return results
}

// hungarian solves the assignment problem for a square cost matrix (minimization).
// Returns a slice assignment[i] = column index chosen for row i, or -1 if unassigned.
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

// blockRefinement describes what refineMatchBlock changed.
type blockRefinement struct {
	BlockStart   int
	BlockEnd     int
	Displaced    int
	Gaps         int
	Reassigned   int
	NeedsReview  bool
	ReviewReason string
}

// refineMatchBlock enforces a contiguous block constraint on episode matches.
// TV disc episodes should map to a contiguous range (e.g. E01-E12). When
// high-confidence matches establish a block, outliers (matches outside the block)
// are reassigned to gap positions within the block.
//
// Returns the refined matches and a refinement summary. If no refinement is
// needed (all matches already contiguous, or insufficient data), the original
// matches are returned unchanged.
func refineMatchBlock(matches []matchResult, refs []referenceFingerprint, rips []ripFingerprint, totalSeasonEpisodes int, discNumber int, policy Policy) ([]matchResult, blockRefinement) {
	policy = policy.normalized()
	var info blockRefinement

	// Skip refinement for trivial cases.
	if len(matches) <= 1 {
		return matches, info
	}

	// Determine high-confidence matches to establish the expected block.
	// High confidence = within 0.05 of max score, or top 70%, whichever is
	// more selective (fewer episodes).
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
	top70Idx := len(sorted) - int(math.Ceil(float64(len(sorted))*policy.BlockHighConfidenceTopRatio))
	if top70Idx < 0 {
		top70Idx = 0
	}
	top70Threshold := sorted[top70Idx]

	// Use whichever threshold is more selective (higher).
	if top70Threshold > threshold {
		threshold = top70Threshold
	}

	var highConf []matchResult
	for _, m := range matches {
		if m.Score >= threshold {
			highConf = append(highConf, m)
		}
	}

	if len(highConf) < 2 {
		// Not enough high-confidence matches to establish a block.
		return matches, info
	}

	// Determine high-confidence range.
	hcMin := highConf[0].TargetEpisode
	hcMax := highConf[0].TargetEpisode
	for _, m := range highConf[1:] {
		if m.TargetEpisode < hcMin {
			hcMin = m.TargetEpisode
		}
		if m.TargetEpisode > hcMax {
			hcMax = m.TargetEpisode
		}
	}

	// Compute valid start range: the block of numEpisodes contiguous episodes
	// must contain all high-confidence matches.
	numEpisodes := len(rips)
	validLow := hcMax - numEpisodes + 1
	if validLow < 1 {
		validLow = 1
	}
	validHigh := hcMin

	// Select blockStart based on disc number.
	var blockStart int
	switch {
	case discNumber == 1:
		// Disc 1 hard rule: always start at episode 1.
		if policy.Disc1MustStartAtEpisode1 {
			blockStart = 1
			if 1 < validLow || 1 > validHigh {
				info.NeedsReview = true
				info.ReviewReason = "disc 1 anchor outside valid high-confidence range"
			}
		} else {
			blockStart = hcMin
		}
	case discNumber >= 2:
		// Use displaced matches' original targets as directional hints.
		blockStart = validHigh // default: expand upward (higher end of valid range)
		highConfSet := make(map[int]struct{}, len(highConf))
		for _, m := range highConf {
			highConfSet[m.TargetEpisode] = struct{}{}
		}
		for _, m := range matches {
			if _, ok := highConfSet[m.TargetEpisode]; ok {
				continue
			}
			if m.TargetEpisode < hcMin {
				// Displaced match pointed below high-conf range: expand downward.
				blockStart = validLow
				break
			}
			if m.TargetEpisode > hcMax {
				// Displaced match pointed above high-conf range: expand upward.
				blockStart = validHigh
				break
			}
		}
		// Hard constraint: disc 2+ cannot start at episode 1.
		if blockStart < policy.Disc2PlusMinStartEpisode {
			blockStart = policy.Disc2PlusMinStartEpisode
		}
	default:
		// Disc unknown (0): preserve existing upward-expansion behavior.
		blockStart = hcMin
	}

	blockEnd := blockStart + numEpisodes - 1

	// Clamp to season bounds.
	if totalSeasonEpisodes > 0 && blockEnd > totalSeasonEpisodes {
		blockEnd = totalSeasonEpisodes
	}
	if blockStart < 1 {
		blockStart = 1
	}

	info.BlockStart = blockStart
	info.BlockEnd = blockEnd

	// Partition matches: valid (in block) vs displaced (outside block).
	var valid, displaced []matchResult
	for _, m := range matches {
		if m.TargetEpisode >= blockStart && m.TargetEpisode <= blockEnd {
			valid = append(valid, m)
		} else {
			displaced = append(displaced, m)
		}
	}

	if len(displaced) == 0 {
		// All matches already in block — no refinement needed.
		return matches, info
	}
	info.Displaced = len(displaced)

	// Find gaps: episodes in the block with no valid match.
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
		// Block is fully covered but some matches point outside.
		// This is unusual — flag for review but keep original matches.
		info.NeedsReview = true
		info.ReviewReason = "displaced matches with no gaps in block"
		return matches, info
	}

	if len(displaced) != len(gaps) {
		// Mismatch between displaced and gaps — flag for review.
		info.NeedsReview = true
		info.ReviewReason = "displaced count does not match gap count"
	}

	// Reassign displaced to gaps using Hungarian matching when reference
	// fingerprints are available for gap episodes.
	refByEp := make(map[int]*referenceFingerprint, len(refs))
	for i := range refs {
		refByEp[refs[i].EpisodeNumber] = &refs[i]
	}

	// Build rip fingerprint lookup by episode key.
	ripByKey := make(map[string]*ripFingerprint, len(rips))
	for i := range rips {
		ripByKey[rips[i].EpisodeKey] = &rips[i]
	}

	// Check if we have reference fingerprints for gap episodes.
	var gapRefs []*referenceFingerprint
	for _, ep := range gaps {
		if ref, ok := refByEp[ep]; ok {
			gapRefs = append(gapRefs, ref)
		}
	}

	reassigned := make([]matchResult, 0, min(len(displaced), len(gaps)))

	if len(gapRefs) == len(gaps) && len(gaps) > 0 && len(displaced) > 0 {
		// Use Hungarian matching on displaced × gaps.
		n := max(len(displaced), len(gaps))
		costMatrix := make([][]float64, n)
		scoreMatrix := make([][]float64, n)
		const padCost = 2.0
		for i := range n {
			costMatrix[i] = make([]float64, n)
			scoreMatrix[i] = make([]float64, n)
			for j := range n {
				costMatrix[i][j] = padCost
			}
		}
		for i := range len(displaced) {
			rip := ripByKey[displaced[i].EpisodeKey]
			for j := range len(gaps) {
				var score float64
				if rip != nil && rip.Vector != nil && gapRefs[j] != nil && gapRefs[j].Vector != nil {
					score = cosineSimilarity(rip.Vector, gapRefs[j].Vector)
				}
				scoreMatrix[i][j] = score
				if score > 0 {
					costMatrix[i][j] = 1.0 - score
				}
			}
		}

		assign := hungarian(costMatrix)
		for i, j := range assign {
			if i >= len(displaced) || j < 0 || j >= len(gaps) {
				continue
			}
			m := displaced[i]
			m.TargetEpisode = gaps[j]
			m.Score = scoreMatrix[i][j]
			if gapRefs[j] != nil {
				m.SubtitleFileID = gapRefs[j].FileID
				m.SubtitleLanguage = gapRefs[j].Language
				m.SubtitleCachePath = gapRefs[j].CachePath
			}
			reassigned = append(reassigned, m)
		}
	} else {
		// No reference fingerprints for gaps — assign by position order.
		limit := min(len(displaced), len(gaps))
		for i := range limit {
			m := displaced[i]
			m.TargetEpisode = gaps[i]
			m.Score = 0 // no similarity data
			reassigned = append(reassigned, m)
		}
	}
	info.Reassigned = len(reassigned)

	// Build final result: valid matches + reassigned matches.
	result := make([]matchResult, 0, len(valid)+len(reassigned))
	result = append(result, valid...)
	result = append(result, reassigned...)
	return result, info
}
