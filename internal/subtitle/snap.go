package subtitle

import (
	"log/slog"
	"math"
	"sort"

	"github.com/five82/spindle/internal/srtutil"
	"github.com/five82/spindle/internal/transcription"
)

// loadReferenceWords reads the transcript's aligned word timestamps; adoption
// proceeds without the snap pass when they are unavailable.
func loadReferenceWords(logger *slog.Logger, jsonPath string) []transcription.Word {
	words, err := transcription.ReadAlignedWords(jsonPath)
	if err != nil {
		logger.Warn("aligned word timestamps unavailable",
			"event_type", "subtitle_word_timings_unavailable",
			"error_hint", "transcript audio.json missing or unparsable",
			"impact", "adopted cues keep ffsubsync timing; word-snap pass skipped",
			"error", err,
			"json_path", jsonPath,
		)
		return nil
	}
	return words
}

// Word-snap pass: ffsubsync corrects offset and drift globally, but each cue
// keeps the human author's lead/lag around actual speech. The reference
// transcript's wav2vec2-aligned word timestamps carry the acoustic onset of
// every line, so a cue whose text is found in the word stream near its synced
// position is shifted to start exactly on its first word. Cues that cannot be
// matched confidently keep their ffsubsync timing, which the adoption gate has
// already verified.
const (
	// snapSearchWindowSeconds bounds how far from the synced position a cue's
	// first word may be found; the gate's <=1s median anchor delta makes a
	// larger window pure wrong-instance risk on repeated lines.
	snapSearchWindowSeconds = 2.0
	// snapMinMatchedTokens and snapMinMatchedFraction gate match confidence:
	// at least two words and most of the cue's text must appear in order in
	// the transcript. Single-word cues never snap — too many false matches.
	snapMinMatchedTokens   = 2
	snapMinMatchedFraction = 0.6
	// snapMaxTokenSkip is how many transcript words may be skipped between two
	// matched cue words, tolerating ASR insertions and condensed subtitle text.
	snapMaxTokenSkip = 2
)

// snapCuesToWords shifts every confidently matched cue so its start coincides
// with the aligned start of its first spoken word (the end moves with it,
// preserving the author's duration), then restores ordering and non-overlap.
// Returns (nil, 0) when nothing snapped.
func snapCuesToWords(cues []srtutil.Cue, words []transcription.Word) ([]srtutil.Cue, int) {
	if len(cues) == 0 || len(words) == 0 {
		return nil, 0
	}
	norms := make([]string, len(words))
	for i, w := range words {
		norms[i] = normalizeToken(w.Text)
	}

	snapped := make([]srtutil.Cue, len(cues))
	copy(snapped, cues)
	count := 0
	for i := range snapped {
		delta, ok := snapDelta(snapped[i], words, norms)
		if !ok {
			continue
		}
		snapped[i].Start += delta
		snapped[i].End += delta
		count++
	}
	if count == 0 {
		return nil, 0
	}

	// Snapped starts are acoustic onsets, so they win: force starts strictly
	// increasing and trim the previous end when a shift created an overlap
	// (the next line's speech has started; the previous cue yields).
	for i := range snapped {
		if snapped[i].Start < 0 {
			snapped[i].Start = 0
		}
		if i > 0 && snapped[i].Start < snapped[i-1].Start+0.001 {
			snapped[i].Start = snapped[i-1].Start + 0.001
		}
		if snapped[i].End <= snapped[i].Start {
			snapped[i].End = snapped[i].Start + 0.001
		}
		if i > 0 && snapped[i-1].End > snapped[i].Start {
			snapped[i-1].End = snapped[i].Start
		}
	}
	return snapped, count
}

// snapDelta finds the cue's first word in the transcript word stream within
// the search window and returns the start-time shift to its aligned onset.
// The first token must match exactly (a partial-tail match would snap to the
// wrong word's onset); candidates are ranked by words matched in order, then
// by smallest shift.
func snapDelta(cue srtutil.Cue, words []transcription.Word, norms []string) (float64, bool) {
	tokens := normalizeTokens(cue.Text)
	if len(tokens) < snapMinMatchedTokens {
		return 0, false
	}
	need := int(math.Ceil(snapMinMatchedFraction * float64(len(tokens))))
	if need < snapMinMatchedTokens {
		need = snapMinMatchedTokens
	}

	lo := sort.Search(len(words), func(i int) bool {
		return words[i].Start >= cue.Start-snapSearchWindowSeconds
	})
	bestMatched := 0
	var bestDelta float64
	for i := lo; i < len(words) && words[i].Start <= cue.Start+snapSearchWindowSeconds; i++ {
		if norms[i] != tokens[0] {
			continue
		}
		matched := matchTokensForward(norms, i, tokens)
		delta := words[i].Start - cue.Start
		if matched > bestMatched || (matched == bestMatched && bestMatched > 0 && math.Abs(delta) < math.Abs(bestDelta)) {
			bestMatched, bestDelta = matched, delta
		}
	}
	if bestMatched < need {
		return 0, false
	}
	return bestDelta, true
}

// matchTokensForward counts how many cue tokens appear in order in the word
// stream starting at start (which already matches tokens[0]), skipping at
// most snapMaxTokenSkip transcript words per matched token. An unmatched cue
// token does not advance the stream: the subtitle may carry words the ASR
// missed.
func matchTokensForward(norms []string, start int, tokens []string) int {
	matched := 1
	j := start + 1
	for t := 1; t < len(tokens); t++ {
		for k := j; k < len(norms) && k <= j+snapMaxTokenSkip; k++ {
			if norms[k] == tokens[t] {
				matched++
				j = k + 1
				break
			}
		}
	}
	return matched
}
