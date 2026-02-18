package subtitles

import (
	"fmt"
	"math"
	"sort"
)

func buildSuspectError(errors []durationMismatchError) error {
	if len(errors) == 0 {
		return nil
	}
	deltas := make([]float64, 0, len(errors))
	anySuspect := false
	for _, e := range errors {
		if e.videoSeconds <= 0 {
			continue // Skip invalid entries rather than returning nil
		}
		deltas = append(deltas, e.deltaSeconds)
		rel := math.Abs(e.deltaSeconds) / e.videoSeconds
		// Flag as suspect if EITHER threshold is exceeded (stricter than before)
		if math.Abs(e.deltaSeconds) >= suspectOffsetSeconds || rel >= suspectRuntimeMismatchRatio {
			anySuspect = true
		}
	}
	if len(deltas) == 0 {
		return nil // No valid errors to check
	}
	if anySuspect {
		return suspectMisIdentificationError{deltas: deltas}
	}
	return nil
}

func checkSubtitleDuration(path string, videoSeconds float64) (float64, bool, error) {
	if videoSeconds <= 0 {
		return 0, false, nil
	}
	last, err := lastSRTTimestamp(path)
	if err != nil {
		return 0, false, err
	}
	if last <= 0 {
		return 0, false, nil
	}
	delta := videoSeconds - last

	// Asymmetric check: credits are normal, subtitle longer than video is suspicious
	if delta > 0 {
		// Subtitle is shorter than video (normal - credits have no dialogue)
		// Allow up to 10 minutes for credits after alignment
		if delta <= postAlignmentCreditsToleranceSeconds {
			return delta, false, nil
		}
	} else {
		// Subtitle is longer than video (suspicious)
		// Only allow small tolerance for timing drift
		if -delta <= subtitleDurationToleranceSeconds {
			return delta, false, nil
		}
	}
	return delta, true, nil
}

// earlyDurationPreCheck performs a quick duration sanity check before expensive
// alignment. Returns true if the subtitle duration is obviously wrong.
//
// The check is asymmetric because subtitles normally end before video ends (credits):
//   - Subtitle shorter than video by up to 10 minutes: OK (credits have no dialogue)
//   - Subtitle longer than video by more than 60 seconds: Suspicious (wrong cut?)
//   - Subtitle shorter than video by more than 10 minutes: Suspicious (wrong movie?)
func earlyDurationPreCheck(path string, videoSeconds float64) (delta float64, reject bool) {
	if videoSeconds <= 0 {
		return 0, false
	}
	last, err := lastSRTTimestamp(path)
	if err != nil || last <= 0 {
		return 0, false // Can't determine, proceed with alignment
	}
	delta = videoSeconds - last

	if delta > 0 {
		// Subtitle is shorter than video (normal - credits have no dialogue)
		// Allow up to 10 minutes (600s) for credits
		if delta > earlyDurationCreditsToleranceSeconds {
			return delta, true // Subtitle way too short, likely wrong movie
		}
	} else {
		// Subtitle is longer than video (suspicious - wrong cut or movie?)
		// Be stricter: only allow 60 seconds
		if -delta > earlyDurationOverlapToleranceSeconds {
			return delta, true // Subtitle longer than video, likely wrong cut
		}
	}
	return delta, false
}

// sparseSubtitleResult holds information about why subtitles were considered too sparse.
type sparseSubtitleResult struct {
	cueCount       int
	videoMinutes   float64
	cuesPerMinute  float64
	coverageRatio  float64
	reason         string
	subtitleEndSec float64
}

func (s sparseSubtitleResult) Error() string {
	return fmt.Sprintf("sparse subtitles: %s (%.1f cues/min, %.0f%% coverage)",
		s.reason, s.cuesPerMinute, s.coverageRatio*100)
}

// checkSubtitleDensity validates that subtitle cue count and coverage are reasonable.
// Returns nil if acceptable, or a sparseSubtitleResult error if the subtitles appear
// incomplete or wrong for the video.
//
// This catches cases like 143 cues for a 126-minute movie (1.1 cues/min vs expected 6-12).
func checkSubtitleDensity(path string, videoSeconds float64, cueCount int) *sparseSubtitleResult {
	if videoSeconds <= 0 || cueCount <= 0 {
		return nil // Can't validate, proceed
	}

	videoMinutes := videoSeconds / 60.0
	cuesPerMinute := float64(cueCount) / videoMinutes

	// Get subtitle bounds to calculate coverage
	start, last, err := subtitleBounds(path)
	if err != nil {
		return nil // Can't determine bounds, proceed
	}

	// Calculate coverage: what fraction of the video has subtitle coverage?
	// Subtract intro gap from consideration (some movies have long intros without dialogue).
	effectiveStart := start
	if effectiveStart < 0 {
		effectiveStart = 0
	}
	subtitleSpan := last - effectiveStart
	if subtitleSpan <= 0 {
		return nil // Invalid bounds, proceed
	}

	// Coverage ratio: how much of the video (excluding reasonable credits) is covered?
	// Consider credits as max 10 minutes at the end.
	effectiveVideoSeconds := videoSeconds
	if effectiveVideoSeconds > postAlignmentCreditsToleranceSeconds {
		effectiveVideoSeconds -= postAlignmentCreditsToleranceSeconds * 0.5 // Assume ~5 min credits on average
	}
	coverageRatio := subtitleSpan / effectiveVideoSeconds
	if coverageRatio > 1.0 {
		coverageRatio = 1.0
	}

	// Check density: too few cues per minute indicates incomplete/wrong subtitles
	if cuesPerMinute < minCuesPerMinute {
		return &sparseSubtitleResult{
			cueCount:       cueCount,
			videoMinutes:   videoMinutes,
			cuesPerMinute:  cuesPerMinute,
			coverageRatio:  coverageRatio,
			reason:         fmt.Sprintf("only %.1f cues/min (expected >= %.1f)", cuesPerMinute, minCuesPerMinute),
			subtitleEndSec: last,
		}
	}

	// Check coverage: subtitle should span reasonable portion of the movie
	if coverageRatio < minSubtitleCoverageRatio {
		return &sparseSubtitleResult{
			cueCount:       cueCount,
			videoMinutes:   videoMinutes,
			cuesPerMinute:  cuesPerMinute,
			coverageRatio:  coverageRatio,
			reason:         fmt.Sprintf("covers only %.0f%% of video (expected >= %.0f%%)", coverageRatio*100, minSubtitleCoverageRatio*100),
			subtitleEndSec: last,
		}
	}

	return nil
}

// alignmentQualityMetrics holds statistics from comparing pre- and post-alignment SRT files.
type alignmentQualityMetrics struct {
	// Shift statistics (seconds): per-cue difference between after and before start times.
	shiftMean   float64
	shiftMedian float64
	shiftStdDev float64
	shiftMax    float64
	cueCount    int

	// Integrity counts on the post-alignment file.
	negativeTimestamps  int
	zeroDurationCues    int
	newOverlaps         int
	preExistingOverlaps int
}

// analyzeAlignmentQuality parses before/after SRT files, matches cues by index,
// and computes shift statistics and integrity metrics. FFSubsync preserves cue
// structure 1:1, so index-based matching is reliable.
func analyzeAlignmentQuality(beforePath, afterPath string) (alignmentQualityMetrics, error) {
	before, err := parseSRTCues(beforePath)
	if err != nil {
		return alignmentQualityMetrics{}, fmt.Errorf("parse before: %w", err)
	}
	after, err := parseSRTCues(afterPath)
	if err != nil {
		return alignmentQualityMetrics{}, fmt.Errorf("parse after: %w", err)
	}

	n := len(before)
	if len(after) < n {
		n = len(after)
	}
	if n == 0 {
		return alignmentQualityMetrics{}, nil
	}

	// Compute per-cue start-time shifts.
	shifts := make([]float64, n)
	for i := 0; i < n; i++ {
		shifts[i] = after[i].start - before[i].start
	}

	// Mean
	var sum float64
	for _, s := range shifts {
		sum += s
	}
	mean := sum / float64(n)

	// Median
	sorted := make([]float64, n)
	copy(sorted, shifts)
	sort.Float64s(sorted)
	var median float64
	if n%2 == 0 {
		median = (sorted[n/2-1] + sorted[n/2]) / 2
	} else {
		median = sorted[n/2]
	}

	// Stddev and max absolute shift
	var sqSum, maxShift float64
	for _, s := range shifts {
		d := s - mean
		sqSum += d * d
		if abs := math.Abs(s); abs > maxShift {
			maxShift = abs
		}
	}
	stddev := math.Sqrt(sqSum / float64(n))

	// Count integrity issues in the after file.
	var negTS, zeroDur int
	for _, c := range after {
		if c.start < 0 || c.end < 0 {
			negTS++
		}
		if c.end <= c.start {
			zeroDur++
		}
	}

	// Count overlaps: cue[i].end > cue[i+1].start.
	// Track both before and after to identify new overlaps introduced by alignment.
	beforeOverlaps := countOverlaps(before)
	afterOverlaps := countOverlaps(after)
	newOverlaps := afterOverlaps - beforeOverlaps
	if newOverlaps < 0 {
		newOverlaps = 0
	}

	return alignmentQualityMetrics{
		shiftMean:           mean,
		shiftMedian:         median,
		shiftStdDev:         stddev,
		shiftMax:            maxShift,
		cueCount:            n,
		negativeTimestamps:  negTS,
		zeroDurationCues:    zeroDur,
		newOverlaps:         newOverlaps,
		preExistingOverlaps: beforeOverlaps,
	}, nil
}

// countOverlaps returns the number of consecutive cue pairs where cue[i].end > cue[i+1].start.
func countOverlaps(cues []srtCue) int {
	count := 0
	for i := 0; i < len(cues)-1; i++ {
		if cues[i].end > cues[i+1].start {
			count++
		}
	}
	return count
}

// checkAlignmentQuality compares before/after SRT files and returns an error if
// alignment produced incoherent timing. Used after ffsubsync which preserves
// cue structure 1:1.
func checkAlignmentQuality(beforePath, afterPath, release string) *alignmentQualityError {
	m, err := analyzeAlignmentQuality(beforePath, afterPath)
	if err != nil {
		return &alignmentQualityError{
			reason:  fmt.Sprintf("analysis failed: %v", err),
			release: release,
			metrics: m,
		}
	}
	if m.cueCount == 0 {
		return nil // Nothing to compare
	}

	// Reject: negative timestamps
	if m.negativeTimestamps > 0 {
		return &alignmentQualityError{
			reason:  fmt.Sprintf("%d cues have negative timestamps", m.negativeTimestamps),
			release: release,
			metrics: m,
		}
	}

	// Reject: zero-duration cues (end <= start)
	if m.zeroDurationCues > 0 {
		return &alignmentQualityError{
			reason:  fmt.Sprintf("%d cues have zero or negative duration", m.zeroDurationCues),
			release: release,
			metrics: m,
		}
	}

	// Reject: chaotic shifts (high stddev relative to median)
	absMedian := math.Abs(m.shiftMedian)
	if absMedian > 0.5 {
		// Non-trivial median shift: check coherence ratio
		ratio := m.shiftStdDev / absMedian
		if ratio > alignmentShiftCoherenceMaxRatio {
			return &alignmentQualityError{
				reason:  fmt.Sprintf("chaotic alignment: shift stddev/|median| ratio %.2f exceeds %.2f (stddev=%.2fs, median=%.2fs)", ratio, alignmentShiftCoherenceMaxRatio, m.shiftStdDev, m.shiftMedian),
				release: release,
				metrics: m,
			}
		}
	} else if m.shiftStdDev > alignmentZeroShiftMaxStdDev {
		// Near-zero median shift: high stddev means partial failure
		return &alignmentQualityError{
			reason:  fmt.Sprintf("partial alignment failure: stddev %.2fs with near-zero median shift %.2fs", m.shiftStdDev, m.shiftMedian),
			release: release,
			metrics: m,
		}
	}

	// Reject: too many new overlaps introduced by alignment
	overlapRatio := float64(m.newOverlaps) / float64(m.cueCount)
	if overlapRatio > alignmentMaxNewOverlapRatio {
		return &alignmentQualityError{
			reason:  fmt.Sprintf("alignment introduced %d new overlaps (%.0f%% of cues, max %.0f%%)", m.newOverlaps, overlapRatio*100, alignmentMaxNewOverlapRatio*100),
			release: release,
			metrics: m,
		}
	}

	return nil
}

// checkOutputIntegrity checks an output SRT file for basic timing integrity
// without comparing to a before file. Used after WhisperX where cue structure
// can change (merge/split), making cross-file comparison unreliable.
func checkOutputIntegrity(path, release string) *alignmentQualityError {
	cues, err := parseSRTCues(path)
	if err != nil {
		return &alignmentQualityError{
			reason:  fmt.Sprintf("parse failed: %v", err),
			release: release,
		}
	}
	if len(cues) == 0 {
		return nil
	}

	var negTS, zeroDur int
	for _, c := range cues {
		if c.start < 0 || c.end < 0 {
			negTS++
		}
		if c.end <= c.start {
			zeroDur++
		}
	}

	if negTS > 0 {
		return &alignmentQualityError{
			reason:  fmt.Sprintf("%d cues have negative timestamps", negTS),
			release: release,
			metrics: alignmentQualityMetrics{negativeTimestamps: negTS, cueCount: len(cues)},
		}
	}
	if zeroDur > 0 {
		return &alignmentQualityError{
			reason:  fmt.Sprintf("%d cues have zero or negative duration", zeroDur),
			release: release,
			metrics: alignmentQualityMetrics{zeroDurationCues: zeroDur, cueCount: len(cues)},
		}
	}

	return nil
}
