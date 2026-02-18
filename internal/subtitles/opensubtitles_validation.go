package subtitles

// checkSubtitleDuration compares the last subtitle timestamp against the video duration.
// Returns the delta, whether it's a mismatch, and any error. Used by ValidateSRTContent
// to flag subtitles whose duration diverges significantly from the video.
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
		if delta <= 600.0 {
			return delta, false, nil
		}
	} else {
		// Subtitle is longer than video (suspicious)
		// Only allow small tolerance for timing drift
		if -delta <= 8.0 {
			return delta, false, nil
		}
	}
	return delta, true, nil
}
