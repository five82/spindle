package contentid

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/srtutil"
)

const (
	middleWindowHalfSec = 300.0
	maxTranscriptChars  = 6000
	verificationPrompt  = `You compare two subtitle transcripts and determine whether they represent the same TV episode.
Account for transcription errors, subtitle paraphrasing, and minor formatting differences.
Respond ONLY with JSON: {"same_episode": true/false, "confidence": 0.0-1.0, "explanation": "brief reason"}`
)

type episodeVerification struct {
	SameEpisode bool    `json:"same_episode"`
	Confidence  float64 `json:"confidence"`
	Explanation string  `json:"explanation"`
}

type verifyResult struct {
	Challenged   int
	Verified     int
	Rejected     int
	NeedsReview  bool
	ReviewReason string
}

func verifyMatches(ctx context.Context, client *llm.Client, matches []matchResult, rips []ripFingerprint, refs []referenceFingerprint, logger *slog.Logger, verifyThreshold float64) ([]matchResult, *verifyResult) {
	if client == nil {
		return matches, nil
	}
	var candidates []int
	for i, m := range matches {
		if m.Score < verifyThreshold {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return matches, nil
	}
	result := &verifyResult{Challenged: len(candidates)}
	updated := append([]matchResult(nil), matches...)
	for _, idx := range candidates {
		m := matches[idx]
		ripPath := findRipPath(rips, m.EpisodeKey)
		refPath := findRefPath(refs, m.TargetEpisode)
		if ripPath == "" || refPath == "" {
			result.NeedsReview = true
			if result.ReviewReason == "" {
				result.ReviewReason = "LLM verification skipped due to missing transcript"
			}
			continue
		}
		ripText, err := extractMiddleTranscript(ripPath)
		if err != nil {
			result.NeedsReview = true
			if result.ReviewReason == "" {
				result.ReviewReason = "LLM verification failed extracting rip transcript"
			}
			continue
		}
		refText, err := extractMiddleTranscript(refPath)
		if err != nil {
			result.NeedsReview = true
			if result.ReviewReason == "" {
				result.ReviewReason = "LLM verification failed extracting reference transcript"
			}
			continue
		}
		userPrompt := buildVerificationPrompt(ripText, refText, m.EpisodeKey, m.TargetEpisode)
		var ev episodeVerification
		if err := client.CompleteJSON(ctx, verificationPrompt, userPrompt, &ev); err != nil {
			result.NeedsReview = true
			if result.ReviewReason == "" {
				result.ReviewReason = "LLM verification request failed"
			}
			continue
		}
		if ev.SameEpisode {
			result.Verified++
			if ev.Confidence > updated[idx].Score {
				updated[idx].Score = ev.Confidence
			}
			if logger != nil {
				logger.Info("episode LLM verification",
					"decision_type", "contentid_llm_verification",
					"decision_result", "confirmed",
					"decision_reason", ev.Explanation,
					"episode_key", m.EpisodeKey,
					"target_episode", m.TargetEpisode,
					"match_score", m.Score,
					"llm_confidence", ev.Confidence,
				)
			}
			continue
		}
		result.Rejected++
		result.NeedsReview = true
		result.ReviewReason = fmt.Sprintf("LLM rejected match for %s -> E%02d", m.EpisodeKey, m.TargetEpisode)
		if logger != nil {
			logger.Info("episode LLM verification",
				"decision_type", "contentid_llm_verification",
				"decision_result", "rejected",
				"decision_reason", ev.Explanation,
				"episode_key", m.EpisodeKey,
				"target_episode", m.TargetEpisode,
				"match_score", m.Score,
				"llm_confidence", ev.Confidence,
			)
		}
	}
	return updated, result
}

func findRipPath(rips []ripFingerprint, key string) string {
	for _, r := range rips {
		if strings.EqualFold(r.EpisodeKey, key) {
			return r.Path
		}
	}
	return ""
}

func findRefPath(refs []referenceFingerprint, episode int) string {
	for _, r := range refs {
		if r.EpisodeNumber == episode {
			return r.CachePath
		}
	}
	return ""
}

func buildVerificationPrompt(whisperXText, referenceText, episodeKey string, targetEpisode int) string {
	return fmt.Sprintf(`Episode key: %s
Target episode: %d

=== TRANSCRIPT A (WhisperX from disc) ===
%s

=== TRANSCRIPT B (OpenSubtitles reference) ===
%s`, episodeKey, targetEpisode, whisperXText, referenceText)
}

func extractMiddleTranscript(srtPath string) (string, error) {
	cues, err := srtutil.ParseFile(srtPath)
	if err != nil {
		return "", err
	}
	if len(cues) == 0 {
		return "", fmt.Errorf("no subtitle cues found")
	}
	total := cues[len(cues)-1].End
	mid := total / 2
	start := max(0, mid-middleWindowHalfSec)
	end := mid + middleWindowHalfSec
	if total < 2*middleWindowHalfSec {
		start = 0
		end = total
	}
	var sb strings.Builder
	for _, cue := range cues {
		if cue.End < start || cue.Start > end {
			continue
		}
		text := strings.ReplaceAll(cue.Text, "\n", " ")
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(text)
		if sb.Len() >= maxTranscriptChars {
			break
		}
	}
	text := sb.String()
	if len(text) > maxTranscriptChars {
		text = text[:maxTranscriptChars]
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("middle transcript empty")
	}
	return text, nil
}

