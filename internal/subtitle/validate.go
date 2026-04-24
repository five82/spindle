package subtitle

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/five82/spindle/internal/srtutil"
)

const (
	maxSubtitleLinesPerCue  = 2
	maxSubtitleCharsPerLine = 42
	maxSubtitleReadingSpeed = 20.0
	minSubtitleCueDuration  = 5.0 / 6.0
	maxSubtitleCueDuration  = 7.0
	unbalancedLineDelta     = 16
)

// ValidateSRTContent checks SRT content for quality issues. Returns a list of
// issue strings (empty means passed). Severe issues are handled by the subtitle
// stage via validateCuesDetailed.
func ValidateSRTContent(srtPath string, videoSeconds float64) ([]string, error) {
	cues, err := srtutil.ParseFile(srtPath)
	if err != nil {
		return nil, err
	}
	return validateCues(cues, videoSeconds), nil
}

type validationResult struct {
	Issues       []string
	SevereIssues []string
	Stats        subtitleQCStats
}

type subtitleQCStats struct {
	CueCount                int
	MaxCPS                  float64
	P95CPS                  float64
	HighCPSCues             int
	ShortDurationCues       int
	LongDurationCues        int
	OverlongLineCues        int
	UnbalancedLineBreakCues int
	TooManyLineCues         int
}

func validateCues(cues []srtutil.Cue, videoSeconds float64) []string {
	return validateCuesDetailed(cues, videoSeconds).Issues
}

func validateCuesDetailed(cues []srtutil.Cue, videoSeconds float64) validationResult {
	if len(cues) == 0 {
		return validationResult{Issues: []string{"empty_subtitle_file"}, SevereIssues: []string{"empty_subtitle_file"}}
	}

	stats := calculateSubtitleQCStats(cues)
	seen := make(map[string]bool)
	severeSeen := make(map[string]bool)
	var issues []string
	var severe []string
	addIssue := func(issue string) {
		if issue == "" || seen[issue] {
			return
		}
		seen[issue] = true
		issues = append(issues, issue)
	}
	addSevere := func(issue string) {
		addIssue(issue)
		if issue == "" || severeSeen[issue] {
			return
		}
		severeSeen[issue] = true
		severe = append(severe, issue)
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
		if cueReadingSpeed(cue) > maxSubtitleReadingSpeed {
			addIssue("high_reading_speed")
		}
		if strings.TrimSpace(cue.Text) != "" {
			if duration > 0 && duration < minSubtitleCueDuration {
				addIssue("short_cue_duration")
			}
			if duration > maxSubtitleCueDuration {
				addIssue("long_cue_duration")
			}
			if isLowInformationLongCue(cue.Text, duration) {
				addSevere("low_information_long_cue")
			}
		}
		if i > 0 && cue.Start < cues[i-1].End {
			addSevere("overlapping_cues")
		}
	}

	return validationResult{Issues: issues, SevereIssues: severe, Stats: stats}
}

func calculateSubtitleQCStats(cues []srtutil.Cue) subtitleQCStats {
	stats := subtitleQCStats{CueCount: len(cues)}
	if len(cues) == 0 {
		return stats
	}
	cpsValues := make([]float64, 0, len(cues))
	for _, cue := range cues {
		lines := splitCueLines(cue.Text)
		if len(lines) > maxSubtitleLinesPerCue {
			stats.TooManyLineCues++
		}
		if hasOverlongLine(lines) {
			stats.OverlongLineCues++
		}
		if hasUnbalancedLineBreak(lines) {
			stats.UnbalancedLineBreakCues++
		}

		duration := cue.End - cue.Start
		if strings.TrimSpace(cue.Text) != "" {
			if duration > 0 && duration < minSubtitleCueDuration {
				stats.ShortDurationCues++
			}
			if duration > maxSubtitleCueDuration {
				stats.LongDurationCues++
			}
		}

		cps := cueReadingSpeed(cue)
		cpsValues = append(cpsValues, cps)
		if cps > stats.MaxCPS {
			stats.MaxCPS = cps
		}
		if cps > maxSubtitleReadingSpeed {
			stats.HighCPSCues++
		}
	}
	sort.Float64s(cpsValues)
	stats.P95CPS = percentileNearestRank(cpsValues, 0.95)
	return stats
}

func cueReadingSpeed(cue srtutil.Cue) float64 {
	duration := cue.End - cue.Start
	if duration <= 0 {
		return 0
	}
	lines := splitCueLines(cue.Text)
	chars := utf8.RuneCountInString(strings.Join(lines, " "))
	return float64(chars) / duration
}

func percentileNearestRank(sortedValues []float64, percentile float64) float64 {
	if len(sortedValues) == 0 {
		return 0
	}
	if percentile <= 0 {
		return sortedValues[0]
	}
	if percentile >= 1 {
		return sortedValues[len(sortedValues)-1]
	}
	idx := int(math.Ceil(percentile*float64(len(sortedValues)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sortedValues) {
		idx = len(sortedValues) - 1
	}
	return sortedValues[idx]
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

func isLowInformationLongCue(text string, duration float64) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	return isLowInformationLongCueMetrics(duration, lexicalWordCount(trimmed), utf8.RuneCountInString(trimmed))
}

func isLowInformationLongCueMetrics(duration float64, lexicalWords, textRunes int) bool {
	if duration <= 0 || lexicalWords == 0 {
		return false
	}
	if duration >= 12 && lexicalWords <= 2 {
		return true
	}
	if duration >= 8 && lexicalWords <= 1 && textRunes <= 24 {
		return true
	}
	return false
}
