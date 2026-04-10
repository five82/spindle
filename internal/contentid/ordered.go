package contentid

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type decodeDiagnostics struct {
	WindowStart        int
	WindowEnd          int
	Orientation        string
	PathScore          float64
	SecondPathScore    float64
	PathMargin         float64
	InternalGapCount   int
	SkippedRefCount    int
	UnresolvedCount    int
	MatchedCount       int
	SequenceContiguous bool
	NeedsReview        bool
	ReviewReason       string
}

type orderedPath struct {
	WindowStart      int
	WindowEnd        int
	Orientation      string
	Score            float64
	InternalGapCount int
	SkippedRefCount  int
	UnresolvedCount  int
	Matches          []orderedAssignment
}

type orderedAssignment struct {
	RipIndex int
	RefIndex int
}

type windowCandidate struct {
	Start int
	End   int
}

type pathCell struct {
	Valid      bool
	Score      float64
	Matched    int
	SkippedRef int
	Unresolved int
	PrevI      int
	PrevJ      int
	Op         byte
}

const (
	pathOpNone       byte = 0
	pathOpMatch      byte = 1
	pathOpSkipRef    byte = 2
	pathOpUnresolved byte = 3
)

func decodeOrderedEpisodeMatches(rips []ripFingerprint, refs []referenceFingerprint, discNumber, totalSeasonEpisodes int, policy Policy) ([]matchResult, decodeDiagnostics) {
	policy = policy.normalized()
	if len(rips) == 0 || len(refs) == 0 {
		return nil, decodeDiagnostics{}
	}

	refs = sortedReferences(refs)
	windows := enumerateCandidateWindows(len(rips), refs, policy)
	if len(windows) == 0 {
		return nil, decodeDiagnostics{}
	}

	var best orderedPath
	bestValid := false
	var second orderedPath
	secondValid := false
	for _, window := range windows {
		forward := decodeOrderedWindowPath(rips, refs[window.Start:window.End], policy, "forward")
		if betterOrderedPath(forward, best) {
			if bestValid {
				second = best
				secondValid = true
			}
			best = forward
			bestValid = len(forward.Matches) > 0
		} else if betterOrderedPath(forward, second) {
			second = forward
			secondValid = len(forward.Matches) > 0
		}

		reversed := reverseReferences(refs[window.Start:window.End])
		reverse := decodeOrderedWindowPath(rips, reversed, policy, "reverse")
		if betterOrderedPath(reverse, best) {
			if bestValid {
				second = best
				secondValid = true
			}
			best = reverse
			bestValid = len(reverse.Matches) > 0
		} else if betterOrderedPath(reverse, second) {
			second = reverse
			secondValid = len(reverse.Matches) > 0
		}
	}
	if !bestValid || len(best.Matches) == 0 {
		return nil, decodeDiagnostics{}
	}

	matches := annotateOrderedMatches(rips, refs, best, second, policy)
	if len(matches) == 0 {
		return nil, decodeDiagnostics{}
	}

	diag := decodeDiagnostics{
		WindowStart:        best.WindowStart,
		WindowEnd:          best.WindowEnd,
		Orientation:        best.Orientation,
		PathScore:          best.Score,
		InternalGapCount:   best.InternalGapCount,
		SkippedRefCount:    best.SkippedRefCount,
		UnresolvedCount:    best.UnresolvedCount,
		MatchedCount:       len(matches),
		SequenceContiguous: checkContiguity(matches),
	}
	if secondValid {
		diag.SecondPathScore = second.Score
	}
	diag.PathMargin = best.Score - diag.SecondPathScore
	if diag.PathMargin < 0 {
		diag.PathMargin = 0
	}
	if discNumber == 1 && policy.Disc1MustStartAtEpisode1 && best.WindowStart > 1 {
		diag.NeedsReview = true
		diag.ReviewReason = fmt.Sprintf("disc 1 ordered path starts at episode %d", best.WindowStart)
	}
	if !diag.SequenceContiguous {
		diag.NeedsReview = true
		if diag.ReviewReason == "" {
			diag.ReviewReason = "ordered path is non-contiguous"
		}
	}
	if totalSeasonEpisodes > 0 && best.WindowEnd > totalSeasonEpisodes {
		diag.NeedsReview = true
		if diag.ReviewReason == "" {
			diag.ReviewReason = "ordered path exceeds season episode count"
		}
	}
	for i := range matches {
		matches[i].PathScore = diag.PathScore
		matches[i].PathMargin = diag.PathMargin
		matches[i].InternalGapCount = diag.InternalGapCount
		matches[i].UnresolvedCount = diag.UnresolvedCount
		matches[i].SequenceContiguous = diag.SequenceContiguous
		matches[i].WindowStart = diag.WindowStart
		matches[i].WindowEnd = diag.WindowEnd
		matches[i].Orientation = diag.Orientation
		if diag.NeedsReview {
			matches[i].NeedsVerification = true
			if matches[i].VerificationReason == "" {
				matches[i].VerificationReason = diag.ReviewReason
			}
		}
	}
	return matches, diag
}

func enumerateCandidateWindows(ripCount int, refs []referenceFingerprint, policy Policy) []windowCandidate {
	if ripCount <= 0 || len(refs) == 0 {
		return nil
	}
	sizes := candidateWindowSizes(ripCount, len(refs), policy)
	windows := make([]windowCandidate, 0, len(refs)*len(sizes))
	for _, size := range sizes {
		for start := 0; start+size <= len(refs); start++ {
			end := start + size
			if !referencesContiguous(refs[start:end]) {
				continue
			}
			windows = append(windows, windowCandidate{Start: start, End: end})
		}
	}
	return windows
}

func candidateWindowSizes(ripCount, refCount int, policy Policy) []int {
	sizes := []int{ripCount - 1, ripCount, ripCount + 1, ripCount + policy.WindowMaxSlack}
	seen := make(map[int]struct{}, len(sizes))
	out := make([]int, 0, len(sizes))
	for _, size := range sizes {
		if size < 1 {
			continue
		}
		if size > refCount {
			size = refCount
		}
		if _, ok := seen[size]; ok {
			continue
		}
		seen[size] = struct{}{}
		out = append(out, size)
	}
	sort.Ints(out)
	return out
}

func referencesContiguous(refs []referenceFingerprint) bool {
	if len(refs) < 2 {
		return true
	}
	for i := 1; i < len(refs); i++ {
		if refs[i].EpisodeNumber-refs[i-1].EpisodeNumber != 1 {
			return false
		}
	}
	return true
}

func sortedReferences(refs []referenceFingerprint) []referenceFingerprint {
	out := append([]referenceFingerprint(nil), refs...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].EpisodeNumber < out[j].EpisodeNumber
	})
	return out
}

func reverseReferences(refs []referenceFingerprint) []referenceFingerprint {
	out := append([]referenceFingerprint(nil), refs...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func decodeOrderedWindowPath(rips []ripFingerprint, refs []referenceFingerprint, policy Policy, orientation string) orderedPath {
	if len(rips) == 0 || len(refs) == 0 {
		return orderedPath{}
	}
	n := len(rips)
	m := len(refs)
	dp := make([][]pathCell, n+1)
	for i := range dp {
		dp[i] = make([]pathCell, m+1)
	}
	dp[0][0] = pathCell{Valid: true}
	for i := 0; i <= n; i++ {
		for j := 0; j <= m; j++ {
			if !dp[i][j].Valid {
				continue
			}
			if i < n && j < m {
				rawScore := textSimilarity(rips[i].Vector, refs[j].Vector)
				cand := dp[i][j]
				cand.Score += emissionScore(rawScore, policy)
				cand.Matched++
				cand.Valid = true
				cand.PrevI = i
				cand.PrevJ = j
				cand.Op = pathOpMatch
				updatePathCell(&dp[i+1][j+1], cand)
			}
			if j < m {
				cand := dp[i][j]
				cand.Score -= policy.ReferenceSkipPenalty
				cand.SkippedRef++
				cand.Valid = true
				cand.PrevI = i
				cand.PrevJ = j
				cand.Op = pathOpSkipRef
				updatePathCell(&dp[i][j+1], cand)
			}
			if i < n {
				cand := dp[i][j]
				cand.Score -= policy.UnresolvedPenalty
				cand.Unresolved++
				cand.Valid = true
				cand.PrevI = i
				cand.PrevJ = j
				cand.Op = pathOpUnresolved
				updatePathCell(&dp[i+1][j], cand)
			}
		}
	}
	cell := dp[n][m]
	if !cell.Valid || cell.Matched == 0 {
		return orderedPath{}
	}

	assignments := make([]orderedAssignment, 0, cell.Matched)
	i, j := n, m
	for i > 0 || j > 0 {
		current := dp[i][j]
		switch current.Op {
		case pathOpMatch:
			assignments = append(assignments, orderedAssignment{RipIndex: i - 1, RefIndex: j - 1})
			i, j = current.PrevI, current.PrevJ
		case pathOpSkipRef, pathOpUnresolved:
			i, j = current.PrevI, current.PrevJ
		default:
			i, j = 0, 0
		}
	}
	for left, right := 0, len(assignments)-1; left < right; left, right = left+1, right-1 {
		assignments[left], assignments[right] = assignments[right], assignments[left]
	}

	path := orderedPath{
		WindowStart:     minRefEpisode(refs),
		WindowEnd:       maxRefEpisode(refs),
		Orientation:     orientation,
		Score:           cell.Score,
		SkippedRefCount: cell.SkippedRef,
		UnresolvedCount: cell.Unresolved,
		Matches:         assignments,
	}
	path.InternalGapCount = internalGapCount(assignments, refs)
	return path
}

func updatePathCell(dst *pathCell, cand pathCell) {
	if betterPathCell(cand, *dst) {
		*dst = cand
	}
}

func betterPathCell(candidate, current pathCell) bool {
	if !candidate.Valid {
		return false
	}
	if !current.Valid {
		return true
	}
	if candidate.Score != current.Score {
		return candidate.Score > current.Score
	}
	if candidate.Matched != current.Matched {
		return candidate.Matched > current.Matched
	}
	if candidate.Unresolved != current.Unresolved {
		return candidate.Unresolved < current.Unresolved
	}
	if candidate.SkippedRef != current.SkippedRef {
		return candidate.SkippedRef < current.SkippedRef
	}
	return false
}

func betterOrderedPath(candidate, current orderedPath) bool {
	if len(candidate.Matches) == 0 {
		return false
	}
	if len(current.Matches) == 0 {
		return true
	}
	if len(candidate.Matches) != len(current.Matches) {
		return len(candidate.Matches) > len(current.Matches)
	}
	if candidate.UnresolvedCount != current.UnresolvedCount {
		return candidate.UnresolvedCount < current.UnresolvedCount
	}
	if candidate.InternalGapCount != current.InternalGapCount {
		return candidate.InternalGapCount < current.InternalGapCount
	}
	if candidate.Score != current.Score {
		return candidate.Score > current.Score
	}
	if candidate.SkippedRefCount != current.SkippedRefCount {
		return candidate.SkippedRefCount < current.SkippedRefCount
	}
	return candidate.WindowStart < current.WindowStart
}

func emissionScore(rawScore float64, policy Policy) float64 {
	return rawScore - policy.MinSimilarityScore
}

func internalGapCount(assignments []orderedAssignment, refs []referenceFingerprint) int {
	if len(assignments) < 2 {
		return 0
	}
	gaps := 0
	for i := 1; i < len(assignments); i++ {
		prev := refs[assignments[i-1].RefIndex].EpisodeNumber
		curr := refs[assignments[i].RefIndex].EpisodeNumber
		gap := abs(curr-prev) - 1
		if gap > 0 {
			gaps += gap
		}
	}
	return gaps
}

func minRefEpisode(refs []referenceFingerprint) int {
	if len(refs) == 0 {
		return 0
	}
	minEpisode := refs[0].EpisodeNumber
	for _, ref := range refs[1:] {
		if ref.EpisodeNumber < minEpisode {
			minEpisode = ref.EpisodeNumber
		}
	}
	return minEpisode
}

func maxRefEpisode(refs []referenceFingerprint) int {
	if len(refs) == 0 {
		return 0
	}
	maxEpisode := refs[0].EpisodeNumber
	for _, ref := range refs[1:] {
		if ref.EpisodeNumber > maxEpisode {
			maxEpisode = ref.EpisodeNumber
		}
	}
	return maxEpisode
}

func annotateOrderedMatches(rips []ripFingerprint, refs []referenceFingerprint, best, second orderedPath, policy Policy) []matchResult {
	if len(best.Matches) == 0 {
		return nil
	}
	scoreMatrix := buildScoreMatrix(rips, refs)
	windowRefs := refsByEpisode(refs, best.WindowStart, best.WindowEnd, best.Orientation)
	results := make([]matchResult, 0, len(best.Matches))
	for _, assignment := range best.Matches {
		rip := rips[assignment.RipIndex]
		if assignment.RefIndex < 0 || assignment.RefIndex >= len(windowRefs) {
			continue
		}
		ref := windowRefs[assignment.RefIndex]
		globalRefIndex := referenceIndexByEpisode(refs, ref.EpisodeNumber)
		if globalRefIndex < 0 {
			continue
		}
		rawScore := scoreMatrix[assignment.RipIndex][globalRefIndex]
		runnerUpEpisode, runnerUpScore := bestAlternateReference(scoreMatrix, refs, assignment.RipIndex, globalRefIndex)
		reverseRunnerUpKey, reverseRunnerUpScore := bestAlternateRip(scoreMatrix, rips, assignment.RipIndex, globalRefIndex)
		neighborEpisode, neighborScore := bestNeighborReference(scoreMatrix, refs, assignment.RipIndex, ref.EpisodeNumber)
		scoreMargin := rawScore - runnerUpScore
		reverseMargin := rawScore - reverseRunnerUpScore
		neighborMargin := rawScore - neighborScore
		pathMargin := best.Score - second.Score
		if pathMargin < 0 {
			pathMargin = 0
		}
		confidence, quality, needsVerify, verifyReason := deriveMatchConfidence(rawScore, scoreMargin, reverseMargin, neighborMargin, pathMargin, best, policy)
		results = append(results, matchResult{
			EpisodeKey:              rip.EpisodeKey,
			TitleID:                 rip.TitleID,
			TargetEpisode:           ref.EpisodeNumber,
			Score:                   rawScore,
			Confidence:              confidence,
			ConfidenceQuality:       quality,
			RunnerUpEpisode:         runnerUpEpisode,
			RunnerUpScore:           runnerUpScore,
			ScoreMargin:             scoreMargin,
			ReverseRunnerUpKey:      reverseRunnerUpKey,
			ReverseRunnerUpScore:    reverseRunnerUpScore,
			ReverseScoreMargin:      reverseMargin,
			NeighborRunnerUpEpisode: neighborEpisode,
			NeighborRunnerUpScore:   neighborScore,
			NeighborScoreMargin:     neighborMargin,
			PathScore:               best.Score,
			PathMargin:              pathMargin,
			InternalGapCount:        best.InternalGapCount,
			UnresolvedCount:         best.UnresolvedCount,
			SequenceContiguous:      best.InternalGapCount == 0,
			WindowStart:             best.WindowStart,
			WindowEnd:               best.WindowEnd,
			Orientation:             best.Orientation,
			NeedsVerification:       needsVerify,
			VerificationReason:      verifyReason,
			SubtitleFileID:          ref.FileID,
			SubtitleLanguage:        ref.Language,
			SubtitlePath:            ref.CachePath,
		})
	}
	return results
}

func refsByEpisode(refs []referenceFingerprint, start, end int, orientation string) []referenceFingerprint {
	window := make([]referenceFingerprint, 0, len(refs))
	for _, ref := range refs {
		if ref.EpisodeNumber >= min(start, end) && ref.EpisodeNumber <= max(start, end) {
			window = append(window, ref)
		}
	}
	sort.Slice(window, func(i, j int) bool {
		return window[i].EpisodeNumber < window[j].EpisodeNumber
	})
	if orientation == "reverse" {
		window = reverseReferences(window)
	}
	return window
}

func referenceIndexByEpisode(refs []referenceFingerprint, episode int) int {
	for i, ref := range refs {
		if ref.EpisodeNumber == episode {
			return i
		}
	}
	return -1
}

func bestNeighborReference(scores [][]float64, refs []referenceFingerprint, ripIdx, episode int) (int, float64) {
	neighborScore := 0.0
	neighborEpisode := 0
	for _, candidate := range []int{episode - 1, episode + 1} {
		refIdx := referenceIndexByEpisode(refs, candidate)
		if refIdx < 0 {
			continue
		}
		if scores[ripIdx][refIdx] > neighborScore {
			neighborScore = scores[ripIdx][refIdx]
			neighborEpisode = candidate
		}
	}
	return neighborEpisode, neighborScore
}

func buildScoreMatrix(rips []ripFingerprint, refs []referenceFingerprint) [][]float64 {
	matrix := make([][]float64, len(rips))
	for i := range rips {
		matrix[i] = make([]float64, len(refs))
		for j := range refs {
			matrix[i][j] = textSimilarity(rips[i].Vector, refs[j].Vector)
		}
	}
	return matrix
}

func deriveMatchConfidence(score, scoreMargin, reverseMargin, neighborMargin, pathMargin float64, path orderedPath, policy Policy) (float64, string, bool, string) {
	confidence := score
	reasons := make([]string, 0, 6)
	confidence -= marginPenalty(scoreMargin, policy.ScoreMarginTarget, 0.18, "score_margin", &reasons)
	confidence -= marginPenalty(reverseMargin, policy.ReverseMarginTarget, 0.15, "reverse_margin", &reasons)
	confidence -= marginPenalty(neighborMargin, policy.NeighborMarginTarget, 0.24, "neighbor_margin", &reasons)
	confidence -= marginPenalty(pathMargin, policy.PathMarginTarget, 0.16, "path_margin", &reasons)
	if path.InternalGapCount > 0 {
		confidence -= min(0.18, 0.08*float64(path.InternalGapCount))
		reasons = append(reasons, "internal_gap")
	}
	if path.UnresolvedCount > 0 {
		confidence -= min(0.12, 0.04*float64(path.UnresolvedCount))
		reasons = append(reasons, "unresolved_titles")
	}
	confidence = clamp01(confidence)
	quality := classifyDerivedConfidence(confidence, scoreMargin, reverseMargin, neighborMargin, pathMargin)
	needsVerify := confidence < policy.LLMVerifyThreshold || neighborMargin < policy.VerifyNeighborMargin || pathMargin < policy.VerifyPathMargin || path.InternalGapCount > 0
	return confidence, quality, needsVerify, strings.Join(reasons, ",")
}

func marginPenalty(margin, target, weight float64, label string, reasons *[]string) float64 {
	if target <= 0 || margin >= target {
		return 0
	}
	if reasons != nil {
		*reasons = append(*reasons, label)
	}
	return weight * (target - max(0, margin)) / target
}

func classifyDerivedConfidence(confidence, scoreMargin, reverseMargin, neighborMargin, pathMargin float64) string {
	switch {
	case confidence >= 0.85 && scoreMargin >= 0.05 && reverseMargin >= 0.05 && neighborMargin >= 0.03 && pathMargin >= 0.10:
		return "clear"
	case confidence < DefaultPolicy().LowConfidenceReviewThreshold || neighborMargin < 0.02 || pathMargin < 0.05:
		return "contested"
	default:
		return "ambiguous"
	}
}

func clamp01(value float64) float64 {
	return math.Max(0, math.Min(1, value))
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
