package subtitles

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

func countSRTCues(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read srt: %w", err)
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return 0, nil
	}
	blocks := strings.Split(content, "\n\n")
	count := 0
	for _, block := range blocks {
		if strings.TrimSpace(block) != "" {
			count++
		}
	}
	return count, nil
}

func lastSRTTimestamp(path string) (float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read srt: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	var last float64
	for _, line := range lines {
		if !strings.Contains(line, "-->") {
			continue
		}
		parts := strings.Split(line, "-->")
		if len(parts) != 2 {
			continue
		}
		endText := strings.TrimSpace(parts[1])
		seconds, err := parseSRTTimestamp(endText)
		if err != nil {
			continue
		}
		if seconds > last {
			last = seconds
		}
	}
	return last, nil
}

func subtitleBounds(path string) (float64, float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, fmt.Errorf("read srt: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	first := math.Inf(1)
	var last float64
	found := false
	for _, line := range lines {
		if !strings.Contains(line, "-->") {
			continue
		}
		parts := strings.Split(line, "-->")
		if len(parts) != 2 {
			continue
		}
		startText := strings.TrimSpace(parts[0])
		if startSeconds, err := parseSRTTimestamp(startText); err == nil {
			if startSeconds < first {
				first = startSeconds
			}
			found = true
		}
		endText := strings.TrimSpace(parts[1])
		if endSeconds, err := parseSRTTimestamp(endText); err == nil {
			if endSeconds > last {
				last = endSeconds
			}
		}
	}
	if !found {
		return 0, last, nil
	}
	return first, last, nil
}

func parseSRTTimestamp(value string) (float64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty timestamp")
	}
	// Normalize period to comma (SRT standard uses comma for milliseconds)
	value = strings.ReplaceAll(value, ".", ",")
	timeParts := strings.Split(value, ",")
	if len(timeParts) != 2 {
		return 0, fmt.Errorf("invalid timestamp %q", value)
	}
	hms := strings.Split(timeParts[0], ":")
	if len(hms) != 3 {
		return 0, fmt.Errorf("invalid timestamp %q", value)
	}
	hours, errH := strconv.Atoi(hms[0])
	minutes, errM := strconv.Atoi(hms[1])
	seconds, errS := strconv.Atoi(hms[2])
	millis, errMS := strconv.Atoi(timeParts[1])
	if errH != nil || errM != nil || errS != nil || errMS != nil {
		return 0, fmt.Errorf("invalid timestamp %q", value)
	}
	return float64(hours*3600+minutes*60+seconds) + float64(millis)/1000, nil
}

// ValidateSRTContent checks an SRT file for format issues.
// Returns a list of issues found; empty slice means validation passed.
func ValidateSRTContent(path string, videoSeconds float64) []string {
	var issues []string

	// Check cue count > 0
	cues, err := countSRTCues(path)
	if err != nil {
		issues = append(issues, fmt.Sprintf("read_error: %v", err))
		return issues
	}
	if cues == 0 {
		issues = append(issues, "empty_subtitle_file")
		return issues
	}

	// Check timestamp format validity
	first, last, err := subtitleBounds(path)
	if err != nil {
		issues = append(issues, fmt.Sprintf("timestamp_parse_error: %v", err))
	} else if first == 0 && last == 0 {
		issues = append(issues, "no_valid_timestamps")
	}

	// Check duration alignment if video duration is known
	if videoSeconds > 0 {
		delta, suspect, err := checkSubtitleDuration(path, videoSeconds)
		if err != nil {
			issues = append(issues, fmt.Sprintf("duration_check_error: %v", err))
		} else if suspect {
			issues = append(issues, fmt.Sprintf("duration_mismatch: delta=%.1fs", delta))
		}
	}

	return issues
}
