package subtitles

import (
	"fmt"
	"strings"
	"unicode"

	"log/slog"

	"spindle/internal/logging"
)

// filterRemoval records a single cue removed by post-transcription filtering.
type filterRemoval struct {
	cue    srtCue
	reason string // "isolated_hallucination", "repeated_hallucination", "music_symbols", "trailing_hallucination", "trailing_music"
}

// filterResult holds the surviving cues and a log of everything removed.
type filterResult struct {
	cues     []srtCue
	removals []filterRemoval
}

// Known WhisperX hallucination phrases (normalized form).
var whisperXHallucinationPhrases = map[string]bool{
	"thank you":              true,
	"thank you for watching": true,
	"thanks for watching":    true,
	"please subscribe":       true,
	"like and subscribe":     true,
	"well be right back":     true,
	"bye":                    true,
	"bye bye":                true,
	"see you next time":      true,
	"see you later":          true,
}

// filterWhisperXOutput removes WhisperX hallucination artifacts from subtitle
// cues. It runs two passes: pattern-based removal of isolated/repeated
// hallucinations throughout the file, then a trailing sweep that catches
// clustered hallucinations in the final minutes without requiring isolation.
// Cue indices are renumbered sequentially in the result.
func filterWhisperXOutput(cues []srtCue, videoSeconds float64) filterResult {
	var allRemovals []filterRemoval

	// Pass 1: remove isolated/repeated hallucinations and music symbols.
	remaining, hallucinationRemovals := removeIsolatedHallucinations(cues)
	allRemovals = append(allRemovals, hallucinationRemovals...)

	// Pass 2: sweep trailing hallucinations (relaxed isolation in final minutes).
	remaining, trailingRemovals := sweepTrailingHallucinations(remaining, videoSeconds)
	allRemovals = append(allRemovals, trailingRemovals...)

	// Renumber cue indices.
	for i := range remaining {
		remaining[i].index = i + 1
	}

	return filterResult{cues: remaining, removals: allRemovals}
}

// removeIsolatedHallucinations removes known hallucination phrases that appear
// in isolation (not mid-conversation) and music-only cues.
func removeIsolatedHallucinations(cues []srtCue) ([]srtCue, []filterRemoval) {
	if len(cues) == 0 {
		return cues, nil
	}

	remove := make([]bool, len(cues))
	var removals []filterRemoval

	// Check for repeated hallucinations: 3+ consecutive cues with identical
	// normalized text where each inter-cue gap > 10s.
	markRepeatedHallucinations(cues, remove, &removals)

	// Check individual cues for isolated hallucinations and music symbols.
	for i := range cues {
		if remove[i] {
			continue
		}

		gapBefore := gapToPrevious(cues, i)
		gapAfter := gapToNext(cues, i)
		isolated := gapBefore >= 30.0 && gapAfter >= 30.0

		// Isolated hallucination phrase.
		norm := normalizeText(cues[i].text)
		if isolated && whisperXHallucinationPhrases[norm] {
			remove[i] = true
			removals = append(removals, filterRemoval{cue: cues[i], reason: "isolated_hallucination"})
			continue
		}

		// Music-only cue that is isolated.
		if isolated && isWhisperMusicCue(cues[i].text) {
			remove[i] = true
			removals = append(removals, filterRemoval{cue: cues[i], reason: "music_symbols"})
		}
	}

	var kept []srtCue
	for i, cue := range cues {
		if !remove[i] {
			kept = append(kept, cue)
		}
	}
	return kept, removals
}

// markRepeatedHallucinations finds runs of 3+ consecutive cues with identical
// normalized text where each inter-cue gap exceeds 10 seconds, and marks them
// for removal.
func markRepeatedHallucinations(cues []srtCue, remove []bool, removals *[]filterRemoval) {
	i := 0
	for i < len(cues) {
		norm := normalizeText(cues[i].text)
		if norm == "" {
			i++
			continue
		}

		// Find the extent of the run.
		runEnd := i + 1
		for runEnd < len(cues) {
			if normalizeText(cues[runEnd].text) != norm {
				break
			}
			gap := cues[runEnd].start - cues[runEnd-1].end
			if gap <= 10.0 {
				break
			}
			runEnd++
		}

		runLen := runEnd - i
		if runLen >= 3 {
			for j := i; j < runEnd; j++ {
				remove[j] = true
				*removals = append(*removals, filterRemoval{cue: cues[j], reason: "repeated_hallucination"})
			}
		}

		i = runEnd
	}
}

// gapToPrevious returns the time gap (seconds) from the previous cue's end to
// this cue's start. For the first cue, returns the gap from time zero.
func gapToPrevious(cues []srtCue, i int) float64 {
	if i == 0 {
		return cues[i].start
	}
	return cues[i].start - cues[i-1].end
}

// gapToNext returns the time gap (seconds) from this cue's end to the next
// cue's start. For the last cue, returns a large value (effectively infinite).
func gapToNext(cues []srtCue, i int) float64 {
	if i >= len(cues)-1 {
		return 1e9
	}
	return cues[i+1].start - cues[i].end
}

// isWhisperMusicCue returns true if the raw cue text consists only of music
// notation symbols (¶, ♪, ♫, *) and whitespace.
func isWhisperMusicCue(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	for _, r := range text {
		switch {
		case r == '\u00B6': // ¶
		case r == '\u266A': // ♪
		case r == '\u266B': // ♫
		case r == '*':
		case unicode.IsSpace(r):
		default:
			return false
		}
	}
	return true
}

// sweepTrailingHallucinations removes known hallucination phrases and music
// symbols in the final minutes of a video without requiring isolation. At the
// end of a movie, "Thank you." and "¶¶" are almost certainly WhisperX
// artifacts from silence or credits music, not real dialogue.
func sweepTrailingHallucinations(cues []srtCue, videoSeconds float64) ([]srtCue, []filterRemoval) {
	const trailingWindow = 300.0 // last 5 minutes

	// Skip for short content — trailing sweep is only meaningful for
	// feature-length videos with credits sections.
	if videoSeconds < 2*trailingWindow || len(cues) == 0 {
		return cues, nil
	}

	threshold := videoSeconds - trailingWindow

	var removals []filterRemoval
	var kept []srtCue

	for _, cue := range cues {
		if cue.start < threshold {
			kept = append(kept, cue)
			continue
		}

		norm := normalizeText(cue.text)
		if whisperXHallucinationPhrases[norm] {
			removals = append(removals, filterRemoval{cue: cue, reason: "trailing_hallucination"})
			continue
		}
		if isWhisperMusicCue(cue.text) {
			removals = append(removals, filterRemoval{cue: cue, reason: "trailing_music"})
			continue
		}

		kept = append(kept, cue)
	}

	return kept, removals
}

// filterTranscriptionOutput is the integration method called from Generate().
// It parses the SRT, runs filtering, and writes back if any cues were removed.
// Errors are non-fatal — the caller should log and continue with unfiltered output.
func (s *Service) filterTranscriptionOutput(srtPath string, videoSeconds float64) error {
	cues, err := parseSRTCues(srtPath)
	if err != nil {
		return fmt.Errorf("parse srt for filtering: %w", err)
	}
	if len(cues) == 0 {
		return nil
	}

	result := filterWhisperXOutput(cues, videoSeconds)
	if len(result.removals) == 0 {
		return nil
	}

	s.logFilterSummary(result)

	if err := writeSRTCues(srtPath, result.cues); err != nil {
		return fmt.Errorf("write filtered srt: %w", err)
	}
	return nil
}

// logFilterSummary logs a summary of filter actions at INFO and individual
// removals at DEBUG.
func (s *Service) logFilterSummary(result filterResult) {
	if s.logger == nil {
		return
	}

	// Count removals by reason.
	reasons := make(map[string]int)
	for _, r := range result.removals {
		reasons[r.reason]++
	}

	attrs := []slog.Attr{
		logging.String(logging.FieldEventType, "whisperx_filter_applied"),
		logging.Int("cues_removed", len(result.removals)),
		logging.Int("cues_remaining", len(result.cues)),
	}
	for reason, count := range reasons {
		attrs = append(attrs, logging.Int("removed_"+reason, count))
	}
	s.logger.LogAttrs(nil, slog.LevelInfo, "whisperx post-filter applied", attrs...) //nolint:staticcheck // nil context is fine for slog

	// Individual removals at DEBUG.
	for _, r := range result.removals {
		s.logger.Debug("whisperx filter removed cue",
			logging.Int("cue_index", r.cue.index),
			logging.String("cue_text", r.cue.text),
			logging.String("reason", r.reason),
			logging.Float64("start", r.cue.start),
			logging.Float64("end", r.cue.end),
		)
	}
}
