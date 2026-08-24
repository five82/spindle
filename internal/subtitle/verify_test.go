package subtitle

import (
	"fmt"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/srtutil"
)

// dialogueCues builds n cues of distinct multi-word dialogue, one every
// spacing seconds starting at start, each 3 seconds long.
func dialogueCues(n int, start, spacing float64) []srtutil.Cue {
	words := []string{"harbor", "signal", "window", "captain", "evidence", "morning", "railway", "garden", "letter", "engine"}
	cues := make([]srtutil.Cue, n)
	for i := range cues {
		w := words[i%len(words)]
		cues[i] = srtutil.Cue{
			Index: i + 1,
			Start: start + float64(i)*spacing,
			End:   start + float64(i)*spacing + 3,
			Text:  fmt.Sprintf("The %s number %d was never mentioned again", w, i),
		}
	}
	return cues
}

func shiftedCues(cues []srtutil.Cue, offset float64) []srtutil.Cue {
	out := make([]srtutil.Cue, len(cues))
	for i, cue := range cues {
		cue.Start += offset
		cue.End += offset
		out[i] = cue
	}
	return out
}

func TestVerifyAdoptionCandidateAcceptsMatchingTrack(t *testing.T) {
	reference := dialogueCues(20, 10, 10)
	check := verifyAdoptionCandidate(shiftedCues(reference, 0.3), reference, 250)
	if !check.Passed {
		t.Fatalf("check = %+v", check)
	}
	if check.AnchorCues < adoptMinAnchorCues {
		t.Fatalf("expected anchor-based verification, got %+v", check)
	}
}

func TestVerifyAdoptionCandidateRejectsWrongContent(t *testing.T) {
	reference := dialogueCues(20, 10, 10)
	wrong := make([]srtutil.Cue, len(reference))
	for i, cue := range reference {
		cue.Text = fmt.Sprintf("Completely unrelated chatter about topic %d entirely elsewhere", i+100)
		wrong[i] = cue
	}
	check := verifyAdoptionCandidate(wrong, reference, 250)
	if check.Passed || !strings.Contains(check.FailureReason, "text similarity") {
		t.Fatalf("check = %+v", check)
	}
}

func TestVerifyAdoptionCandidateRejectsResidualOffset(t *testing.T) {
	reference := dialogueCues(20, 10, 10)
	check := verifyAdoptionCandidate(shiftedCues(reference, 4), reference, 250)
	if check.Passed || !strings.Contains(check.FailureReason, "anchor timing") {
		t.Fatalf("check = %+v", check)
	}
}

func TestVerifyAdoptionCandidateRejectsShortSpan(t *testing.T) {
	reference := dialogueCues(20, 10, 10)
	check := verifyAdoptionCandidate(reference[:10], reference, 250)
	if check.Passed {
		t.Fatalf("check = %+v", check)
	}
}

func TestVerifyAdoptionCandidateRejectsMissingEndingDespiteHighSpanCoverage(t *testing.T) {
	// Forrest Gump's candidate covered 93% of a long reference but ended more
	// than ten minutes before its final spoken cue.
	reference := dialogueCues(100, 10, 85)
	candidate := reference[:92]

	check := verifyAdoptionCandidate(candidate, reference, 8528)
	if check.Passed || !strings.Contains(check.FailureReason, "before the spoken reference") {
		t.Fatalf("check = %+v", check)
	}
	if check.SpanCoverage < 0.9 {
		t.Fatalf("span coverage %.3f does not exercise the proportional-coverage bug", check.SpanCoverage)
	}
	if check.ReferenceTailGap <= adoptMaxReferenceTailGapSeconds {
		t.Fatalf("reference tail gap = %.1fs", check.ReferenceTailGap)
	}
}

func TestVerifyAdoptionCandidateAllowsSparseNineMinuteASRTail(t *testing.T) {
	// Rush's downloaded track ends with the final dialogue. WhisperX then emits
	// isolated end-credit hallucinations for another 9.5 minutes; those must not
	// make an otherwise strongly matching candidate fail its tail check.
	candidate := dialogueCues(100, 10, 65)
	reference := append([]srtutil.Cue(nil), candidate...)
	lastEnd := candidate[len(candidate)-1].End
	for i, offset := range []float64{52, 264, 487, 535, 568} {
		reference = append(reference, srtutil.Cue{
			Index: len(reference) + 1,
			Start: lastEnd + offset - 1,
			End:   lastEnd + offset,
			Text:  fmt.Sprintf("isolated credit hallucination %d", i),
		})
	}

	check := verifyAdoptionCandidate(candidate, reference, lastEnd+600)
	if !check.Passed {
		t.Fatalf("check = %+v", check)
	}
	if check.ReferenceTailGap != 568 {
		t.Fatalf("reference tail gap = %.1fs, want 568s", check.ReferenceTailGap)
	}
}

func TestVerifyAdoptionCandidateAllowsLongCreditsAfterReferenceTail(t *testing.T) {
	reference := dialogueCues(20, 10, 10)
	check := verifyAdoptionCandidate(reference, reference, 1000)
	if !check.Passed {
		t.Fatalf("matching spoken tails rejected because of video credits: %+v", check)
	}
}

func TestVerifyAdoptionCandidateRejectsCuesPastVideoEnd(t *testing.T) {
	reference := dialogueCues(20, 10, 10)
	candidate := shiftedCues(reference, 0)
	candidate[len(candidate)-1].End = 400
	check := verifyAdoptionCandidate(candidate, reference, 210)
	if check.Passed || !strings.Contains(check.FailureReason, "runs past") {
		t.Fatalf("check = %+v", check)
	}
}

func TestVerifyAdoptionCandidateOverlapFallbackWithFewAnchors(t *testing.T) {
	// Reference is ASR-style wording; candidate paraphrases every cue so no
	// exact text anchors exist, but shares enough vocabulary and timing.
	reference := dialogueCues(20, 10, 10)
	paraphrased := make([]srtutil.Cue, len(reference))
	for i, cue := range reference {
		cue.Text = strings.Replace(cue.Text, "was never mentioned again", "was never mentioned", 1)
		paraphrased[i] = cue
	}
	check := verifyAdoptionCandidate(paraphrased, reference, 250)
	if !check.Passed {
		t.Fatalf("check = %+v", check)
	}
	if check.AnchorCues >= adoptMinAnchorCues || check.TimeOverlap < adoptMinTimeOverlap {
		t.Fatalf("expected overlap fallback, got %+v", check)
	}
}

func TestVerifyAdoptionCandidateRejectsSectionalOffset(t *testing.T) {
	// First three quarters aligned, last quarter 2s late: the whole-track
	// median stays near zero, but the final act is unwatchably off — the
	// intermission-cut pattern the per-segment check exists for.
	reference := dialogueCues(40, 10, 10)
	sectional := make([]srtutil.Cue, len(reference))
	for i, cue := range reference {
		if i >= 30 {
			cue.Start += 2
			cue.End += 2
		}
		sectional[i] = cue
	}
	check := verifyAdoptionCandidate(sectional, reference, 420)
	if check.Passed || !strings.Contains(check.FailureReason, "segment") {
		t.Fatalf("check = %+v", check)
	}
	if check.MedianAnchorDelta > adoptMaxMedianAnchorDelta {
		t.Fatalf("whole-track median %.2fs should have passed; the segment check must be what failed", check.MedianAnchorDelta)
	}
}

func TestRefineCueTimingRepairsLinearDrift(t *testing.T) {
	reference := dialogueCues(40, 10, 10)
	// Same content with a 2.5s offset plus 1% drift: ~6.5s off by the end,
	// the residual ffsubsync leaves when the true time base is between its
	// discrete framerate ratios.
	drifted := make([]srtutil.Cue, len(reference))
	for i, cue := range reference {
		cue.Start = cue.Start*1.01 + 2.5
		cue.End = cue.End*1.01 + 2.5
		drifted[i] = cue
	}
	if check := verifyAdoptionCandidate(drifted, reference, 420); check.Passed {
		t.Fatal("drifted track passed without refinement")
	}
	refined, ok := refineCueTiming(drifted, reference)
	if !ok {
		t.Fatal("refineCueTiming refused a linear drift")
	}
	check := verifyAdoptionCandidate(refined, reference, 420)
	if !check.Passed {
		t.Fatalf("refined track rejected: %s (%s)", check.FailureReason, check.Metrics())
	}
	if check.MedianAnchorDelta > 0.2 {
		t.Fatalf("median delta after refinement = %.2fs", check.MedianAnchorDelta)
	}
}

func TestRefineCueTimingRefusesStaircaseOffsets(t *testing.T) {
	reference := dialogueCues(40, 10, 10)
	// A TV-cut staircase (act-break gaps): +2s for the first half, +40s for
	// the second. The fitted slope is far outside a plausible drift, so the
	// repair must refuse rather than split the difference.
	staircase := make([]srtutil.Cue, len(reference))
	for i, cue := range reference {
		offset := 2.0
		if i >= len(reference)/2 {
			offset = 40.0
		}
		cue.Start += offset
		cue.End += offset
		staircase[i] = cue
	}
	if _, ok := refineCueTiming(staircase, reference); ok {
		t.Fatal("refineCueTiming accepted a staircase offset")
	}
}

func TestVerifyAdoptionCandidateEmptyInputs(t *testing.T) {
	reference := dialogueCues(5, 0, 10)
	if check := verifyAdoptionCandidate(nil, reference, 100); check.Passed {
		t.Fatalf("empty candidate passed: %+v", check)
	}
	if check := verifyAdoptionCandidate(reference, nil, 100); check.Passed {
		t.Fatalf("empty reference passed: %+v", check)
	}
}
