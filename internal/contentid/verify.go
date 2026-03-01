package contentid

import (
	"context"
	"fmt"
	"log/slog"

	"spindle/internal/logging"
	"spindle/internal/services/llm"
	"spindle/internal/subtitles"
)

// llmVerifier abstracts the LLM JSON-completion call for testability.
type llmVerifier interface {
	CompleteJSON(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

const (
	middleWindowHalfSec = 300.0 // 5 min each side = 10 min window
	maxTranscriptChars  = 6000  // cap per transcript to stay within token budget
)

// verifyRejection pairs a match index with the rejected match details.
type verifyRejection struct {
	matchIdx int
	match    matchResult
}

// verifyResult summarises what LLM verification did across all candidates.
type verifyResult struct {
	Challenged   int
	Verified     int
	Rejected     int
	Rematched    int
	NeedsReview  bool
	ReviewReason string
}

// verifyMatches runs second-level LLM verification on matches scoring below
// verifyThreshold. It returns potentially updated matches and a summary.
//
// Escalation logic:
//   - 0 below threshold  -> skip verification entirely
//   - 1 rejection        -> needs_review (single disagreement isn't enough to re-match)
//   - 2+ rejections      -> cross-match rejected episodes against rejected references
//   - all rejected       -> needs_review
func verifyMatches(
	ctx context.Context,
	client llmVerifier,
	matches []matchResult,
	rips []ripFingerprint,
	refs []referenceFingerprint,
	logger *slog.Logger,
	verifyThreshold float64,
) ([]matchResult, *verifyResult) {
	if client == nil {
		return matches, nil
	}

	// Partition into confirmed and candidates.
	var candidates []int // indices into matches
	for i, m := range matches {
		if m.Score < verifyThreshold {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return matches, nil
	}

	vr := &verifyResult{Challenged: len(candidates)}

	// Verify each candidate.
	var rejections []verifyRejection

	for _, idx := range candidates {
		m := matches[idx]

		ripPath := findRipPath(rips, m.EpisodeKey)
		if ripPath == "" {
			logVerifySkip(logger, vr, m, "rip SRT not found; original cosine match retained", nil)
			continue
		}
		ref, ok := findRef(refs, m.TargetEpisode)
		if !ok || ref.CachePath == "" {
			logVerifySkip(logger, vr, m, "reference SRT not found; original cosine match retained", nil)
			continue
		}

		ev, err := verifySingleMatch(ctx, client, m, ripPath, ref.CachePath, logger)
		if err != nil {
			logVerifySkip(logger, vr, m, "LLM call failed; original cosine match retained", err)
			continue
		}
		if ev.SameEpisode {
			vr.Verified++
		} else {
			vr.Rejected++
			rejections = append(rejections, verifyRejection{matchIdx: idx, match: m})
		}
	}

	if len(rejections) == 0 {
		logVerifySummary(logger, vr)
		return matches, vr
	}

	// Single rejection -> needs_review, keep original matches.
	if len(rejections) == 1 {
		vr.NeedsReview = true
		vr.ReviewReason = fmt.Sprintf("LLM rejected match for %s (episode %d)",
			rejections[0].match.EpisodeKey, rejections[0].match.TargetEpisode)
		logVerifySummary(logger, vr)
		return matches, vr
	}

	// 2+ rejections -> attempt cross-match.
	rematched, allRejected := rematchRejected(ctx, client, rejections, rips, refs, logger)
	if allRejected {
		vr.NeedsReview = true
		vr.ReviewReason = "LLM rejected all cross-match combinations"
		logVerifySummary(logger, vr)
		return matches, vr
	}

	// Apply rematched assignments.
	result := make([]matchResult, len(matches))
	copy(result, matches)
	for _, rm := range rematched {
		for _, rej := range rejections {
			if rej.match.EpisodeKey == rm.EpisodeKey {
				result[rej.matchIdx] = rm
				vr.Rematched++
				break
			}
		}
	}

	logVerifySummary(logger, vr)
	return result, vr
}

// findRipPath returns the SRT path for the rip with the given episode key.
func findRipPath(rips []ripFingerprint, key string) string {
	for _, r := range rips {
		if r.EpisodeKey == key {
			return r.Path
		}
	}
	return ""
}

// findRef returns the reference fingerprint for the given episode number.
func findRef(refs []referenceFingerprint, episode int) (referenceFingerprint, bool) {
	for _, r := range refs {
		if r.EpisodeNumber == episode {
			return r, true
		}
	}
	return referenceFingerprint{}, false
}

// verifySingleMatch extracts the middle transcript from both the rip and reference
// SRT files, sends them to the LLM, and returns the parsed verification result.
func verifySingleMatch(
	ctx context.Context,
	client llmVerifier,
	match matchResult,
	ripPath, refPath string,
	logger *slog.Logger,
) (*EpisodeVerification, error) {
	ripText, err := extractMiddleTranscript(ripPath)
	if err != nil {
		return nil, fmt.Errorf("extract rip transcript: %w", err)
	}
	refText, err := extractMiddleTranscript(refPath)
	if err != nil {
		return nil, fmt.Errorf("extract reference transcript: %w", err)
	}

	userPrompt := buildVerificationPrompt(ripText, refText, match.EpisodeKey, match.TargetEpisode)
	raw, err := client.CompleteJSON(ctx, EpisodeVerificationPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call: %w", err)
	}

	var ev EpisodeVerification
	if err := llm.DecodeLLMJSON(raw, &ev); err != nil {
		return nil, fmt.Errorf("decode LLM response: %w", err)
	}

	result := "confirmed"
	if !ev.SameEpisode {
		result = "rejected"
	}
	logger.Info("episode LLM verification",
		logging.String(logging.FieldDecisionType, "episode_llm_verification"),
		logging.String("decision_result", result),
		logging.String("decision_reason", ev.Explanation),
		logging.String("episode_key", match.EpisodeKey),
		logging.Int("target_episode", match.TargetEpisode),
		logging.Float64("match_score", match.Score),
		logging.Float64("llm_confidence", ev.Confidence),
	)

	return &ev, nil
}

// rematchRejected performs N x M LLM comparisons across rejected episodes and
// their rejected references, then greedily assigns the best-confidence pairs.
// Returns the new match assignments and whether all combinations were rejected.
func rematchRejected(
	ctx context.Context,
	client llmVerifier,
	rejections []verifyRejection,
	rips []ripFingerprint,
	refs []referenceFingerprint,
	logger *slog.Logger,
) ([]matchResult, bool) {
	// Collect the rejected episode keys and target episodes.
	type candidate struct {
		episodeKey string
		ripPath    string
	}
	type reference struct {
		episode  int
		refPath  string
		fileID   int64
		language string
	}

	var candidates []candidate
	var references []reference
	seenEps := make(map[int]bool)

	for _, rej := range rejections {
		candidates = append(candidates, candidate{
			episodeKey: rej.match.EpisodeKey,
			ripPath:    findRipPath(rips, rej.match.EpisodeKey),
		})
		if !seenEps[rej.match.TargetEpisode] {
			seenEps[rej.match.TargetEpisode] = true
			ref, _ := findRef(refs, rej.match.TargetEpisode)
			references = append(references, reference{
				episode:  rej.match.TargetEpisode,
				refPath:  ref.CachePath,
				fileID:   ref.FileID,
				language: ref.Language,
			})
		}
	}

	// Pre-extract transcripts to avoid redundant file I/O in the N x M loop.
	ripTexts := make(map[string]string, len(candidates))
	for _, cand := range candidates {
		if cand.ripPath == "" {
			continue
		}
		text, err := extractMiddleTranscript(cand.ripPath)
		if err != nil {
			continue
		}
		ripTexts[cand.episodeKey] = text
	}
	refTexts := make(map[int]string, len(references))
	for _, ref := range references {
		if ref.refPath == "" {
			continue
		}
		text, err := extractMiddleTranscript(ref.refPath)
		if err != nil {
			continue
		}
		refTexts[ref.episode] = text
	}

	// Build confidence matrix: candidates x references.
	type scored struct {
		candidateIdx int
		referenceIdx int
		confidence   float64
		explanation  string
	}
	var accepted []scored

	for ci, cand := range candidates {
		ripText, ok := ripTexts[cand.episodeKey]
		if !ok {
			continue
		}
		for ri, ref := range references {
			refText, ok := refTexts[ref.episode]
			if !ok {
				continue
			}

			userPrompt := buildVerificationPrompt(ripText, refText, cand.episodeKey, ref.episode)
			raw, err := client.CompleteJSON(ctx, EpisodeVerificationPrompt, userPrompt)
			if err != nil {
				logger.Warn("LLM rematch call failed",
					logging.String(logging.FieldEventType, "llm_rematch_error"),
					logging.String(logging.FieldErrorHint, "cross-match LLM call failed; combination skipped"),
					logging.String(logging.FieldImpact, "rematch combination skipped"),
					logging.String("episode_key", cand.episodeKey),
					logging.Int("target_episode", ref.episode),
					logging.Error(err),
				)
				continue
			}
			var ev EpisodeVerification
			if err := llm.DecodeLLMJSON(raw, &ev); err != nil {
				continue
			}
			if ev.SameEpisode {
				accepted = append(accepted, scored{
					candidateIdx: ci,
					referenceIdx: ri,
					confidence:   ev.Confidence,
					explanation:  ev.Explanation,
				})
			}
		}
	}

	if len(accepted) == 0 {
		logger.Info("episode LLM rematch",
			logging.String(logging.FieldDecisionType, "episode_llm_rematch"),
			logging.String("decision_result", "needs_review"),
			logging.String("decision_reason", "all_combinations_rejected"),
			logging.Int("candidates", len(candidates)),
			logging.Int("references", len(references)),
		)
		return nil, true
	}

	size := max(len(candidates), len(references))
	const padCost = 2.0
	cost := make([][]float64, size)
	scoreMatrix := make([][]float64, size)
	explain := make([][]string, size)
	for i := range size {
		cost[i] = make([]float64, size)
		scoreMatrix[i] = make([]float64, size)
		explain[i] = make([]string, size)
		for j := range size {
			cost[i][j] = padCost
		}
	}
	for _, s := range accepted {
		if s.candidateIdx >= len(candidates) || s.referenceIdx >= len(references) {
			continue
		}
		scoreMatrix[s.candidateIdx][s.referenceIdx] = s.confidence
		cost[s.candidateIdx][s.referenceIdx] = 1.0 - s.confidence
		explain[s.candidateIdx][s.referenceIdx] = s.explanation
	}

	assign := hungarian(cost)
	var result []matchResult
	for ci, ri := range assign {
		if ci >= len(candidates) || ri < 0 || ri >= len(references) {
			continue
		}
		confidence := scoreMatrix[ci][ri]
		if confidence <= 0 {
			continue
		}

		cand := candidates[ci]
		ref := references[ri]

		// Find original match to copy TitleID.
		var titleID int
		for _, rej := range rejections {
			if rej.match.EpisodeKey == cand.episodeKey {
				titleID = rej.match.TitleID
				break
			}
		}

		result = append(result, matchResult{
			EpisodeKey:        cand.episodeKey,
			TitleID:           titleID,
			TargetEpisode:     ref.episode,
			Score:             confidence,
			SubtitleFileID:    ref.fileID,
			SubtitleLanguage:  ref.language,
			SubtitleCachePath: ref.refPath,
		})

		logger.Info("episode LLM rematch",
			logging.String(logging.FieldDecisionType, "episode_llm_rematch"),
			logging.String("decision_result", "reassigned"),
			logging.String("decision_reason", explain[ci][ri]),
			logging.String("episode_key", cand.episodeKey),
			logging.Int("target_episode", ref.episode),
			logging.Float64("llm_confidence", confidence),
		)
	}

	return result, false
}

// extractMiddleTranscript extracts the middle 10 minutes of dialogue from an SRT
// file, truncated to maxTranscriptChars.
func extractMiddleTranscript(srtPath string) (string, error) {
	start, end, err := subtitles.MiddleSRTRange(srtPath, middleWindowHalfSec)
	if err != nil {
		return "", err
	}
	text, err := subtitles.ExtractSRTTimeRange(srtPath, start, end)
	if err != nil {
		return "", err
	}
	if len(text) > maxTranscriptChars {
		text = text[:maxTranscriptChars]
	}
	return text, nil
}

// buildVerificationPrompt creates the user-content prompt for episode verification.
func buildVerificationPrompt(whisperXText, referenceText, episodeKey string, targetEpisode int) string {
	return fmt.Sprintf(`Episode key: %s
Target episode: %d

=== TRANSCRIPT A (WhisperX from disc) ===
%s

=== TRANSCRIPT B (OpenSubtitles reference) ===
%s`, episodeKey, targetEpisode, whisperXText, referenceText)
}

// logVerifySkip logs a verification skip, marks the result for review, and
// optionally includes an error attribute.
func logVerifySkip(logger *slog.Logger, vr *verifyResult, m matchResult, hint string, err error) {
	attrs := []logging.Attr{
		logging.String(logging.FieldEventType, "llm_verification_error"),
		logging.String(logging.FieldErrorHint, hint),
		logging.String(logging.FieldImpact, "match not verified by LLM"),
		logging.String("episode_key", m.EpisodeKey),
		logging.Int("target_episode", m.TargetEpisode),
	}
	if err != nil {
		attrs = append(attrs, logging.Error(err))
	}
	logger.Warn("LLM verification failed, keeping original match", logging.Args(attrs...)...)
	vr.NeedsReview = true
	if vr.ReviewReason == "" {
		vr.ReviewReason = "llm verification error"
	}
}

// logVerifySummary logs the overall verification summary.
func logVerifySummary(logger *slog.Logger, vr *verifyResult) {
	if logger == nil || vr == nil {
		return
	}
	logger.Info("content id LLM verification summary",
		logging.String(logging.FieldDecisionType, "contentid_llm_verification"),
		logging.Int("challenged", vr.Challenged),
		logging.Int("verified", vr.Verified),
		logging.Int("rejected", vr.Rejected),
		logging.Int("rematched", vr.Rematched),
		logging.Bool("needs_review", vr.NeedsReview),
	)
}
