package contentid

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/srtutil"
)

const (
	middleWindowHalfSec = 300.0
	maxTranscriptChars  = 6000
	verificationPrompt  = `You compare two TV episode transcripts to determine if they are from the same episode.

TRANSCRIPT A is a WhisperX speech-to-text transcription from a Blu-ray disc.
TRANSCRIPT B is a reference subtitle from OpenSubtitles for a specific episode.

Both are extracted from the middle portion of the episode, typically about 10 minutes, though shorter transcripts may use the full available duration.
WhisperX transcripts may contain speech recognition errors.
Reference subtitles may differ in exact wording due to subtitle conventions, release differences, or localization.

Focus on whether the same scenes and dialogue events occur in both.
Do NOT penalize minor word differences, transcription errors, or timing differences.

Respond ONLY with JSON: {"same_episode": true/false, "explanation": "brief reason"}`
)

type episodeVerification struct {
	SameEpisode bool   `json:"same_episode"`
	Explanation string `json:"explanation"`
}

type verifyResult struct {
	Challenged   int
	Verified     int
	Rejected     int
	Failed       int
	NeedsReview  bool
	ReviewReason string
}

func verifyMatches(ctx context.Context, client *llm.Client, accepted []matchResult, pendingByRip map[string][]matchResult, rips []ripFingerprint, refs []referenceFingerprint, logger *slog.Logger) ([]matchResult, map[string][]matchResult, *verifyResult) {
	remaining := clonePendingByRip(pendingByRip)
	if client == nil || len(pendingByRip) == 0 {
		return accepted, remaining, nil
	}

	result := &verifyResult{}
	updated := append([]matchResult(nil), accepted...)
	acceptedRips := make(map[string]struct{}, len(updated))
	acceptedEpisodes := make(map[int]struct{}, len(updated))
	for _, match := range updated {
		acceptedRips[strings.ToLower(match.EpisodeKey)] = struct{}{}
		acceptedEpisodes[match.TargetEpisode] = struct{}{}
	}

	queue := verificationQueue(pendingByRip, acceptedRips)
	for _, entry := range queue {
		if _, ok := acceptedRips[strings.ToLower(entry.EpisodeKey)]; ok {
			continue
		}
		for _, candidate := range entry.Candidates {
			if _, ok := acceptedEpisodes[candidate.TargetEpisode]; ok {
				remaining[entry.EpisodeKey] = removeCandidateEpisode(remaining[entry.EpisodeKey], candidate.TargetEpisode)
				continue
			}
			result.Challenged++
			ripPath := findRipPath(rips, candidate.EpisodeKey)
			refPath := findRefPath(refs, candidate.TargetEpisode)
			if ripPath == "" || refPath == "" {
				result.Failed++
				result.NeedsReview = true
				if result.ReviewReason == "" {
					result.ReviewReason = "LLM verification failed for ambiguous episode pair"
				}
				remaining[entry.EpisodeKey] = removeCandidateEpisode(remaining[entry.EpisodeKey], candidate.TargetEpisode)
				continue
			}
			ripText, err := extractMiddleTranscript(ripPath)
			if err != nil {
				result.Failed++
				result.NeedsReview = true
				if result.ReviewReason == "" {
					result.ReviewReason = "LLM verification failed for ambiguous episode pair"
				}
				remaining[entry.EpisodeKey] = removeCandidateEpisode(remaining[entry.EpisodeKey], candidate.TargetEpisode)
				continue
			}
			refText, err := extractMiddleTranscript(refPath)
			if err != nil {
				result.Failed++
				result.NeedsReview = true
				if result.ReviewReason == "" {
					result.ReviewReason = "LLM verification failed for ambiguous episode pair"
				}
				remaining[entry.EpisodeKey] = removeCandidateEpisode(remaining[entry.EpisodeKey], candidate.TargetEpisode)
				continue
			}
			userPrompt := buildVerificationPrompt(ripText, refText, candidate.EpisodeKey, candidate.TargetEpisode)
			var ev episodeVerification
			if err := client.CompleteJSON(ctx, verificationPrompt, userPrompt, &ev); err != nil {
				result.Failed++
				result.NeedsReview = true
				if result.ReviewReason == "" {
					result.ReviewReason = "LLM verification failed for ambiguous episode pair"
				}
				remaining[entry.EpisodeKey] = removeCandidateEpisode(remaining[entry.EpisodeKey], candidate.TargetEpisode)
				if logger != nil {
					logger.Info("episode LLM verification",
						"decision_type", "contentid_llm_verification",
						"decision_result", "failed",
						"decision_reason", err.Error(),
						"episode_key", candidate.EpisodeKey,
						"target_episode", candidate.TargetEpisode,
						"match_score", candidate.Score,
						"match_confidence", candidate.Confidence,
					)
				}
				continue
			}
			if ev.SameEpisode {
				verified := candidate
				verified.AcceptedBy = "llm_verified"
				verified.NeedsVerification = false
				verified.VerificationReason = ""
				updated = append(updated, verified)
				acceptedRips[strings.ToLower(verified.EpisodeKey)] = struct{}{}
				acceptedEpisodes[verified.TargetEpisode] = struct{}{}
				delete(remaining, entry.EpisodeKey)
				result.Verified++
				if logger != nil {
					logger.Info("episode LLM verification",
						"decision_type", "contentid_llm_verification",
						"decision_result", "confirmed",
						"decision_reason", ev.Explanation,
						"episode_key", candidate.EpisodeKey,
						"target_episode", candidate.TargetEpisode,
						"match_score", candidate.Score,
						"match_confidence", candidate.Confidence,
					)
				}
				break
			}
			result.Rejected++
			result.NeedsReview = true
			if result.ReviewReason == "" {
				result.ReviewReason = "LLM rejected ambiguous episode pair"
			}
			remaining[entry.EpisodeKey] = removeCandidateEpisode(remaining[entry.EpisodeKey], candidate.TargetEpisode)
			if logger != nil {
				logger.Info("episode LLM verification",
					"decision_type", "contentid_llm_verification",
					"decision_result", "rejected",
					"decision_reason", ev.Explanation,
					"episode_key", candidate.EpisodeKey,
					"target_episode", candidate.TargetEpisode,
					"match_score", candidate.Score,
					"match_confidence", candidate.Confidence,
				)
			}
		}
	}
	return updated, remaining, result
}

type verificationEntry struct {
	EpisodeKey string
	Candidates []matchResult
	Strength   float64
}

func verificationQueue(pendingByRip map[string][]matchResult, acceptedRips map[string]struct{}) []verificationEntry {
	queue := make([]verificationEntry, 0, len(pendingByRip))
	for episodeKey, candidates := range pendingByRip {
		if _, ok := acceptedRips[strings.ToLower(episodeKey)]; ok {
			continue
		}
		if len(candidates) > maxVerificationCandidatesPerRip {
			candidates = append([]matchResult(nil), candidates[:maxVerificationCandidatesPerRip]...)
		} else {
			candidates = append([]matchResult(nil), candidates...)
		}
		strength := 0.0
		if len(candidates) > 0 {
			strength = candidates[0].Strength
		}
		queue = append(queue, verificationEntry{EpisodeKey: episodeKey, Candidates: candidates, Strength: strength})
	}
	sort.Slice(queue, func(i, j int) bool {
		if queue[i].Strength != queue[j].Strength {
			return queue[i].Strength > queue[j].Strength
		}
		return queue[i].EpisodeKey < queue[j].EpisodeKey
	})
	return queue
}

func clonePendingByRip(in map[string][]matchResult) map[string][]matchResult {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]matchResult, len(in))
	for key, candidates := range in {
		out[key] = append([]matchResult(nil), candidates...)
	}
	return out
}

func removeCandidateEpisode(candidates []matchResult, episode int) []matchResult {
	if len(candidates) == 0 {
		return nil
	}
	out := candidates[:0]
	for _, candidate := range candidates {
		if candidate.TargetEpisode != episode {
			out = append(out, candidate)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
	start := max(0.0, mid-middleWindowHalfSec)
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
