package subtitle

import (
	"math"
	"testing"

	"github.com/five82/spindle/internal/srtutil"
)

func TestCueOverlapAndTokenNormalizationBoundaries(t *testing.T) {
	if got := normalizeToken("<i>Hello</i>!"); got != "hello" {
		t.Fatalf("markup token: %q", got)
	}
	if got := normalizeToken("two words"); got != "" {
		t.Fatalf("multiword token: %q", got)
	}
	if got := normalizeAnchorText("one two three"); got != "" {
		t.Fatalf("too short anchor: %q", got)
	}
	if got := normalizeAnchorText("<i>Four</i>, useful words in this cue!"); got != "four useful words in this cue" {
		t.Fatalf("anchor: %q", got)
	}
	if got := medianAbs([]float64{-2, 5, -3, 10}); got != 4 {
		t.Fatalf("even median absolute values: %f", got)
	}
	if got := medianAbs([]float64{-2, 5, -3}); got != 3 {
		t.Fatalf("odd median: %f", got)
	}
	a := []srtutil.Cue{{Start: 1, End: 3}, {Start: 10, End: 12}}
	b := []srtutil.Cue{{Start: 2, End: 4}, {Start: 9, End: 11}}
	if got := cueTimeOverlap(a, b); math.Abs(got-.5) > .0001 {
		t.Fatalf("partial overlap: %f", got)
	}
	if got := cueTimeOverlap(a, []srtutil.Cue{{Start: 15, End: 16}}); got != 0 {
		t.Fatalf("no overlap: %f", got)
	}
	if got := cueTimeOverlap(a, []srtutil.Cue{{Start: 1, End: 1}}); got != 0 {
		t.Fatalf("zero speech: %f", got)
	}
}
