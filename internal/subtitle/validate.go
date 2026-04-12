package subtitle

import (
	"os"
	"strings"

	"github.com/five82/spindle/internal/srtutil"
)

const (
	maxSubtitleLinesPerCue  = 2
	maxSubtitleCharsPerLine = 42
	maxSubtitleReadingSpeed = 25.0
	minSubtitleCueDuration  = 0.5
	maxSubtitleCueDuration  = 7.0
	unbalancedLineDelta     = 16
)

// ValidateSRTContent checks SRT content for quality issues. Returns a list of
// issue strings (empty means passed). Issues flag for review but do not fail
// the stage.
func ValidateSRTContent(srtPath string, videoSeconds float64) ([]string, error) {
	content, err := os.ReadFile(srtPath)
	if err != nil {
		return nil, err
	}

	cues := srtutil.Parse(string(content))
	if len(cues) == 0 {
		return []string{"empty_subtitle_file"}, nil
	}

	seen := make(map[string]bool)
	var issues []string
	addIssue := func(issue string) {
		if issue == "" || seen[issue] {
			return
		}
		seen[issue] = true
		issues = append(issues, issue)
	}

	lastEnd := cues[len(cues)-1].End
	if lastEnd > videoSeconds+8 {
		addIssue("duration_mismatch")
	}
	if videoSeconds > 60 {
		cuesPerMin := float64(len(cues)) / (videoSeconds / 60)
		if cuesPerMin < 2 {
			addIssue("sparse_subtitles")
		}
	}
	if cues[0].Start > 900 {
		addIssue("late_first_cue")
	}

	for i, cue := range cues {
		lines := splitCueLines(cue.Text)
		if len(lines) > maxSubtitleLinesPerCue {
			addIssue("too_many_lines")
		}
		if hasOverlongLine(lines) {
			addIssue("line_too_long")
		}
		if hasUnbalancedLineBreak(lines) {
			addIssue("unbalanced_line_breaks")
		}
		duration := cue.End - cue.Start
		if duration > 0 {
			chars := len([]rune(strings.Join(lines, " ")))
			if float64(chars)/duration > maxSubtitleReadingSpeed {
				addIssue("high_reading_speed")
			}
		}
		if strings.TrimSpace(cue.Text) != "" {
			if duration > 0 && duration < minSubtitleCueDuration {
				addIssue("short_cue_duration")
			}
			if duration > maxSubtitleCueDuration {
				addIssue("long_cue_duration")
			}
		}
		if i > 0 && cue.Start < cues[i-1].End {
			addIssue("overlapping_cues")
		}
	}

	return issues, nil
}

func splitCueLines(text string) []string {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lines = append(lines, trimmed)
	}
	return lines
}

func hasOverlongLine(lines []string) bool {
	for _, line := range lines {
		if len([]rune(line)) > maxSubtitleCharsPerLine {
			return true
		}
	}
	return false
}

func hasUnbalancedLineBreak(lines []string) bool {
	if len(lines) != 2 {
		return false
	}
	left := len([]rune(lines[0]))
	right := len([]rune(lines[1]))
	if left == 0 || right == 0 {
		return false
	}
	if left < right {
		left, right = right, left
	}
	return left-right > unbalancedLineDelta
}
