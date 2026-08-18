package subtitle

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/five82/spindle/internal/srtutil"
	"github.com/five82/spindle/internal/textutil"
)

// Adoption gate thresholds. A downloaded subtitle becomes the display track
// only when it demonstrably matches this rip's content and timing; rejecting
// a borderline candidate costs a manual `spindle subtitle` run, while
// adopting a wrong one ships bad subtitles, so every threshold errs toward
// rejection.
const (
	// adoptMinTextSimilarity sits above contentid's 0.58 episode-match floor:
	// adoption demands more confidence than identification.
	adoptMinTextSimilarity = 0.60
	// adoptMinAnchorCues is the minimum number of exact-text cue matches for
	// the anchor timing check; below it the interval-overlap check applies.
	adoptMinAnchorCues        = 8
	adoptMaxMedianAnchorDelta = 1.0 // seconds
	// adoptAnchorSegments splits the anchors into equal time segments checked
	// against adoptMaxMedianAnchorDelta individually: a whole-track median
	// hides a section that drifted after an intermission or act break.
	// Segments with fewer than adoptMinSegmentAnchors anchors are skipped so
	// sparse-dialogue stretches cannot fail on repeated-line mismatch noise.
	adoptAnchorSegments    = 4
	adoptMinSegmentAnchors = 6
	adoptMinTimeOverlap    = 0.5
	adoptMinSpanCoverage   = 0.6
	// An absolute tail bound prevents high proportional span coverage on long
	// movies from hiding a substantially missing ending. Compare against the
	// spoken reference rather than video duration so long credits remain valid.
	adoptMaxReferenceTailGapSeconds = 10 * 60
	// adoptRefineMinSimilarity gates the anchor-based timing refinement: only
	// a candidate whose text already proves it is this title's subtitle may
	// have its timing repaired and re-verified.
	adoptRefineMinSimilarity = 0.75
	// adoptDurationSlackSeconds mirrors the validator's duration_mismatch
	// slack for cues running past the end of the video.
	adoptDurationSlackSeconds = 8.0
)

// adoptionCheck reports the verification-gate metrics for one candidate.
type adoptionCheck struct {
	TextSimilarity    float64
	AnchorCues        int
	MedianAnchorDelta float64
	TimeOverlap       float64
	SpanCoverage      float64
	ReferenceTailGap  float64
	TimingRefined     bool
	SnappedCues       int
	Passed            bool
	FailureReason     string
}

// Metrics renders the gate measurements for decision logging.
func (c adoptionCheck) Metrics() string {
	return fmt.Sprintf("text_similarity=%.3f anchor_cues=%d median_anchor_delta_s=%.2f time_overlap=%.2f span_coverage=%.2f reference_tail_gap_s=%.1f timing_refined=%t snapped_cues=%d",
		c.TextSimilarity, c.AnchorCues, c.MedianAnchorDelta, c.TimeOverlap, c.SpanCoverage, c.ReferenceTailGap, c.TimingRefined, c.SnappedCues)
}

// verifyAdoptionCandidate decides whether synced downloaded cues match the
// WhisperX reference transcript of the rip well enough to ship as the
// display subtitle.
func verifyAdoptionCandidate(candidate, reference []srtutil.Cue, videoSeconds float64) adoptionCheck {
	check := adoptionCheck{MedianAnchorDelta: -1, TimeOverlap: -1}
	if len(candidate) == 0 {
		check.FailureReason = "candidate has no cues"
		return check
	}
	if len(reference) == 0 {
		check.FailureReason = "reference transcript has no cues"
		return check
	}

	check.TextSimilarity = textutil.CosineSimilarity(
		textutil.NewFingerprint(stripMarkup(srtutil.PlainText(candidate))),
		textutil.NewFingerprint(srtutil.PlainText(reference)),
	)
	if check.TextSimilarity < adoptMinTextSimilarity {
		check.FailureReason = fmt.Sprintf("text similarity %.3f below %.2f; likely wrong content", check.TextSimilarity, adoptMinTextSimilarity)
		return check
	}

	pairs := anchorDeltas(candidate, reference)
	check.AnchorCues = len(pairs)
	if len(pairs) >= adoptMinAnchorCues {
		deltas := make([]float64, len(pairs))
		for i, p := range pairs {
			deltas[i] = p.delta
		}
		check.MedianAnchorDelta = medianAbs(deltas)
		if check.MedianAnchorDelta > adoptMaxMedianAnchorDelta {
			check.FailureReason = fmt.Sprintf("median anchor timing delta %.2fs exceeds %.1fs after sync", check.MedianAnchorDelta, adoptMaxMedianAnchorDelta)
			return check
		}
		if reason := anchorSegmentFailure(pairs); reason != "" {
			check.FailureReason = reason
			return check
		}
	} else {
		check.TimeOverlap = cueTimeOverlap(candidate, reference)
		if check.TimeOverlap < adoptMinTimeOverlap {
			check.FailureReason = fmt.Sprintf("cue time overlap %.2f below %.2f with only %d text anchors", check.TimeOverlap, adoptMinTimeOverlap, len(pairs))
			return check
		}
	}

	refSpan := reference[len(reference)-1].End - reference[0].Start
	candSpan := candidate[len(candidate)-1].End - candidate[0].Start
	if refSpan > 0 {
		check.SpanCoverage = candSpan / refSpan
		if check.SpanCoverage < adoptMinSpanCoverage {
			check.FailureReason = fmt.Sprintf("candidate spans %.0f%% of the reference; likely a different cut", check.SpanCoverage*100)
			return check
		}
	}
	check.ReferenceTailGap = reference[len(reference)-1].End - candidate[len(candidate)-1].End
	if check.ReferenceTailGap > adoptMaxReferenceTailGapSeconds {
		check.FailureReason = fmt.Sprintf("candidate ends %.0fs before the spoken reference; exceeds %ds", check.ReferenceTailGap, adoptMaxReferenceTailGapSeconds)
		return check
	}
	if videoSeconds > 0 && candidate[len(candidate)-1].End > videoSeconds+adoptDurationSlackSeconds {
		check.FailureReason = fmt.Sprintf("last cue at %.0fs runs past the %.0fs video", candidate[len(candidate)-1].End, videoSeconds)
		return check
	}

	check.Passed = true
	return check
}

// anchorPair is one exact-text timing anchor: the candidate cue's start time
// and its start-time delta against the reference cue with the same text.
type anchorPair struct {
	at    float64 // candidate cue start
	delta float64 // candidate minus reference start
}

// anchorDeltas pairs cues whose normalized text appears exactly once in both
// tracks and returns their timing anchors in candidate order.
func anchorDeltas(candidate, reference []srtutil.Cue) []anchorPair {
	type occurrence struct {
		start float64
		count int
	}
	refByText := make(map[string]*occurrence, len(reference))
	for _, cue := range reference {
		key := normalizeAnchorText(cue.Text)
		if key == "" {
			continue
		}
		if occ, ok := refByText[key]; ok {
			occ.count++
		} else {
			refByText[key] = &occurrence{start: cue.Start, count: 1}
		}
	}
	candCounts := make(map[string]int, len(candidate))
	for _, cue := range candidate {
		if key := normalizeAnchorText(cue.Text); key != "" {
			candCounts[key]++
		}
	}

	var pairs []anchorPair
	for _, cue := range candidate {
		key := normalizeAnchorText(cue.Text)
		if key == "" || candCounts[key] != 1 {
			continue
		}
		occ, ok := refByText[key]
		if !ok || occ.count != 1 {
			continue
		}
		pairs = append(pairs, anchorPair{at: cue.Start, delta: cue.Start - occ.start})
	}
	return pairs
}

// anchorSegmentFailure checks each time segment's median anchor delta,
// catching a track that is aligned overall but off by seconds for one
// stretch (an intermission or act-break cut difference).
func anchorSegmentFailure(pairs []anchorPair) string {
	if len(pairs) == 0 {
		return ""
	}
	sorted := make([]anchorPair, len(pairs))
	copy(sorted, pairs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].at < sorted[j].at })
	span := sorted[len(sorted)-1].at - sorted[0].at
	if span <= 0 {
		return ""
	}
	segments := make([][]float64, adoptAnchorSegments)
	for _, p := range sorted {
		idx := int((p.at - sorted[0].at) / span * adoptAnchorSegments)
		if idx >= adoptAnchorSegments {
			idx = adoptAnchorSegments - 1
		}
		segments[idx] = append(segments[idx], p.delta)
	}
	for i, seg := range segments {
		if len(seg) < adoptMinSegmentAnchors {
			continue
		}
		if median := medianAbs(seg); median > adoptMaxMedianAnchorDelta {
			return fmt.Sprintf("median anchor timing delta %.2fs in segment %d/%d exceeds %.1fs after sync", median, i+1, adoptAnchorSegments, adoptMaxMedianAnchorDelta)
		}
	}
	return ""
}

// refineCueTiming fits a robust line through the anchor deltas (median delta
// of the earliest third vs the latest third) and applies the inverse
// correction, repairing the constant offsets and linear framerate drift that
// ffsubsync's discrete ratio list cannot express. The caller must re-verify
// the result; a nonlinear mismatch (recut source) still fails that gate.
func refineCueTiming(candidate, reference []srtutil.Cue) ([]srtutil.Cue, bool) {
	pairs := anchorDeltas(candidate, reference)
	if len(pairs) < adoptMinAnchorCues {
		return nil, false
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].at < pairs[j].at })
	third := len(pairs) / 3
	if third == 0 {
		third = 1
	}
	head, tail := pairs[:third], pairs[len(pairs)-third:]
	t1, d1 := medianAt(head), medianDelta(head)
	t2, d2 := medianAt(tail), medianDelta(tail)
	if t2-t1 < 1 {
		return nil, false
	}
	slope := (d2 - d1) / (t2 - t1)
	intercept := d1 - slope*t1
	// A sane repair is a small drift plus a bounded offset; anything larger
	// means the anchors do not describe a linear timing error.
	if slope < -0.05 || slope > 0.05 || intercept < -120 || intercept > 120 {
		return nil, false
	}

	refined := make([]srtutil.Cue, len(candidate))
	for i, cue := range candidate {
		cue.Start -= intercept + slope*cue.Start
		cue.End -= intercept + slope*cue.End
		if cue.Start < 0 {
			cue.Start = 0
		}
		if cue.End <= cue.Start {
			cue.End = cue.Start + 0.001
		}
		refined[i] = cue
	}
	return refined, true
}

func medianAt(pairs []anchorPair) float64 {
	vals := make([]float64, len(pairs))
	for i, p := range pairs {
		vals[i] = p.at
	}
	sort.Float64s(vals)
	return vals[len(vals)/2]
}

func medianDelta(pairs []anchorPair) float64 {
	vals := make([]float64, len(pairs))
	for i, p := range pairs {
		vals[i] = p.delta
	}
	sort.Float64s(vals)
	return vals[len(vals)/2]
}

// normalizeAnchorText lowercases and strips markup and punctuation so a cue
// can be matched across ASR and human transcription; short texts are
// discarded because they repeat too often to anchor anything.
func normalizeAnchorText(text string) string {
	tokens := normalizeTokens(text)
	if len(tokens) < 4 {
		return ""
	}
	return strings.Join(tokens, " ")
}

// normalizeTokens lowercases text, strips markup and punctuation, and returns
// the remaining words, the comparable form shared by cue anchoring and the
// word-snap pass.
func normalizeTokens(text string) []string {
	text = strings.ToLower(stripMarkup(text))
	var b strings.Builder
	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		}
	}
	return strings.Fields(b.String())
}

// normalizeToken normalizes one transcript word to the same form.
func normalizeToken(word string) string {
	if tokens := normalizeTokens(word); len(tokens) == 1 {
		return tokens[0]
	}
	return ""
}

func medianAbs(values []float64) float64 {
	abs := make([]float64, len(values))
	for i, v := range values {
		if v < 0 {
			v = -v
		}
		abs[i] = v
	}
	sort.Float64s(abs)
	mid := len(abs) / 2
	if len(abs)%2 == 1 {
		return abs[mid]
	}
	return (abs[mid-1] + abs[mid]) / 2
}

// cueTimeOverlap returns the total intersection of the two cue interval sets
// relative to the smaller set's total speech time.
func cueTimeOverlap(a, b []srtutil.Cue) float64 {
	totalA := intervalTotal(a)
	totalB := intervalTotal(b)
	smaller := totalA
	if totalB < smaller {
		smaller = totalB
	}
	if smaller <= 0 {
		return 0
	}

	var intersection float64
	j := 0
	for _, cue := range a {
		for j < len(b) && b[j].End < cue.Start {
			j++
		}
		for k := j; k < len(b) && b[k].Start < cue.End; k++ {
			start := cue.Start
			if b[k].Start > start {
				start = b[k].Start
			}
			end := cue.End
			if b[k].End < end {
				end = b[k].End
			}
			if end > start {
				intersection += end - start
			}
		}
	}
	return intersection / smaller
}

func intervalTotal(cues []srtutil.Cue) float64 {
	var total float64
	for _, cue := range cues {
		if cue.End > cue.Start {
			total += cue.End - cue.Start
		}
	}
	return total
}
