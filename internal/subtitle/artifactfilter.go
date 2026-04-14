package subtitle

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/five82/spindle/internal/srtutil"
)

// musicPattern matches cues containing only music symbols and whitespace.
var musicPattern = regexp.MustCompile(`^[\s\x{00B6}\x{266A}\x{266B}*]+$`)

type indexedTimedCue struct {
	Orig  int
	Start float64
	End   float64
	Text  string
}

// filterCanonicalTranscriptOutput applies generic ASR artifact filtering to SRT
// content. Returns filtered SRT content or an error if zero cues survive.
func filterCanonicalTranscriptOutput(srtContent string, videoSeconds float64) (string, error) {
	cues := srtutil.Parse(srtContent)
	if len(cues) == 0 {
		return "", fmt.Errorf("no cues found in SRT content")
	}
	indexed := make([]indexedTimedCue, 0, len(cues))
	for i, cue := range cues {
		indexed = append(indexed, indexedTimedCue{Orig: i, Start: cue.Start, End: cue.End, Text: cue.Text})
	}
	filtered, err := filterIndexedArtifacts(indexed, videoSeconds)
	if err != nil {
		return "", err
	}
	result := make([]srtutil.Cue, 0, len(filtered))
	for i, cue := range filtered {
		out := cues[cue.Orig]
		out.Index = i + 1
		result = append(result, out)
	}
	return srtutil.Format(result), nil
}

func filterIndexedArtifacts(cues []indexedTimedCue, videoSeconds float64) ([]indexedTimedCue, error) {
	if len(cues) == 0 {
		return nil, fmt.Errorf("no cues found in SRT content")
	}
	cues = removeIsolatedArtifactsIndexed(cues)
	cues = sweepTrailingArtifactsIndexed(cues, videoSeconds)
	if len(cues) == 0 {
		return nil, fmt.Errorf("all cues removed by subtitle artifact filter")
	}
	return cues, nil
}

// removeIsolatedArtifacts removes:
//  1. Runs of 3+ consecutive cues with identical normalized text where each
//     inter-cue gap exceeds 10 seconds.
//  2. Cues matching music patterns isolated by >= 30s gaps on both sides.
func removeIsolatedArtifacts(cues []srtutil.Cue) []srtutil.Cue {
	indexed := make([]indexedTimedCue, 0, len(cues))
	for i, cue := range cues {
		indexed = append(indexed, indexedTimedCue{Orig: i, Start: cue.Start, End: cue.End, Text: cue.Text})
	}
	filtered := removeIsolatedArtifactsIndexed(indexed)
	result := make([]srtutil.Cue, 0, len(filtered))
	for _, cue := range filtered {
		result = append(result, cues[cue.Orig])
	}
	return result
}

func removeIsolatedArtifactsIndexed(cues []indexedTimedCue) []indexedTimedCue {
	if len(cues) == 0 {
		return cues
	}
	remove := make([]bool, len(cues))
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
	cues = compactIndexedCues(cues, remove)
	remove = make([]bool, len(cues))
	for i, cue := range cues {
		if !musicPattern.MatchString(cue.Text) {
			continue
		}
		if gapBeforeIndexedCue(cues, i) >= 30 && gapAfterIndexedCue(cues, i) >= 30 {
			remove[i] = true
		}
	}
	return compactIndexedCues(cues, remove)
}

// sweepTrailingArtifacts removes obvious trailing non-speech artifacts in
// the last 300 seconds of a video. Only applies when videoSeconds >= 600.
func sweepTrailingArtifacts(cues []srtutil.Cue, videoSeconds float64) []srtutil.Cue {
	indexed := make([]indexedTimedCue, 0, len(cues))
	for i, cue := range cues {
		indexed = append(indexed, indexedTimedCue{Orig: i, Start: cue.Start, End: cue.End, Text: cue.Text})
	}
	filtered := sweepTrailingArtifactsIndexed(indexed, videoSeconds)
	result := make([]srtutil.Cue, 0, len(filtered))
	for _, cue := range filtered {
		result = append(result, cues[cue.Orig])
	}
	return result
}

func sweepTrailingArtifactsIndexed(cues []indexedTimedCue, videoSeconds float64) []indexedTimedCue {
	if videoSeconds < 600 || len(cues) == 0 {
		return cues
	}
	threshold := videoSeconds - 300
	remove := make([]bool, len(cues))
	for i, cue := range cues {
		if cue.Start < threshold {
			continue
		}
		if musicPattern.MatchString(cue.Text) {
			remove[i] = true
		}
	}
	return compactIndexedCues(cues, remove)
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

func gapBeforeIndexedCue(cues []indexedTimedCue, i int) float64 {
	if i == 0 {
		return cues[0].Start
	}
	return cues[i].Start - cues[i-1].End
}

func gapAfterIndexedCue(cues []indexedTimedCue, i int) float64 {
	if i >= len(cues)-1 {
		return 1e9
	}
	return cues[i+1].Start - cues[i].End
}

func compactIndexedCues(cues []indexedTimedCue, remove []bool) []indexedTimedCue {
	result := make([]indexedTimedCue, 0, len(cues))
	for i, cue := range cues {
		if !remove[i] {
			result = append(result, cue)
		}
	}
	return result
}
