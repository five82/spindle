package subtitles

import (
	"regexp"
	"strconv"
	"strings"
)

var adPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)opensubtitles`),
	regexp.MustCompile(`(?i)subtitles? by`),
	regexp.MustCompile(`(?i)synced? and corrected`),
	regexp.MustCompile(`(?i)advertise (your|yours?) product`),
	regexp.MustCompile(`(?i)http(s)?://`),
	regexp.MustCompile(`(?i)\bwww\.`),
	regexp.MustCompile(`(?i)\bsubscene\b`),
	regexp.MustCompile(`(?i)\byts\b`),
	regexp.MustCompile(`(?i)\byify\b`),
}

// CleanStats reports the effects of subtitle cleanup operations.
type CleanStats struct {
	RemovedCues int
}

// CleanSRT removes advertisement cues and normalizes spacing in SRT subtitles.
func CleanSRT(raw []byte) ([]byte, CleanStats) {
	normalized := strings.ReplaceAll(string(raw), "\r\n", "\n")
	blocks := splitBlocks(normalized)
	cleaned := make([]string, 0, len(blocks))
	var stats CleanStats
	for _, block := range blocks {
		if blockIsAdvertisement(block) {
			stats.RemovedCues++
			continue
		}
		cleaned = append(cleaned, normalizeBlock(block))
	}
	output := strings.Join(cleaned, "\n\n")
	if !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	return []byte(output), stats
}

func splitBlocks(content string) []string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n\n")
}

func blockIsAdvertisement(block string) bool {
	lines := strings.Split(block, "\n")
	if len(lines) == 0 {
		return false
	}
	textLines := subtitleTextLines(lines)
	if len(textLines) == 0 {
		return false
	}
	payload := strings.ToLower(strings.Join(textLines, " "))
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return false
	}
	for _, pattern := range adPatterns {
		if pattern.MatchString(payload) {
			return true
		}
	}
	return false
}

func subtitleTextLines(lines []string) []string {
	start := 0
	if start < len(lines) && isNumeric(lines[start]) {
		start++
	}
	if start < len(lines) && strings.Contains(lines[start], "-->") {
		start++
	}
	if start >= len(lines) {
		return nil
	}
	text := make([]string, 0, len(lines)-start)
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			text = append(text, trimmed)
		}
	}
	return text
}

func normalizeBlock(block string) string {
	lines := strings.Split(block, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.Join(lines, "\n")
}

func isNumeric(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	_, err := strconv.Atoi(value)
	return err == nil
}
