package subtitles

import "math"

func buildSuspectError(errors []durationMismatchError) error {
	if len(errors) == 0 {
		return nil
	}
	deltas := make([]float64, 0, len(errors))
	for _, e := range errors {
		if e.videoSeconds <= 0 {
			return nil
		}
		deltas = append(deltas, e.deltaSeconds)
		rel := math.Abs(e.deltaSeconds) / e.videoSeconds
		if math.Abs(e.deltaSeconds) < suspectOffsetSeconds && rel < suspectRuntimeMismatchRatio {
			return nil
		}
	}
	return suspectMisIdentificationError{deltas: deltas}
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
