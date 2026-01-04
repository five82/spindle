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
	if math.Abs(delta) <= subtitleDurationToleranceSeconds {
		return delta, false, nil
	}
	return delta, true, nil
}
