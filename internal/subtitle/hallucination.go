package subtitle

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// srtCue represents a single subtitle cue parsed from SRT format.
type srtCue struct {
	Index int
	Start float64 // seconds
	End   float64 // seconds
	Text  string
}

// knownHallucinationPhrases contains normalized text commonly hallucinated
// by WhisperX on silence or noise.
var knownHallucinationPhrases = map[string]bool{
	"thank you":                 true,
	"thanks for watching":       true,
	"please subscribe":          true,
	"like and subscribe":        true,
	"you":                       true,
	"bye":                       true,
	"thank you for watching":    true,
	"subscribe":                 true,
	"thanks":                    true,
	"see you in the next video": true,
}

// musicPattern matches cues containing only music symbols and whitespace.
var musicPattern = regexp.MustCompile(`^[\s\x{00B6}\x{266A}\x{266B}*]+$`)

// srtTimestampPattern matches SRT timestamp lines: 00:00:00,000 --> 00:00:00,000
var srtTimestampPattern = regexp.MustCompile(
	`(\d{2}):(\d{2}):(\d{2}),(\d{3})\s*-->\s*(\d{2}):(\d{2}):(\d{2}),(\d{3})`,
)

// filterWhisperXOutput applies hallucination filtering to SRT content.
// Returns filtered SRT content or an error if zero cues survive.
func filterWhisperXOutput(srtContent string, videoSeconds float64) (string, error) {
	cues := parseSRT(srtContent)
	if len(cues) == 0 {
		return "", fmt.Errorf("no cues found in SRT content")
	}

	// Pass 1: remove isolated hallucinations.
	cues = removeIsolatedHallucinations(cues)

	// Pass 2: sweep trailing hallucinations.
	cues = sweepTrailingHallucinations(cues, videoSeconds)

	if len(cues) == 0 {
		return "", fmt.Errorf("all cues removed by hallucination filter")
	}

	// Renumber indices.
	for i := range cues {
		cues[i].Index = i + 1
	}

	return formatSRT(cues), nil
}

// removeIsolatedHallucinations removes:
// 1. Runs of 3+ consecutive cues with identical normalized text where each
//    inter-cue gap exceeds 10 seconds.
// 2. Cues with known hallucination phrases isolated by >= 30s gaps on both sides.
// 3. Cues matching music patterns isolated by >= 30s gaps on both sides.
func removeIsolatedHallucinations(cues []srtCue) []srtCue {
	if len(cues) == 0 {
		return cues
	}

	// Mark cues for removal.
	remove := make([]bool, len(cues))

	// Rule 1: runs of 3+ identical normalized text with >10s inter-cue gaps.
	i := 0
	for i < len(cues) {
		norm := normalizeText(cues[i].Text)
		j := i + 1
		for j < len(cues) && normalizeText(cues[j].Text) == norm {
			gap := cues[j].Start - cues[j-1].End
			if gap <= 10 {
				break
			}
			j++
		}
		runLen := j - i
		if runLen >= 3 {
			// Check all inter-cue gaps in the run exceed 10s.
			allGapsLarge := true
			for k := i + 1; k < j; k++ {
				if cues[k].Start-cues[k-1].End <= 10 {
					allGapsLarge = false
					break
				}
			}
			if allGapsLarge {
				for k := i; k < j; k++ {
					remove[k] = true
				}
			}
		}
		i = j
	}

	// Apply removals and compact.
	cues = compactCues(cues, remove)
	remove = make([]bool, len(cues))

	// Rule 2: isolated known phrases (gap >= 30s before AND after).
	for i, cue := range cues {
		norm := normalizeText(cue.Text)
		if !knownHallucinationPhrases[norm] {
			continue
		}
		gapBefore := gapBeforeCue(cues, i)
		gapAfter := gapAfterCue(cues, i)
		if gapBefore >= 30 && gapAfter >= 30 {
			remove[i] = true
		}
	}

	cues = compactCues(cues, remove)
	remove = make([]bool, len(cues))

	// Rule 3: isolated music patterns (gap >= 30s before AND after).
	for i, cue := range cues {
		if !musicPattern.MatchString(cue.Text) {
			continue
		}
		gapBefore := gapBeforeCue(cues, i)
		gapAfter := gapAfterCue(cues, i)
		if gapBefore >= 30 && gapAfter >= 30 {
			remove[i] = true
		}
	}

	return compactCues(cues, remove)
}

// sweepTrailingHallucinations removes known phrase and music pattern cues in
// the last 300 seconds of a video. Only applies when videoSeconds >= 600.
func sweepTrailingHallucinations(cues []srtCue, videoSeconds float64) []srtCue {
	if videoSeconds < 600 || len(cues) == 0 {
		return cues
	}

	threshold := videoSeconds - 300
	remove := make([]bool, len(cues))

	for i, cue := range cues {
		if cue.Start < threshold {
			continue
		}
		norm := normalizeText(cue.Text)
		if knownHallucinationPhrases[norm] || musicPattern.MatchString(cue.Text) {
			remove[i] = true
		}
	}

	return compactCues(cues, remove)
}

// parseSRT parses SRT formatted content into cues.
func parseSRT(content string) []srtCue {
	var cues []srtCue
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")

	i := 0
	for i < len(lines) {
		// Skip blank lines.
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
		if i >= len(lines) {
			break
		}

		// Parse index.
		idx, err := strconv.Atoi(strings.TrimSpace(lines[i]))
		if err != nil {
			i++
			continue
		}
		i++
		if i >= len(lines) {
			break
		}

		// Parse timestamp.
		m := srtTimestampPattern.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		start := parseTimestamp(m[1], m[2], m[3], m[4])
		end := parseTimestamp(m[5], m[6], m[7], m[8])
		i++

		// Collect text lines until blank line.
		var textLines []string
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
			textLines = append(textLines, lines[i])
			i++
		}

		cues = append(cues, srtCue{
			Index: idx,
			Start: start,
			End:   end,
			Text:  strings.Join(textLines, "\n"),
		})
	}

	return cues
}

// formatSRT converts cues back to SRT format.
func formatSRT(cues []srtCue) string {
	var b strings.Builder
	for i, cue := range cues {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%d\n", cue.Index)
		fmt.Fprintf(&b, "%s --> %s\n", formatTimestamp(cue.Start), formatTimestamp(cue.End))
		b.WriteString(cue.Text)
		b.WriteString("\n")
	}
	return b.String()
}

// normalizeText lowercases text, strips non-alphanumeric characters except
// spaces, and collapses whitespace.
func normalizeText(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	lastSpace := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
		} else if !lastSpace {
			b.WriteRune(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// parseTimestamp converts SRT timestamp components to seconds.
func parseTimestamp(h, m, s, ms string) float64 {
	hours, _ := strconv.Atoi(h)
	minutes, _ := strconv.Atoi(m)
	seconds, _ := strconv.Atoi(s)
	millis, _ := strconv.Atoi(ms)
	return float64(hours)*3600 + float64(minutes)*60 + float64(seconds) + float64(millis)/1000
}

// formatTimestamp converts seconds to SRT timestamp format.
func formatTimestamp(secs float64) string {
	total := int(secs * 1000)
	ms := total % 1000
	total /= 1000
	s := total % 60
	total /= 60
	m := total % 60
	h := total / 60
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// gapBeforeCue returns the gap in seconds before the cue at index i.
// Returns +Inf for the first cue.
func gapBeforeCue(cues []srtCue, i int) float64 {
	if i == 0 {
		return cues[0].Start // gap from start of video
	}
	return cues[i].Start - cues[i-1].End
}

// gapAfterCue returns the gap in seconds after the cue at index i.
// Returns +Inf for the last cue.
func gapAfterCue(cues []srtCue, i int) float64 {
	if i >= len(cues)-1 {
		return 1e9 // effectively infinite
	}
	return cues[i+1].Start - cues[i].End
}

// compactCues returns cues with marked entries removed.
func compactCues(cues []srtCue, remove []bool) []srtCue {
	result := make([]srtCue, 0, len(cues))
	for i, cue := range cues {
		if !remove[i] {
			result = append(result, cue)
		}
	}
	return result
}
