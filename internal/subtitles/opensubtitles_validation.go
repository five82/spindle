package subtitles

import "math"

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
