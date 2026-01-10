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
	value = strings.ReplaceAll(value, ".", ",")
	timeParts := strings.Split(value, ",")
	if len(timeParts) != 2 {
		return 0, fmt.Errorf("invalid timestamp %q", value)
	}
	hms := strings.Split(timeParts[0], ":")
	if len(hms) != 3 {
		return 0, fmt.Errorf("invalid timestamp %q", value)
	}
	hours, err := strconv.Atoi(hms[0])
	if err != nil {
		return 0, fmt.Errorf("parse hours: %w", err)
	}
	minutes, err := strconv.Atoi(hms[1])
	if err != nil {
		return 0, fmt.Errorf("parse minutes: %w", err)
	}
	seconds, err := strconv.Atoi(hms[2])
	if err != nil {
		return 0, fmt.Errorf("parse seconds: %w", err)
	}
	millis, err := strconv.Atoi(timeParts[1])
	if err != nil {
		return 0, fmt.Errorf("parse millis: %w", err)
	}
	total := hours*3600 + minutes*60 + seconds
	return float64(total) + float64(millis)/1000, nil
}
