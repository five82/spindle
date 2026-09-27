package subtitle

import (
	"strings"
	"testing"

	"github.com/five82/spindle/internal/srtutil"
)

func TestSubtitleValidationEmptyAndBoundaryMetrics(t *testing.T) {
	for _, tc := range []struct {
		vals             []float64
		percentile, want float64
	}{
		{nil, .95, 0}, {[]float64{1, 2, 3}, 0, 1}, {[]float64{1, 2, 3}, 1, 3}, {[]float64{1, 2, 3}, .5, 2},
	} {
		if got := percentileNearestRank(tc.vals, tc.percentile); got != tc.want {
			t.Errorf("percentile(%v,%v)=%v", tc.vals, tc.percentile, got)
		}
	}
	if cueReadingSpeed(srtutil.Cue{Start: 2, End: 1, Text: "bad time"}) != 0 {
		t.Fatal("negative duration has reading speed")
	}
	if !hasOverlongLine([]string{strings.Repeat("x", maxSubtitleCharsPerLine+1)}) {
		t.Fatal("overlong line accepted")
	}
	if hasUnbalancedLineBreak([]string{"one"}) || hasUnbalancedLineBreak([]string{"", "two"}) {
		t.Fatal("short/missing line considered unbalanced")
	}
	if !hasUnbalancedLineBreak([]string{strings.Repeat("long ", 20), "short"}) {
		t.Fatal("uneven lines accepted")
	}
	if isLowInformationLongCue("", 13) || isLowInformationLongCueMetrics(0, 1, 1) || isLowInformationLongCueMetrics(13, 0, 1) {
		t.Fatal("empty cue flagged as low information")
	}
	if !isLowInformationLongCue("Yes", 12) || !isLowInformationLongCue("No", 8) {
		t.Fatal("long single-word cue not flagged")
	}
	if lexicalWordCount("It's all well-known") < 3 {
		t.Fatal("unicode and hyphen words lost")
	}
}
