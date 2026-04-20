package subtitle

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/five82/spindle/internal/srtutil"
)

const (
	preferredSubtitleReadingSpeed = 20.0
	preferredSubtitleCueDuration  = 1.0
	preferredWrapCharsPerLine     = 41
	preferredDisplayMaxCueChars   = 80
	preferredDisplayMaxCueWords   = 16
	preferredDisplayMaxCueDur     = 8.5
	displayCueGapPadding          = 0.04
	maxDisplayExtensionPerSide    = 1.25
)

type displayPostProcessStats struct {
	SplitCues   int
	WrappedCues int
	RetimedCues int
}

func postProcessDisplaySRT(path string, videoSeconds float64) (displayPostProcessStats, error) {
	cues, err := srtutil.ParseFile(path)
	if err != nil {
		return displayPostProcessStats{}, fmt.Errorf("parse formatted subtitle: %w", err)
	}
	if len(cues) == 0 {
		return displayPostProcessStats{}, nil
	}
	processed, stats := postProcessDisplayCues(cues, videoSeconds)
	for i := range processed {
		processed[i].Index = i + 1
	}
	if err := os.WriteFile(path, []byte(srtutil.Format(processed)), 0o644); err != nil {
		return displayPostProcessStats{}, fmt.Errorf("rewrite formatted subtitle: %w", err)
	}
	return stats, nil
}

func postProcessDisplayCues(cues []srtutil.Cue, videoSeconds float64) ([]srtutil.Cue, displayPostProcessStats) {
	processed, splitCount := splitDisplayCues(cues)
	stats := displayPostProcessStats{SplitCues: splitCount}
	for i := range processed {
		wrapped, changed := wrapDisplayCueText(processed[i].Text)
		if changed {
			processed[i].Text = wrapped
			stats.WrappedCues++
		}
	}
	stats.RetimedCues = retimeDisplayCues(processed, videoSeconds)
	return processed, stats
}

func wrapDisplayCueText(text string) (string, bool) {
	normalized := normalizeCueWhitespace(text)
	if normalized == "" {
		return "", strings.TrimSpace(text) != ""
	}
	if !strings.Contains(normalized, "\n") && utf8.RuneCountInString(normalized) <= maxSubtitleCharsPerLine {
		return normalized, normalized != strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	}

	words := strings.Fields(normalized)
	if len(words) < 2 {
		return normalized, normalized != strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	}
	breakAt, ok := bestTwoLineBreak(words, preferredWrapCharsPerLine)
	if !ok {
		return normalized, normalized != strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	}
	wrapped := strings.Join(words[:breakAt], " ") + "\n" + strings.Join(words[breakAt:], " ")
	return wrapped, wrapped != strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
}

func normalizeCueWhitespace(text string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if trimmed == "" {
		return ""
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(trimmed, "\n", " ")), " ")
}

func bestTwoLineBreak(words []string, maxChars int) (int, bool) {
	if len(words) < 2 {
		return 0, false
	}
	bestIdx := 0
	bestScore := int(^uint(0) >> 1)
	bestOverflow := int(^uint(0) >> 1)
	for i := 1; i < len(words); i++ {
		left := strings.Join(words[:i], " ")
		right := strings.Join(words[i:], " ")
		leftLen := utf8.RuneCountInString(left)
		rightLen := utf8.RuneCountInString(right)
		overflow := max(0, leftLen-maxChars) + max(0, rightLen-maxChars)
		score := max(leftLen, rightLen)
		if overflow == 0 {
			score = absInt(leftLen - rightLen)
		}
		if overflow < bestOverflow || (overflow == bestOverflow && score < bestScore) {
			bestOverflow = overflow
			bestScore = score
			bestIdx = i
		}
	}
	if bestIdx == 0 {
		return 0, false
	}
	return bestIdx, true
}

func splitDisplayCues(cues []srtutil.Cue) ([]srtutil.Cue, int) {
	processed := make([]srtutil.Cue, 0, len(cues))
	var splitCount int
	for _, cue := range cues {
		parts := splitDisplayCue(cue)
		if len(parts) > 1 {
			splitCount += len(parts) - 1
		}
		processed = append(processed, parts...)
	}
	return processed, splitCount
}

func splitDisplayCue(cue srtutil.Cue) []srtutil.Cue {
	text := normalizeCueWhitespace(cue.Text)
	if text == "" {
		cue.Text = ""
		return []srtutil.Cue{cue}
	}
	words := strings.Fields(text)
	if len(words) < 2 {
		cue.Text = text
		return []srtutil.Cue{cue}
	}
	duration := cue.End - cue.Start
	if !shouldSplitDisplayCue(text, duration, len(words)) {
		cue.Text = text
		return []srtutil.Cue{cue}
	}
	chunkMaxChars := preferredDisplayMaxCueChars
	chunkMaxWords := preferredDisplayMaxCueWords
	if !cueWrapsCleanly(text) {
		chunkMaxChars = 70
		chunkMaxWords = 14
	}
	chunks := splitWordsIntoCueChunks(words, chunkMaxChars, chunkMaxWords)
	if len(chunks) < 2 {
		cue.Text = text
		return []srtutil.Cue{cue}
	}
	parts := make([]srtutil.Cue, 0, len(chunks))
	totalRunes := 0
	for _, chunk := range chunks {
		totalRunes += utf8.RuneCountInString(chunk)
	}
	start := cue.Start
	remainingDuration := duration
	remainingRunes := totalRunes
	for i, chunk := range chunks {
		part := cue
		part.Text = chunk
		if i == len(chunks)-1 || duration <= 0 || remainingRunes <= 0 {
			part.Start = start
			part.End = cue.End
			parts = append(parts, part)
			break
		}
		chunkRunes := utf8.RuneCountInString(chunk)
		remainingParts := len(chunks) - i - 1
		minRemaining := float64(remainingParts) * minSubtitleCueDuration
		partDuration := duration * float64(chunkRunes) / float64(totalRunes)
		partDuration = max(partDuration, minSubtitleCueDuration)
		partDuration = min(partDuration, remainingDuration-minRemaining)
		if partDuration < minSubtitleCueDuration {
			partDuration = minSubtitleCueDuration
		}
		part.Start = start
		part.End = start + partDuration
		parts = append(parts, part)
		start = part.End
		remainingDuration = cue.End - start
		remainingRunes -= chunkRunes
	}
	return parts
}

func cueWrapsCleanly(text string) bool {
	wrapped, _ := wrapDisplayCueText(text)
	lines := splitCueLines(wrapped)
	return len(lines) > 0 && len(lines) <= maxSubtitleLinesPerCue && !hasOverlongLine(lines)
}

func shouldSplitDisplayCue(text string, duration float64, wordCount int) bool {
	textRunes := utf8.RuneCountInString(text)
	wrapsCleanly := cueWrapsCleanly(text)
	if wrapsCleanly {
		cps := 0.0
		if duration > 0 {
			cps = float64(textRunes) / duration
		}
		if duration <= preferredDisplayMaxCueDur && cps <= preferredSubtitleReadingSpeed && wordCount <= preferredDisplayMaxCueWords+2 {
			return false
		}
		if duration <= maxSubtitleCueDuration+1.0 && cps <= 18.0 && wordCount <= preferredDisplayMaxCueWords+2 {
			return false
		}
	}
	return textRunes > preferredDisplayMaxCueChars || wordCount > preferredDisplayMaxCueWords || duration > preferredDisplayMaxCueDur || !wrapsCleanly
}

func splitWordsIntoCueChunks(words []string, maxChars, maxWords int) []string {
	if len(words) == 0 {
		return nil
	}
	chunks := make([]string, 0, len(words))
	for start := 0; start < len(words); {
		end := start
		charCount := 0
		lastPreferredBreak := 0
		for end < len(words) {
			wordLen := utf8.RuneCountInString(words[end])
			if end > start {
				wordLen++
			}
			prospectiveWords := end - start + 1
			if end > start && ((maxWords > 0 && prospectiveWords > maxWords) || (maxChars > 0 && charCount+wordLen > maxChars)) {
				if lastPreferredBreak > start {
					end = lastPreferredBreak
				}
				break
			}
			charCount += wordLen
			end++
			if cueBreakPreferred(words[end-1]) {
				lastPreferredBreak = end
			}
		}
		if end == start {
			end++
		}
		chunks = append(chunks, strings.Join(words[start:end], " "))
		start = end
	}
	return chunks
}

func cueBreakPreferred(word string) bool {
	return strings.HasSuffix(word, ".") || strings.HasSuffix(word, ",") || strings.HasSuffix(word, "?") || strings.HasSuffix(word, "!") || strings.HasSuffix(word, ";") || strings.HasSuffix(word, ":")
}

func retimeDisplayCues(cues []srtutil.Cue, videoSeconds float64) int {
	if len(cues) == 0 {
		return 0
	}
	if videoSeconds <= 0 {
		videoSeconds = cues[len(cues)-1].End
	}

	beforeBudget := make([]float64, len(cues))
	afterBudget := make([]float64, len(cues))
	beforeBudget[0] = min(maxDisplayExtensionPerSide, max(0, cues[0].Start-displayCueGapPadding))
	for i := 1; i < len(cues); i++ {
		gap := cues[i].Start - cues[i-1].End
		usable := gap - displayCueGapPadding
		if usable <= 0 {
			continue
		}
		share := min(maxDisplayExtensionPerSide, usable/2)
		afterBudget[i-1] = share
		beforeBudget[i] = share
	}
	afterBudget[len(cues)-1] = min(maxDisplayExtensionPerSide, max(0, videoSeconds-cues[len(cues)-1].End-displayCueGapPadding))

	var changed int
	for i := range cues {
		text := normalizeCueWhitespace(cues[i].Text)
		if text == "" {
			continue
		}
		duration := cues[i].End - cues[i].Start
		if duration <= 0 {
			continue
		}
		chars := utf8.RuneCountInString(text)
		if duration > maxSubtitleCueDuration {
			if lexicalWordCount(text) <= 5 {
				target := min(maxSubtitleCueDuration, max(preferredSubtitleCueDuration, float64(chars)/preferredSubtitleReadingSpeed))
				if target < duration-0.10 {
					cues[i].End = min(cues[i].End, cues[i].Start+target)
					changed++
				}
			}
			continue
		}
		target := max(duration, preferredSubtitleCueDuration, float64(chars)/preferredSubtitleReadingSpeed)
		target = min(target, maxSubtitleCueDuration)
		need := target - duration
		if need < 0.10 {
			continue
		}

		extendBefore := min(beforeBudget[i], need/2)
		extendAfter := min(afterBudget[i], need-extendBefore)
		remaining := need - extendBefore - extendAfter
		if remaining > 0 {
			extraBefore := min(beforeBudget[i]-extendBefore, remaining)
			extendBefore += extraBefore
			remaining -= extraBefore
		}
		if remaining > 0 {
			extraAfter := min(afterBudget[i]-extendAfter, remaining)
			extendAfter += extraAfter
		}
		if extendBefore+extendAfter < 0.10 {
			continue
		}

		cues[i].Start -= extendBefore
		cues[i].End += extendAfter
		changed++
	}

	for i := range cues {
		if cues[i].Start < 0 {
			cues[i].Start = 0
		}
		if videoSeconds > 0 && cues[i].End > videoSeconds {
			cues[i].End = videoSeconds
		}
		if cues[i].End < cues[i].Start {
			cues[i].End = cues[i].Start
		}
	}
	return changed
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
