package subtitles

import (
	"fmt"
	"os"
	"strings"
)

// MiddleSRTRange computes start/end seconds for the middle segment of an SRT file.
// If total duration <= 2*halfWindowSec, returns the full subtitle range.
func MiddleSRTRange(path string, halfWindowSec float64) (startSec, endSec float64, err error) {
	first, last, err := subtitleBounds(path)
	if err != nil {
		return 0, 0, fmt.Errorf("subtitle bounds: %w", err)
	}
	if last <= first {
		return first, last, nil
	}
	duration := last - first
	if duration <= 2*halfWindowSec {
		return first, last, nil
	}
	mid := first + duration/2
	return mid - halfWindowSec, mid + halfWindowSec, nil
}

// ExtractSRTTimeRange reads an SRT file and returns plain-text dialogue lines
// whose start timestamp falls within [startSec, endSec].
// Ad cues are excluded. Returns empty string if no cues fall in range.
func ExtractSRTTimeRange(path string, startSec, endSec float64) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read srt: %w", err)
	}
	normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
	blocks := splitBlocks(normalized)
	if len(blocks) == 0 {
		return "", nil
	}

	var lines []string
	for _, block := range blocks {
		if blockIsAdvertisement(block) {
			continue
		}
		blockLines := strings.Split(block, "\n")

		// Find the timestamp line and extract start time.
		ts, ok := blockStartTimestamp(blockLines)
		if !ok {
			continue
		}
		if ts < startSec || ts > endSec {
			continue
		}
		text := subtitleTextLines(blockLines)
		if len(text) > 0 {
			lines = append(lines, text...)
		}
	}
	return strings.Join(lines, "\n"), nil
}

// blockStartTimestamp extracts the start timestamp (in seconds) from an SRT cue block's lines.
func blockStartTimestamp(lines []string) (float64, bool) {
	for _, line := range lines {
		if !strings.Contains(line, "-->") {
			continue
		}
		parts := strings.Split(line, "-->")
		if len(parts) != 2 {
			continue
		}
		ts, err := parseSRTTimestamp(strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}
		return ts, true
	}
	return 0, false
}
