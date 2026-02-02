package subtitles

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// srtCue represents a single subtitle cue with timing and text.
type srtCue struct {
	index int
	start float64
	end   float64
	text  string
}

// parseSRTCues reads an SRT file and returns all cues.
func parseSRTCues(path string) ([]srtCue, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read srt: %w", err)
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil, nil
	}

	// Split by double newlines (cue separator)
	blocks := strings.Split(content, "\n\n")
	var cues []srtCue

	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		lines := strings.Split(block, "\n")
		if len(lines) < 3 {
			continue
		}

		// First line is index
		var index int
		if _, err := fmt.Sscanf(lines[0], "%d", &index); err != nil {
			continue
		}

		// Second line is timing
		if !strings.Contains(lines[1], "-->") {
			continue
		}
		parts := strings.Split(lines[1], "-->")
		if len(parts) != 2 {
			continue
		}

		start, err := parseSRTTimestamp(parts[0])
		if err != nil {
			continue
		}
		end, err := parseSRTTimestamp(parts[1])
		if err != nil {
			continue
		}

		// Remaining lines are text
		text := strings.Join(lines[2:], "\n")

		cues = append(cues, srtCue{
			index: index,
			start: start,
			end:   end,
			text:  text,
		})
	}

	return cues, nil
}

var textNormalizeRe = regexp.MustCompile(`[^a-z0-9\s]`)

// normalizeText prepares text for comparison by lowercasing and removing punctuation.
func normalizeText(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = textNormalizeRe.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), " ")
	return s
}

// findMatchingCues finds cues from forced that match cues in reference by text similarity.
// Returns pairs of (reference cue, forced cue).
// Matching considers both text content and timing proximity.
func findMatchingCues(reference, forced []srtCue) [][2]srtCue {
	var matches [][2]srtCue

	// Maximum time difference (in seconds) to consider a potential match
	const maxTimeDiff = 60.0

	for _, fc := range forced {
		forcedNorm := normalizeText(fc.text)
		if forcedNorm == "" {
			continue
		}

		var bestMatch *srtCue
		bestScore := 0.0

		for i := range reference {
			rc := &reference[i]
			refNorm := normalizeText(rc.text)
			if refNorm == "" {
				continue
			}

			// Skip if timing is too far off (unless we have no reference point yet)
			timeDiff := fc.start - rc.start
			if timeDiff < 0 {
				timeDiff = -timeDiff
			}
			if len(matches) > 0 && timeDiff > maxTimeDiff {
				continue
			}

			// Calculate text match score
			var score float64
			if forcedNorm == refNorm {
				score = 1.0 // Exact match
			} else if strings.Contains(refNorm, forcedNorm) {
				score = 0.9 // Forced text is subset of reference
			} else if strings.Contains(forcedNorm, refNorm) {
				score = 0.8 // Reference text is subset of forced
			} else {
				overlap := wordOverlap(forcedNorm, refNorm)
				if overlap >= 0.6 {
					score = overlap * 0.7 // Partial word overlap
				}
			}

			if score > bestScore {
				bestScore = score
				bestMatch = rc
			}
		}

		if bestMatch != nil && bestScore >= 0.4 {
			matches = append(matches, [2]srtCue{*bestMatch, fc})
		}
	}

	return matches
}

// wordOverlap calculates the ratio of matching words between two strings.
func wordOverlap(a, b string) float64 {
	wordsA := strings.Fields(a)
	wordsB := strings.Fields(b)
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0
	}

	matches := 0
	for _, wa := range wordsA {
		for _, wb := range wordsB {
			if wa == wb {
				matches++
				break
			}
		}
	}

	// Use smaller set as denominator
	denom := len(wordsA)
	if len(wordsB) < denom {
		denom = len(wordsB)
	}
	return float64(matches) / float64(denom)
}

// timeTransform represents a linear time transformation: t_new = scale * t_old + offset
type timeTransform struct {
	scale  float64
	offset float64
}

// calculateTimeTransform computes a linear transformation from matched cue pairs.
// Uses linear regression if enough points, or simple two-point calculation otherwise.
func calculateTimeTransform(matches [][2]srtCue) (timeTransform, bool) {
	if len(matches) < 2 {
		return timeTransform{scale: 1.0, offset: 0}, false
	}

	// Use first and last match for simple linear fit
	first := matches[0]
	last := matches[len(matches)-1]

	// t_ref = scale * t_forced + offset
	// Using start times
	t1_forced := first[1].start
	t1_ref := first[0].start
	t2_forced := last[1].start
	t2_ref := last[0].start

	// Avoid division by zero
	if t2_forced == t1_forced {
		return timeTransform{scale: 1.0, offset: t1_ref - t1_forced}, true
	}

	scale := (t2_ref - t1_ref) / (t2_forced - t1_forced)
	offset := t1_ref - scale*t1_forced

	return timeTransform{scale: scale, offset: offset}, true
}

// applyTransform converts a time using the transformation.
func (t timeTransform) applyTransform(seconds float64) float64 {
	return t.scale*seconds + t.offset
}

// writeSRTCues writes cues to an SRT file.
func writeSRTCues(path string, cues []srtCue) error {
	var sb strings.Builder
	for i, cue := range cues {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("%d\n", cue.index))
		sb.WriteString(fmt.Sprintf("%s --> %s\n", formatSRTTimestamp(cue.start), formatSRTTimestamp(cue.end)))
		sb.WriteString(cue.text)
		sb.WriteString("\n")
	}
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

// alignForcedToReference adjusts forced subtitle timing based on a reference subtitle.
// It finds matching cues, calculates a time transformation, and applies it.
func alignForcedToReference(referencePath, forcedPath, outputPath string) (int, timeTransform, error) {
	refCues, err := parseSRTCues(referencePath)
	if err != nil {
		return 0, timeTransform{}, fmt.Errorf("parse reference: %w", err)
	}

	forcedCues, err := parseSRTCues(forcedPath)
	if err != nil {
		return 0, timeTransform{}, fmt.Errorf("parse forced: %w", err)
	}

	matches := findMatchingCues(refCues, forcedCues)
	if len(matches) < 2 {
		// Not enough matches to calculate transformation - copy as-is
		if err := copyFile(forcedPath, outputPath); err != nil {
			return 0, timeTransform{}, err
		}
		return len(matches), timeTransform{scale: 1.0, offset: 0}, nil
	}

	transform, ok := calculateTimeTransform(matches)
	if !ok {
		// Couldn't calculate transformation - copy as-is
		if err := copyFile(forcedPath, outputPath); err != nil {
			return 0, timeTransform{}, err
		}
		return len(matches), timeTransform{scale: 1.0, offset: 0}, nil
	}

	// Apply transformation to all forced cues
	adjustedCues := make([]srtCue, len(forcedCues))
	for i, cue := range forcedCues {
		adjustedCues[i] = srtCue{
			index: cue.index,
			start: transform.applyTransform(cue.start),
			end:   transform.applyTransform(cue.end),
			text:  cue.text,
		}
	}

	if err := writeSRTCues(outputPath, adjustedCues); err != nil {
		return 0, timeTransform{}, fmt.Errorf("write adjusted: %w", err)
	}

	return len(matches), transform, nil
}
