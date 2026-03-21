package subtitle

import (
	"os"
)

// ValidateSRTContent checks SRT content for quality issues. Returns a list of
// issue strings (empty means passed). Issues flag for review but do not fail
// the stage.
func ValidateSRTContent(srtPath string, videoSeconds float64) ([]string, error) {
	content, err := os.ReadFile(srtPath)
	if err != nil {
		return nil, err
	}

	cues := parseSRT(string(content))
	var issues []string

	// Check: no cues at all.
	if len(cues) == 0 {
		return []string{"empty_subtitle_file"}, nil
	}

	// Check: last cue end exceeds video duration by more than 8 seconds.
	lastEnd := cues[len(cues)-1].End
	if lastEnd > videoSeconds+8 {
		issues = append(issues, "duration_mismatch")
	}

	// Check: sparse subtitles (< 2 cues per minute for videos > 60s).
	if videoSeconds > 60 {
		cuesPerMin := float64(len(cues)) / (videoSeconds / 60)
		if cuesPerMin < 2 {
			issues = append(issues, "sparse_subtitles")
		}
	}

	// Check: first cue starts very late (> 900 seconds / 15 minutes).
	if cues[0].Start > 900 {
		issues = append(issues, "late_first_cue")
	}

	return issues, nil
}
