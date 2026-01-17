package subtitles

import (
	"fmt"
	"math"
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
