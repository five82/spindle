package contentid

import (
	"math"
	"sort"
	"strings"

	"github.com/five82/spindle/internal/textutil"
)

const maxVerificationCandidatesPerRip = 2

type provisionalClaim struct {
	RipIndex int
	RefIndex int
	Match    matchResult
	Clear    bool
}

type matchResolution struct {
	Accepted            []matchResult
	PendingByRip        map[string][]matchResult
	UnresolvedKeys      []string
	ClearMatchCount     int
	AmbiguousCount      int
	ContestedCount      int
	SuspectReferenceCount int
}

func resolveEpisodeClaims(rips []ripFingerprint, refs []referenceFingerprint, policy Policy) matchResolution {
	policy = policy.normalized()
	if len(rips) == 0 || len(refs) == 0 {
		return matchResolution{}
	}

	weightedRips := cloneRipFingerprints(rips)
	weightedRefs := cloneReferenceFingerprints(sortedReferences(refs))
	applyIDFWeighting(weightedRips, weightedRefs)
	scores := buildScoreMatrix(weightedRips, weightedRefs)
	claims := buildClaims(rips, weightedRefs, scores, policy)
	if len(claims) == 0 {
		return matchResolution{UnresolvedKeys: unresolvedKeysFromRips(rips)}
	}

	sort.Slice(claims, func(i, j int) bool {
		if claims[i].Match.Strength != claims[j].Match.Strength {
			return claims[i].Match.Strength > claims[j].Match.Strength
		}
		if claims[i].Match.Score != claims[j].Match.Score {
			return claims[i].Match.Score > claims[j].Match.Score
		}
		if claims[i].Match.TargetEpisode != claims[j].Match.TargetEpisode {
			return claims[i].Match.TargetEpisode < claims[j].Match.TargetEpisode
		}
		return claims[i].Match.EpisodeKey < claims[j].Match.EpisodeKey
	})

	acceptedByRip := make(map[string]struct{}, len(rips))
	acceptedEpisodes := make(map[int]struct{}, len(refs))
	accepted := make([]matchResult, 0, len(rips))
	for _, claim := range claims {
		if !claim.Clear {
			continue
		}
		if _, ok := acceptedByRip[strings.ToLower(claim.Match.EpisodeKey)]; ok {
			continue
		}
		if _, ok := acceptedEpisodes[claim.Match.TargetEpisode]; ok {
			continue
		}
		match := claim.Match
		match.AcceptedBy = "clear_claim"
		match.NeedsVerification = false
		match.VerificationReason = ""
		accepted = append(accepted, match)
		acceptedByRip[strings.ToLower(match.EpisodeKey)] = struct{}{}
		acceptedEpisodes[match.TargetEpisode] = struct{}{}
	}

	pendingByRip := make(map[string][]matchResult)
	unresolved := make([]string, 0, len(rips))
	contested := 0
	ambiguous := 0
	for _, rip := range rips {
		key := strings.ToLower(rip.EpisodeKey)
		if _, ok := acceptedByRip[key]; ok {
			continue
		}
		candidates := topPendingClaimsForRip(claims, rip.EpisodeKey, acceptedEpisodes)
		if len(candidates) > 0 {
			pendingByRip[rip.EpisodeKey] = candidates
			ambiguous++
			if len(candidates) > 1 {
				contested++
			}
		}
		unresolved = append(unresolved, rip.EpisodeKey)
	}

	suspectRefCount := 0
	for _, ref := range weightedRefs {
		if ref.Suspect {
			suspectRefCount++
		}
	}

	return matchResolution{
		Accepted:              accepted,
		PendingByRip:          pendingByRip,
		UnresolvedKeys:        unresolved,
		ClearMatchCount:       len(accepted),
		AmbiguousCount:        ambiguous,
		ContestedCount:        contested,
		SuspectReferenceCount: suspectRefCount,
	}
}

func buildClaims(rips []ripFingerprint, refs []referenceFingerprint, scores [][]float64, policy Policy) []provisionalClaim {
	claims := make([]provisionalClaim, 0, len(rips)*len(refs))
	for i, rip := range rips {
		for j, ref := range refs {
			score := scores[i][j]
			if score < policy.MinSimilarityScore {
				continue
			}
			runnerUpEpisode, runnerUpScore := bestAlternateReference(scores, refs, i, j)
			episodeRunnerUpKey, episodeRunnerUpScore := bestAlternateRip(scores, rips, i, j)
			neighborEpisode, neighborScore := bestNeighborReference(scores, refs, i, ref.EpisodeNumber)
			ripMargin := score - runnerUpScore
			episodeMargin := score - episodeRunnerUpScore
			neighborMargin := score - neighborScore
			confidence, quality, needsVerify, verifyReason := deriveMatchConfidence(score, ripMargin, episodeMargin, neighborMargin, ref.Suspect, policy)
			match := matchResult{
				EpisodeKey:              rip.EpisodeKey,
				TitleID:                 rip.TitleID,
				TargetEpisode:           ref.EpisodeNumber,
				Score:                   score,
				Confidence:              confidence,
				ConfidenceQuality:       quality,
				RunnerUpEpisode:         runnerUpEpisode,
				RunnerUpScore:           runnerUpScore,
				ScoreMargin:             ripMargin,
				EpisodeRunnerUpKey:      episodeRunnerUpKey,
				EpisodeRunnerUpScore:    episodeRunnerUpScore,
				EpisodeScoreMargin:      episodeMargin,
				NeighborRunnerUpEpisode: neighborEpisode,
				NeighborRunnerUpScore:   neighborScore,
				NeighborScoreMargin:     neighborMargin,
				NeedsVerification:       needsVerify,
				VerificationReason:      verifyReason,
				SubtitleFileID:          ref.FileID,
				SubtitleLanguage:        ref.Language,
				SubtitlePath:            ref.CachePath,
				ReferenceSuspect:        ref.Suspect,
				ReferenceSuspectReason:  ref.SuspectReason,
			}
			match.Strength = claimStrength(match)
			claims = append(claims, provisionalClaim{
				RipIndex: i,
				RefIndex: j,
				Match:    match,
				Clear:    isClearClaim(match, policy),
			})
		}
	}
	return claims
}

func topPendingClaimsForRip(claims []provisionalClaim, episodeKey string, acceptedEpisodes map[int]struct{}) []matchResult {
	seenEpisodes := make(map[int]struct{})
	pending := make([]matchResult, 0, maxVerificationCandidatesPerRip)
	for _, claim := range claims {
		if !strings.EqualFold(claim.Match.EpisodeKey, episodeKey) {
			continue
		}
		if _, ok := acceptedEpisodes[claim.Match.TargetEpisode]; ok {
			continue
		}
		if _, ok := seenEpisodes[claim.Match.TargetEpisode]; ok {
			continue
		}
		seenEpisodes[claim.Match.TargetEpisode] = struct{}{}
		pending = append(pending, claim.Match)
		if len(pending) >= maxVerificationCandidatesPerRip {
			break
		}
	}
	return pending
}

func reconcileSingleHole(matches []matchResult, candidatesByRip map[string][]matchResult, refs []referenceFingerprint, policy Policy) ([]matchResult, bool) {
	remaining := unresolvedCandidateRips(matches, candidatesByRip)
	if len(remaining) != 1 {
		return matches, false
	}
	unresolvedKey := remaining[0]
	candidates := candidatesByRip[unresolvedKey]
	if len(candidates) == 0 {
		return matches, false
	}

	assigned := assignedEpisodes(matches)
	missingEpisode, ok := singleMissingEpisode(assigned)
	if !ok || !hasReferenceEpisode(refs, missingEpisode) {
		return matches, false
	}

	missingClaim, found := claimForEpisode(candidates, missingEpisode)
	if !found || missingClaim.ReferenceSuspect {
		return matches, false
	}
	if hasStrongContradiction(candidates, missingEpisode, missingClaim, policy) {
		return matches, false
	}

	reconciled := missingClaim
	reconciled.AcceptedBy = "single_hole_reconciliation"
	reconciled.Confidence = clamp01(reconciled.Confidence - 0.08)
	reconciled.ConfidenceQuality = classifyDerivedConfidence(reconciled.Confidence, reconciled.ScoreMargin, reconciled.EpisodeScoreMargin, reconciled.NeighborScoreMargin, reconciled.ReferenceSuspect)
	reconciled.NeedsVerification = false
	reconciled.VerificationReason = ""
	return append(matches, reconciled), true
}

func unresolvedCandidateRips(matches []matchResult, candidatesByRip map[string][]matchResult) []string {
	accepted := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		accepted[strings.ToLower(match.EpisodeKey)] = struct{}{}
	}
	keys := make([]string, 0, len(candidatesByRip))
	for key := range candidatesByRip {
		if _, ok := accepted[strings.ToLower(key)]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func assignedEpisodes(matches []matchResult) []int {
	episodes := make([]int, 0, len(matches))
	for _, match := range matches {
		if match.TargetEpisode > 0 {
			episodes = append(episodes, match.TargetEpisode)
		}
	}
	sort.Ints(episodes)
	return compactInts(episodes)
}

func singleMissingEpisode(episodes []int) (int, bool) {
	if len(episodes) < 2 {
		return 0, false
	}
	start := episodes[0]
	end := episodes[len(episodes)-1]
	if end-start+1 != len(episodes)+1 {
		return 0, false
	}
	missing := 0
	for episode := start; episode <= end; episode++ {
		if !containsInt(episodes, episode) {
			if missing != 0 {
				return 0, false
			}
			missing = episode
		}
	}
	if missing == 0 {
		return 0, false
	}
	return missing, true
}

func claimForEpisode(candidates []matchResult, episode int) (matchResult, bool) {
	for _, candidate := range candidates {
		if candidate.TargetEpisode == episode {
			return candidate, true
		}
	}
	return matchResult{}, false
}

func hasStrongContradiction(candidates []matchResult, missingEpisode int, missingClaim matchResult, policy Policy) bool {
	if len(candidates) == 0 {
		return false
	}
	best := candidates[0]
	if best.TargetEpisode == missingEpisode {
		return false
	}
	if best.Score >= missingClaim.Score+policy.ClearMatchMargin {
		return true
	}
	return best.Confidence >= policy.LLMVerifyThreshold && best.TargetEpisode != missingEpisode
}

func hasReferenceEpisode(refs []referenceFingerprint, episode int) bool {
	for _, ref := range refs {
		if ref.EpisodeNumber == episode {
			return true
		}
	}
	return false
}

func unresolvedKeysFromRips(rips []ripFingerprint) []string {
	keys := make([]string, 0, len(rips))
	for _, rip := range rips {
		keys = append(keys, rip.EpisodeKey)
	}
	return keys
}

func sortedReferences(refs []referenceFingerprint) []referenceFingerprint {
	out := append([]referenceFingerprint(nil), refs...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].EpisodeNumber < out[j].EpisodeNumber
	})
	return out
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

func deriveMatchConfidence(score, ripMargin, episodeMargin, neighborMargin float64, referenceSuspect bool, policy Policy) (float64, string, bool, string) {
	confidence := score
	reasons := make([]string, 0, 4)
	confidence -= marginPenalty(ripMargin, policy.ClearMatchMargin, 0.20, "rip_margin", &reasons)
	confidence -= marginPenalty(episodeMargin, policy.ClearMatchMargin, 0.18, "episode_margin", &reasons)
	confidence -= marginPenalty(neighborMargin, policy.ClearMatchMargin/2, 0.16, "neighbor_margin", &reasons)
	if referenceSuspect {
		confidence -= 0.18
		reasons = append(reasons, "suspect_reference")
	}
	confidence = clamp01(confidence)
	quality := classifyDerivedConfidence(confidence, ripMargin, episodeMargin, neighborMargin, referenceSuspect)
	needsVerify := referenceSuspect || confidence < policy.LLMVerifyThreshold || ripMargin < policy.ClearMatchMargin || episodeMargin < policy.ClearMatchMargin || neighborMargin < policy.ClearMatchMargin/2
	return confidence, quality, needsVerify, strings.Join(reasons, ",")
}

func isClearClaim(match matchResult, policy Policy) bool {
	return match.Score >= policy.MinSimilarityScore &&
		match.ScoreMargin >= policy.ClearMatchMargin &&
		match.EpisodeScoreMargin >= policy.ClearMatchMargin &&
		match.NeighborScoreMargin >= policy.ClearMatchMargin/2 &&
		!match.ReferenceSuspect &&
		match.Confidence >= policy.LLMVerifyThreshold
}

func claimStrength(match matchResult) float64 {
	strength := match.Confidence
	strength += 0.10 * math.Max(0, match.ScoreMargin)
	strength += 0.10 * math.Max(0, match.EpisodeScoreMargin)
	strength += 0.05 * math.Max(0, match.NeighborScoreMargin)
	if match.ReferenceSuspect {
		strength -= 0.15
	}
	return strength
}

func marginPenalty(margin, target, weight float64, label string, reasons *[]string) float64 {
	if target <= 0 || margin >= target {
		return 0
	}
	if reasons != nil {
		*reasons = append(*reasons, label)
	}
	return weight * (target - max(0.0, margin)) / target
}

func classifyDerivedConfidence(confidence, ripMargin, episodeMargin, neighborMargin float64, referenceSuspect bool) string {
	switch {
	case !referenceSuspect && confidence >= 0.85 && ripMargin >= 0.05 && episodeMargin >= 0.05 && neighborMargin >= 0.025:
		return "clear"
	case referenceSuspect || confidence < DefaultPolicy().LowConfidenceReviewThreshold || neighborMargin < 0.02:
		return "contested"
	default:
		return "ambiguous"
	}
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

func referenceIndexByEpisode(refs []referenceFingerprint, episode int) int {
	for i, ref := range refs {
		if ref.EpisodeNumber == episode {
			return i
		}
	}
	return -1
}

func textSimilarity(a, b *textutil.Fingerprint) float64 {
	return textutil.CosineSimilarity(a, b)
}

func clamp01(value float64) float64 {
	return math.Max(0, math.Min(1, value))
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
