package subtitle

import (
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/five82/spindle/internal/srtutil"
)

const (
	// targetMinCueDuration is the extension target for short cues; it sits
	// above minSubtitleCueDuration so millisecond rounding cannot dip a
	// retimed cue back under the validator's floor.
	targetMinCueDuration       = 1.0
	maxDisplayExtensionPerSide = 2.0
	// mergeMaxCueGap bounds the silence between two cues that may be merged
	// into one; larger gaps mean separate utterances.
	mergeMaxCueGap = 0.5
)

type displayPostProcessStats struct {
	SplitCues   int
	MergedCues  int
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
	split, splitCount := splitDisplayCues(cues)
	merged, mergeCount := mergeDisplayCues(split)
	stats := displayPostProcessStats{SplitCues: splitCount, MergedCues: mergeCount}
	for i := range merged {
		wrapped, changed := wrapDisplayCueText(merged[i].Text)
		if changed {
			merged[i].Text = wrapped
			stats.WrappedCues++
		}
	}
	stats.RetimedCues = retimeDisplayCues(merged, videoSeconds)
	return merged, stats
}

func wrapDisplayCueText(text string) (string, bool) {
	normalized := normalizeCueWhitespace(text)
	if normalized == "" {
		return "", text != ""
	}
	result := normalized
	if utf8.RuneCountInString(normalized) > maxSubtitleCharsPerLine {
		words := strings.Fields(normalized)
		if len(words) >= 2 {
			if breakAt, ok := bestTwoLineBreak(words, maxSubtitleCharsPerLine); ok {
				result = strings.Join(words[:breakAt], " ") + "\n" + strings.Join(words[breakAt:], " ")
			}
		}
	}
	return result, result != text
}

func normalizeCueWhitespace(text string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if trimmed == "" {
		return ""
	}
	collapsed := strings.Join(strings.Fields(strings.ReplaceAll(trimmed, "\n", " ")), " ")
	return insertMissingPunctuationSpaces(collapsed)
}

// insertMissingPunctuationSpaces repairs words the transcription glued
// together across punctuation ("Ow!which", "shh.Ah!"). A space is inserted
// after ",", "!", or "?" between two letters, and after "." only between a
// lowercase and an uppercase letter so abbreviations ("U.S.A.", "a.m.") and
// numbers ("3.5", "1,000") stay intact.
func insertMissingPunctuationSpaces(text string) string {
	runes := []rune(text)
	var b strings.Builder
	b.Grow(len(text))
	for i, r := range runes {
		b.WriteRune(r)
		if i == 0 || i == len(runes)-1 {
			continue
		}
		prev, next := runes[i-1], runes[i+1]
		switch r {
		case ',', '!', '?':
			if unicode.IsLetter(prev) && unicode.IsLetter(next) {
				b.WriteRune(' ')
			}
		case '.':
			if unicode.IsLower(prev) && unicode.IsUpper(next) {
				b.WriteRune(' ')
			}
		}
	}
	return b.String()
}

func bestTwoLineBreak(words []string, maxChars int) (int, bool) {
	if len(words) < 2 {
		return 0, false
	}
	bestIdx := 0
	bestScore := int(^uint(0) >> 1)
	for i := 1; i < len(words); i++ {
		left := strings.Join(words[:i], " ")
		right := strings.Join(words[i:], " ")
		score := displayLineBreakScore(words, i, left, right, maxChars)
		if score < bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	if bestIdx == 0 {
		return 0, false
	}
	return bestIdx, true
}

func displayLineBreakScore(words []string, breakAt int, left, right string, maxChars int) int {
	leftLen := utf8.RuneCountInString(left)
	rightLen := utf8.RuneCountInString(right)
	leftWords := breakAt
	rightWords := len(words) - breakAt
	overflow := max(0, leftLen-maxChars) + max(0, rightLen-maxChars)

	// Start with fit and shape. Overflow dominates all style preferences; when
	// both lines fit, prefer balanced or slightly bottom-heavy lines.
	score := overflow * 10000
	score += absInt(leftLen-rightLen) * 8
	if leftLen > rightLen {
		score += (leftLen - rightLen) * 3
	}
	if leftWords <= 2 && len(words) > 4 {
		score += 240
	}
	if rightWords <= 1 && len(words) > 3 {
		score += 180
	}
	if absInt(leftLen-rightLen) > unbalancedLineDelta {
		score += 120
	}

	prev := words[breakAt-1]
	next := words[breakAt]
	prevNorm := normalizedBreakWord(prev)
	nextNorm := normalizedBreakWord(next)

	if cueBreakPreferred(prev) {
		score -= 90
	}
	if displayBreakConjunctions[nextNorm] {
		score -= 55
	}
	if displayBreakPrepositions[nextNorm] {
		score -= 35
	}

	if displayBreakDeterminers[prevNorm] {
		score += 100
	}
	if displayBreakAuxiliaries[prevNorm] {
		score += 90
	}
	if displayBreakSubjectPronouns[prevNorm] {
		score += 80
	}
	if titleCaseNameLike(prev) && titleCaseNameLike(next) {
		score += 45
	}

	return score
}

func normalizedBreakWord(word string) string {
	word = strings.Trim(word, " \t\r\n\"'()[]{}<>.,?!;:")
	return strings.ToLower(word)
}

func titleCaseNameLike(word string) bool {
	word = strings.Trim(word, " \t\r\n\"'()[]{}<>.,?!;:")
	runes := []rune(word)
	if len(runes) < 2 || len(runes) > 20 {
		return false
	}
	if !unicode.IsUpper(runes[0]) {
		return false
	}
	for _, r := range runes[1:] {
		if !unicode.IsLetter(r) && r != '-' {
			return false
		}
	}
	return true
}

var displayBreakConjunctions = map[string]bool{
	"and": true, "but": true, "or": true, "so": true, "yet": true, "for": true, "nor": true,
	"because": true, "although": true, "though": true, "while": true, "if": true, "when": true,
}

var displayBreakPrepositions = map[string]bool{
	"in": true, "on": true, "at": true, "to": true, "from": true, "with": true, "by": true, "for": true,
	"of": true, "about": true, "into": true, "over": true, "under": true, "before": true, "after": true,
	"through": true, "between": true, "without": true, "within": true,
}

var displayBreakDeterminers = map[string]bool{
	"a": true, "an": true, "the": true,
	"my": true, "your": true, "his": true, "her": true, "its": true, "our": true, "their": true,
}

var displayBreakAuxiliaries = map[string]bool{
	"am": true, "is": true, "are": true, "was": true, "were": true, "be": true, "been": true, "being": true,
	"do": true, "does": true, "did": true, "have": true, "has": true, "had": true,
	"will": true, "would": true, "can": true, "could": true, "shall": true, "should": true, "may": true,
	"might": true, "must": true, "not": true, "don't": true, "doesn't": true, "didn't": true, "can't": true,
	"won't": true, "wouldn't": true, "shouldn't": true, "couldn't": true,
}

var displayBreakSubjectPronouns = map[string]bool{
	"i": true, "you": true, "he": true, "she": true, "it": true, "we": true, "they": true,
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

// splitDisplayCue splits a cue whose text cannot wrap cleanly into two
// 42-char lines. Duration-based splitting is not done here: the Python
// stable-ts stage owns duration splits because it has word timings, so by
// the time cues reach this pass only line-length overflow remains to fix.
func splitDisplayCue(cue srtutil.Cue) []srtutil.Cue {
	text := normalizeCueWhitespace(cue.Text)
	if text == "" {
		cue.Text = ""
		return []srtutil.Cue{cue}
	}
	// Unsplit cues keep their original text; the wrap pass normalizes and
	// counts the change, so WrappedCues stays an honest stat.
	words := strings.Fields(text)
	if len(words) < 2 || cueWrapsCleanly(text) {
		return []srtutil.Cue{cue}
	}
	chunks := splitWordsIntoCueChunks(words)
	if len(chunks) < 2 {
		return []srtutil.Cue{cue}
	}
	parts := make([]srtutil.Cue, 0, len(chunks))
	totalRunes := 0
	for _, chunk := range chunks {
		totalRunes += utf8.RuneCountInString(chunk)
	}
	duration := cue.End - cue.Start
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
		if remainingDuration >= minRemaining+minSubtitleCueDuration {
			partDuration = max(partDuration, minSubtitleCueDuration)
			partDuration = min(partDuration, remainingDuration-minRemaining)
		} else {
			partDuration = remainingDuration / float64(remainingParts+1)
		}
		if partDuration <= 0 {
			partDuration = remainingDuration / float64(remainingParts+1)
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

// splitWordsIntoCueChunks greedily grows a chunk word by word while it still
// wraps cleanly into maxSubtitleLinesPerCue lines. When the next word would
// break that, it cuts at the last preferred punctuation boundary inside the
// chunk if one exists past the chunk start, else right before the
// overflowing word. Every chunk has at least one word.
func splitWordsIntoCueChunks(words []string) []string {
	if len(words) == 0 {
		return nil
	}
	chunks := make([]string, 0, len(words))
	for start := 0; start < len(words); {
		end := start + 1
		lastPreferredBreak := 0
		if cueBreakPreferred(words[start]) {
			lastPreferredBreak = end
		}
		for end < len(words) {
			candidate := strings.Join(words[start:end+1], " ")
			if !cueWrapsCleanly(candidate) {
				break
			}
			end++
			if cueBreakPreferred(words[end-1]) {
				lastPreferredBreak = end
			}
		}
		if end < len(words) && lastPreferredBreak > start {
			end = lastPreferredBreak
		}
		chunks = append(chunks, strings.Join(words[start:end], " "))
		start = end
	}
	return chunks
}

func cueBreakPreferred(word string) bool {
	return strings.HasSuffix(word, ".") || strings.HasSuffix(word, ",") || strings.HasSuffix(word, "?") || strings.HasSuffix(word, "!") || strings.HasSuffix(word, ";") || strings.HasSuffix(word, ":")
}

// mergeDisplayCues joins a cue with its successor when either is too short
// or too fast to read and the pair plays as one utterance. Merging absorbs
// the inter-cue gap, which is the only way to fix reading speed without
// dropping text.
func mergeDisplayCues(cues []srtutil.Cue) ([]srtutil.Cue, int) {
	if len(cues) == 0 {
		return cues, 0
	}
	result := make([]srtutil.Cue, 0, len(cues))
	result = append(result, cues[0])
	var merged int
	for i := 1; i < len(cues); i++ {
		next := cues[i]
		cur := result[len(result)-1]
		if mergedCue, ok := tryMergeDisplayCues(cur, next); ok {
			result[len(result)-1] = mergedCue
			merged++
			continue
		}
		result = append(result, next)
	}
	return result, merged
}

func tryMergeDisplayCues(cur, next srtutil.Cue) (srtutil.Cue, bool) {
	curText := normalizeCueWhitespace(cur.Text)
	nextText := normalizeCueWhitespace(next.Text)
	if curText == "" || nextText == "" {
		return srtutil.Cue{}, false
	}
	if !cueIsDeficient(cur, curText) && !cueIsDeficient(next, nextText) {
		return srtutil.Cue{}, false
	}
	if gap := next.Start - cur.End; gap > mergeMaxCueGap {
		return srtutil.Cue{}, false
	}
	if next.End-cur.Start > maxSubtitleCueDuration {
		return srtutil.Cue{}, false
	}
	mergedText := normalizeCueWhitespace(curText + " " + nextText)
	if !cueWrapsCleanly(mergedText) {
		return srtutil.Cue{}, false
	}
	return srtutil.Cue{Index: cur.Index, Start: cur.Start, End: next.End, Text: mergedText}, true
}

// cueIsDeficient reports whether a cue is too short to comfortably display
// or too fast to comfortably read.
func cueIsDeficient(cue srtutil.Cue, normalizedText string) bool {
	duration := cue.End - cue.Start
	if duration < minSubtitleCueDuration {
		return true
	}
	return float64(utf8.RuneCountInString(normalizedText))/duration > maxSubtitleReadingSpeed
}

func retimeDisplayCues(cues []srtutil.Cue, videoSeconds float64) int {
	if len(cues) == 0 {
		return 0
	}
	if videoSeconds <= 0 {
		videoSeconds = cues[len(cues)-1].End
	}

	var changed int

	// Hard cap: after split+merge every cue text fits maxSubtitleCueChars,
	// which needs at most 4.2s at maxSubtitleReadingSpeed, so a 7s cap never
	// harms readability.
	for i := range cues {
		if normalizeCueWhitespace(cues[i].Text) == "" {
			continue
		}
		if duration := cues[i].End - cues[i].Start; duration > maxSubtitleCueDuration {
			cues[i].End = cues[i].Start + maxSubtitleCueDuration
			changed++
		}
	}

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
		target := max(duration, targetMinCueDuration, float64(chars)/maxSubtitleReadingSpeed)
		target = min(target, maxSubtitleCueDuration)
		need := target - duration
		if need < 0.10 {
			continue
		}

		prevEnd := 0.0
		if i > 0 {
			prevEnd = cues[i-1].End + minSubtitleCueGap
		}
		nextStart := videoSeconds
		if i < len(cues)-1 {
			nextStart = cues[i+1].Start - minSubtitleCueGap
		}
		beforeBudget := min(maxDisplayExtensionPerSide, max(0, cues[i].Start-prevEnd))
		afterBudget := min(maxDisplayExtensionPerSide, max(0, nextStart-cues[i].End))

		extendBefore := min(beforeBudget, need/2)
		extendAfter := min(afterBudget, need-extendBefore)
		remaining := need - extendBefore - extendAfter
		if remaining > 0 {
			extraBefore := min(beforeBudget-extendBefore, remaining)
			extendBefore += extraBefore
			remaining -= extraBefore
		}
		if remaining > 0 {
			extraAfter := min(afterBudget-extendAfter, remaining)
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
		if i < len(cues)-1 && cues[i].End > cues[i+1].Start-minSubtitleCueGap {
			cues[i].End = max(cues[i].Start, cues[i+1].Start-minSubtitleCueGap)
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
