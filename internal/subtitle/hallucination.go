package subtitle

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/five82/spindle/internal/srtutil"
)

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

// filterWhisperXOutput applies hallucination filtering to SRT content.
// Returns filtered SRT content or an error if zero cues survive.
func filterWhisperXOutput(srtContent string, videoSeconds float64) (string, error) {
	cues := srtutil.Parse(srtContent)
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

	return srtutil.Format(cues), nil
}

// removeIsolatedHallucinations removes:
// 1. Runs of 3+ consecutive cues with identical normalized text where each
//    inter-cue gap exceeds 10 seconds.
// 2. Cues with known hallucination phrases isolated by >= 30s gaps on both sides.
// 3. Cues matching music patterns isolated by >= 30s gaps on both sides.
func removeIsolatedHallucinations(cues []srtutil.Cue) []srtutil.Cue {
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
func sweepTrailingHallucinations(cues []srtutil.Cue, videoSeconds float64) []srtutil.Cue {
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

// gapBeforeCue returns the gap in seconds before the cue at index i.
// Returns +Inf for the first cue.
func gapBeforeCue(cues []srtutil.Cue, i int) float64 {
	if i == 0 {
		return cues[0].Start // gap from start of video
	}
	return cues[i].Start - cues[i-1].End
}

// gapAfterCue returns the gap in seconds after the cue at index i.
// Returns +Inf for the last cue.
func gapAfterCue(cues []srtutil.Cue, i int) float64 {
	if i >= len(cues)-1 {
		return 1e9 // effectively infinite
	}
	return cues[i+1].Start - cues[i].End
}

// compactCues returns cues with marked entries removed.
func compactCues(cues []srtutil.Cue, remove []bool) []srtutil.Cue {
	result := make([]srtutil.Cue, 0, len(cues))
	for i, cue := range cues {
		if !remove[i] {
			result = append(result, cue)
		}
	}
	return result
}
