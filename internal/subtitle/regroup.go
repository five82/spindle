package subtitle

import (
	"fmt"
	"math"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/five82/spindle/internal/srtutil"
)

const (
	maxSubtitleCharsPerCue = maxSubtitleCharsPerLine * 2
	minSplitCueChars       = 16
	minSplitCuePartDur     = 1.0
)

func regroupFormattedSubtitle(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read formatted subtitle for regrouping: %w", err)
	}
	cues := srtutil.Parse(string(data))
	if len(cues) == 0 {
		return nil
	}
	formatted := srtutil.Format(regroupDisplayCues(cues))
	if formatted == string(data) {
		return nil
	}
	if err := os.WriteFile(path, []byte(formatted), 0o644); err != nil {
		return fmt.Errorf("write regrouped subtitle: %w", err)
	}
	return nil
}

func regroupDisplayCues(cues []srtutil.Cue) []srtutil.Cue {
	cues = mergeAdjacentDisplayCues(cues)
	out := make([]srtutil.Cue, 0, len(cues))
	for _, cue := range cues {
		out = append(out, splitDisplayCue(cue)...)
	}
	for i := range out {
		out[i].Index = i + 1
	}
	return out
}

func mergeAdjacentDisplayCues(cues []srtutil.Cue) []srtutil.Cue {
	if len(cues) < 2 {
		return cues
	}
	merged := make([]srtutil.Cue, 0, len(cues))
	current := cues[0]
	current.Text = normalizeCueDisplayText(current.Text)
	for i := 1; i < len(cues); i++ {
		next := cues[i]
		next.Text = normalizeCueDisplayText(next.Text)
		if shouldMergeAdjacentCue(current, next) {
			current.Text = appendDisplayFragments(current.Text, next.Text)
			current.End = next.End
			continue
		}
		merged = append(merged, current)
		current = next
	}
	merged = append(merged, current)
	return merged
}

func shouldMergeAdjacentCue(current, next srtutil.Cue) bool {
	gap := next.Start - current.End
	if gap < -0.01 || gap > 0.18 {
		return false
	}
	currText := normalizeCueDisplayText(current.Text)
	nextText := normalizeCueDisplayText(next.Text)
	if currText == "" || nextText == "" {
		return false
	}
	combined := appendDisplayFragments(currText, nextText)
	if runeLen(combined) > maxSubtitleCharsPerCue*2 {
		return false
	}
	currentDur := current.End - current.Start
	nextDur := next.End - next.Start
	if currentDur > 2.2 && nextDur > 2.2 {
		return false
	}
	if runeLen(currText) <= 12 || runeLen(nextText) <= 12 {
		return true
	}
	if !endsStrongSentence(currText) {
		return true
	}
	return false
}

func appendDisplayFragments(left, right string) string {
	left = normalizeCueDisplayText(left)
	right = normalizeCueDisplayText(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	if strings.HasPrefix(right, ".") || strings.HasPrefix(right, ",") || strings.HasPrefix(right, "!") || strings.HasPrefix(right, "?") || strings.HasPrefix(right, ";") || strings.HasPrefix(right, ":") {
		return left + right
	}
	return left + " " + right
}

func splitDisplayCue(cue srtutil.Cue) []srtutil.Cue {
	text := normalizeCueDisplayText(cue.Text)
	if text == "" {
		cue.Text = ""
		return []srtutil.Cue{cue}
	}
	parts := splitDisplayText(text, cue.End-cue.Start)
	if len(parts) == 0 {
		cue.Text = wrapCueText(text)
		return []srtutil.Cue{cue}
	}
	result := make([]srtutil.Cue, 0, len(parts))
	start := cue.Start
	remaining := cue.End - cue.Start
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		end := cue.End
		if i < len(parts)-1 {
			partDur, otherDur := proportionalDurations(remaining, part, strings.Join(parts[i+1:], " "))
			if partDur < minSubtitleCueDuration || otherDur < minSubtitleCueDuration {
				result = nil
				break
			}
			end = start + partDur
			remaining = otherDur
		}
		result = append(result, srtutil.Cue{Start: start, End: end, Text: wrapCueText(part)})
		start = end
	}
	if len(result) == 0 {
		cue.Text = wrapCueText(text)
		return []srtutil.Cue{cue}
	}
	if result[len(result)-1].End < cue.End {
		result[len(result)-1].End = cue.End
	}
	return result
}

func splitDisplayText(text string, duration float64) []string {
	text = normalizeCueDisplayText(text)
	if text == "" {
		return nil
	}
	if !shouldSplitDisplayText(text, duration) {
		return []string{text}
	}
	idx := bestCueSplitIndex(text)
	if idx <= 0 || idx >= len(text) {
		return []string{text}
	}
	left := strings.TrimSpace(text[:idx])
	right := strings.TrimSpace(text[idx:])
	if left == "" || right == "" {
		return []string{text}
	}
	leftDur, rightDur := proportionalDurations(duration, left, right)
	if leftDur < minSplitCuePartDur || rightDur < minSplitCuePartDur {
		return []string{text}
	}
	if runeLen(left) < minSplitCueChars || runeLen(right) < minSplitCueChars {
		return []string{text}
	}
	out := append([]string{}, splitDisplayText(left, leftDur)...)
	out = append(out, splitDisplayText(right, rightDur)...)
	return out
}

func shouldSplitDisplayText(text string, duration float64) bool {
	chars := runeLen(text)
	if duration >= minSubtitleCueDuration*2 && chars > maxSubtitleCharsPerCue {
		return true
	}
	if duration >= minSubtitleCueDuration*2 && hasMultipleSentences(text) && chars > maxSubtitleCharsPerLine {
		return true
	}
	if duration > maxSubtitleCueDuration && chars > maxSubtitleCharsPerLine {
		return true
	}
	return false
}

func hasMultipleSentences(text string) bool {
	count := 0
	for _, r := range text {
		if r == '.' || r == '!' || r == '?' {
			count++
			if count >= 2 {
				return true
			}
		}
	}
	return false
}

func bestCueSplitIndex(text string) int {
	text = normalizeCueDisplayText(text)
	if text == "" {
		return -1
	}
	half := len(text) / 2
	bestIdx := -1
	bestScore := math.MinInt
	for i := 1; i < len(text)-1; i++ {
		if text[i] != ' ' {
			continue
		}
		left := strings.TrimSpace(text[:i])
		right := strings.TrimSpace(text[i:])
		if left == "" || right == "" {
			continue
		}
		score := -absInt(i - half)
		score -= absInt(runeLen(left) - runeLen(right))
		score -= maxInt(0, runeLen(left)-maxSubtitleCharsPerCue)
		score -= maxInt(0, runeLen(right)-maxSubtitleCharsPerCue)
		switch lastRune(left) {
		case '.', '!', '?':
			score += 200
		case ';', ':':
			score += 150
		case ',':
			score += 100
		}
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	return bestIdx
}

func endsStrongSentence(text string) bool {
	switch lastRune(strings.TrimSpace(text)) {
	case '.', '!', '?':
		return true
	default:
		return false
	}
}

func wrapCueText(text string) string {
	text = normalizeCueDisplayText(text)
	if text == "" || runeLen(text) <= maxSubtitleCharsPerLine {
		return text
	}
	bestIdx := -1
	bestScore := math.MinInt
	for i := 1; i < len(text)-1; i++ {
		if text[i] != ' ' {
			continue
		}
		left := strings.TrimSpace(text[:i])
		right := strings.TrimSpace(text[i:])
		if left == "" || right == "" {
			continue
		}
		leftLen := runeLen(left)
		rightLen := runeLen(right)
		if leftLen > maxSubtitleCharsPerLine || rightLen > maxSubtitleCharsPerLine {
			continue
		}
		score := -absInt(leftLen - rightLen)
		score -= absInt(i - len(text)/2)
		switch lastRune(left) {
		case '.', '!', '?':
			score += 80
		case ';', ':', ',':
			score += 40
		}
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	if bestIdx == -1 {
		softLimit := maxSubtitleCharsPerLine + 4
		for i := 1; i < len(text)-1; i++ {
			if text[i] != ' ' {
				continue
			}
			left := strings.TrimSpace(text[:i])
			right := strings.TrimSpace(text[i:])
			if left == "" || right == "" {
				continue
			}
			leftLen := runeLen(left)
			rightLen := runeLen(right)
			if leftLen > softLimit || rightLen > softLimit {
				continue
			}
			score := -maxInt(leftLen, rightLen)
			score -= absInt(leftLen - rightLen)
			if minInt(leftLen, rightLen) <= 3 {
				score -= 100
			}
			if score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}
	}
	if bestIdx == -1 {
		return text
	}
	return strings.TrimSpace(text[:bestIdx]) + "\n" + strings.TrimSpace(text[bestIdx:])
}

func normalizeCueDisplayText(text string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(text, "\n", " ")), " ")
}

func proportionalDurations(total float64, leftText, rightText string) (float64, float64) {
	leftWeight := math.Max(1, float64(runeLen(strings.TrimSpace(leftText))))
	rightWeight := math.Max(1, float64(runeLen(strings.TrimSpace(rightText))))
	leftDur := total * (leftWeight / (leftWeight + rightWeight))
	rightDur := total - leftDur
	return leftDur, rightDur
}

func runeLen(text string) int {
	return utf8.RuneCountInString(text)
}

func lastRune(text string) rune {
	r, _ := utf8.DecodeLastRuneInString(text)
	return r
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
