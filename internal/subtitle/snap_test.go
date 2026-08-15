package subtitle

import (
	"math"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/srtutil"
	"github.com/five82/spindle/internal/transcription"
)

// wordsForCues spreads each cue's words evenly across its interval, modeling
// a forced-alignment word stream whose onsets are the acoustic truth.
func wordsForCues(cues []srtutil.Cue) []transcription.Word {
	var words []transcription.Word
	for _, cue := range cues {
		tokens := strings.Fields(cue.Text)
		step := (cue.End - cue.Start) / float64(len(tokens))
		for i, tok := range tokens {
			start := cue.Start + float64(i)*step
			words = append(words, transcription.Word{Text: tok, Start: start, End: start + step*0.8})
		}
	}
	return words
}

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.002
}

func TestSnapCuesToWordsShiftsToWordOnsets(t *testing.T) {
	truth := dialogueCues(10, 10, 10)
	words := wordsForCues(truth)
	candidate := shiftedCues(truth, 0.4)

	snapped, count := snapCuesToWords(candidate, words)
	if count != len(truth) {
		t.Fatalf("snapped count = %d, want %d", count, len(truth))
	}
	for i, cue := range snapped {
		if !approxEqual(cue.Start, truth[i].Start) {
			t.Fatalf("cue %d start = %.3f, want %.3f", i, cue.Start, truth[i].Start)
		}
		if !approxEqual(cue.End-cue.Start, candidate[i].End-candidate[i].Start) {
			t.Fatalf("cue %d duration changed: %.3f", i, cue.End-cue.Start)
		}
	}
}

func TestSnapCuesToWordsLeavesUnmatchedCues(t *testing.T) {
	truth := dialogueCues(5, 10, 10)
	words := wordsForCues(truth)
	candidate := shiftedCues(truth, 0.5)
	candidate[2].Text = "Completely different words spoken elsewhere entirely"

	snapped, count := snapCuesToWords(candidate, words)
	if count != len(truth)-1 {
		t.Fatalf("snapped count = %d, want %d", count, len(truth)-1)
	}
	if !approxEqual(snapped[2].Start, candidate[2].Start) {
		t.Fatalf("unmatched cue moved: %.3f", snapped[2].Start)
	}
}

func TestSnapCuesToWordsIgnoresSingleWordCues(t *testing.T) {
	words := wordsForCues([]srtutil.Cue{{Start: 10, End: 11, Text: "Yes"}})
	cues := []srtutil.Cue{{Start: 10.4, End: 11.4, Text: "Yes"}}
	if snapped, count := snapCuesToWords(cues, words); count != 0 || snapped != nil {
		t.Fatalf("single-word cue snapped: count=%d cues=%+v", count, snapped)
	}
}

func TestSnapCuesToWordsRespectsSearchWindow(t *testing.T) {
	truth := dialogueCues(5, 10, 10)
	words := wordsForCues(truth)
	candidate := shiftedCues(truth, snapSearchWindowSeconds+1)

	if snapped, count := snapCuesToWords(candidate, words); count != 0 || snapped != nil {
		t.Fatalf("out-of-window cues snapped: count=%d", count)
	}
}

func TestSnapCuesToWordsPrefersClosestRepeatedLine(t *testing.T) {
	line := "We should never have come back here"
	words := wordsForCues([]srtutil.Cue{
		{Start: 10, End: 12, Text: line},
		{Start: 12.5, End: 14.5, Text: line},
	})
	cues := []srtutil.Cue{
		{Start: 12.3, End: 14.3, Text: line},
		{Start: 20, End: 22, Text: "An unrelated closing line for padding"},
	}
	snapped, count := snapCuesToWords(cues, words)
	if count != 1 {
		t.Fatalf("count = %d", count)
	}
	if !approxEqual(snapped[0].Start, 12.5) {
		t.Fatalf("snapped to %.3f, want the closer instance at 12.5", snapped[0].Start)
	}
}

func TestSnapCuesToWordsToleratesInsertedTranscriptWords(t *testing.T) {
	// The ASR heard fillers the subtitle does not carry (up to
	// snapMaxTokenSkip consecutive insertions are tolerated).
	words := wordsForCues([]srtutil.Cue{{Start: 10, End: 13, Text: "the signal was um you lost at midnight"}})
	cues := []srtutil.Cue{
		{Start: 10.6, End: 13.6, Text: "The signal was lost at midnight"},
		{Start: 20, End: 22, Text: "An unrelated closing line for padding"},
	}
	snapped, count := snapCuesToWords(cues, words)
	if count != 1 {
		t.Fatalf("count = %d", count)
	}
	if !approxEqual(snapped[0].Start, 10) {
		t.Fatalf("snapped start = %.3f, want 10", snapped[0].Start)
	}
}

func TestSnapCuesToWordsTrimsOverlapCreatedByShift(t *testing.T) {
	lineA := "The harbor was quiet before the storm arrived"
	lineB := "Nobody expected the captain to return that night"
	words := append(
		wordsForCues([]srtutil.Cue{{Start: 10, End: 12.4, Text: lineA}}),
		wordsForCues([]srtutil.Cue{{Start: 12.5, End: 15, Text: lineB}})...,
	)
	cues := []srtutil.Cue{
		{Start: 10, End: 13, Text: lineA},
		{Start: 13.2, End: 16, Text: lineB},
	}
	snapped, count := snapCuesToWords(cues, words)
	if count != 2 {
		t.Fatalf("count = %d", count)
	}
	if !approxEqual(snapped[1].Start, 12.5) {
		t.Fatalf("second cue start = %.3f, want 12.5", snapped[1].Start)
	}
	if snapped[0].End > snapped[1].Start {
		t.Fatalf("overlap survived: first ends %.3f, second starts %.3f", snapped[0].End, snapped[1].Start)
	}
}

func TestSnapCuesToWordsNoWords(t *testing.T) {
	if snapped, count := snapCuesToWords(dialogueCues(3, 10, 10), nil); count != 0 || snapped != nil {
		t.Fatalf("snap without words: count=%d", count)
	}
}
