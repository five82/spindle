package identification

import (
	"regexp"
	"strconv"
	"strings"

	"spindle/internal/disc"
)

type mediaKind int

const (
	mediaKindUnknown mediaKind = iota
	mediaKindMovie
	mediaKindTV
)

func (k mediaKind) String() string {
	switch k {
	case mediaKindMovie:
		return "movie"
	case mediaKindTV:
		return "tv"
	default:
		return "unknown"
	}
}

var (
	seasonPattern     = regexp.MustCompile(`(?i)season\s*(\d{1,2})`)
	sPattern          = regexp.MustCompile(`(?i)\bS(\d{1,2})\b`)
	seasonWordPat     = regexp.MustCompile(`(?i)\bseason\b`)
	discNumberPattern = regexp.MustCompile(`(?i)\b(?:disc|dvd|blu[- ]?ray|bd)\s*([0-9]{1,2}|[ivxlcdm]{1,4})\b`)
)

func detectMediaKind(title, label string, scan *disc.ScanResult) mediaKind {
	titleLower := strings.ToLower(title)
	labelLower := strings.ToLower(label)
	if looksLikeTVTitle(titleLower) || looksLikeTVTitle(labelLower) {
		return mediaKindTV
	}
	if scan != nil {
		if multiEpisodeDuration(scan) {
			return mediaKindTV
		}
	}
	return mediaKindUnknown
}

func looksLikeTVTitle(value string) bool {
	if value == "" {
		return false
	}
	if seasonWordPat.MatchString(value) || sPattern.MatchString(value) {
		return true
	}
	if strings.Contains(value, "complete series") {
		return true
	}
	return false
}

func multiEpisodeDuration(scan *disc.ScanResult) bool {
	if scan == nil || len(scan.Titles) == 0 {
		return false
	}
	var episodic int
	for _, title := range scan.Titles {
		if isEpisodeRuntime(title.Duration) {
			episodic++
		}
	}
	return episodic >= 3 && episodic >= len(scan.Titles)/2
}

func isEpisodeRuntime(seconds int) bool {
	return seconds >= 18*60 && seconds <= 35*60
}

func extractSeasonNumber(candidates ...string) (int, bool) {
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if season := findSeason(candidate); season > 0 {
			return season, true
		}
	}
	return 0, false
}

func findSeason(value string) int {
	if match := seasonPattern.FindStringSubmatch(value); len(match) == 2 {
		if n, err := strconv.Atoi(match[1]); err == nil {
			return n
		}
	}
	if match := sPattern.FindStringSubmatch(value); len(match) == 2 {
		if n, err := strconv.Atoi(match[1]); err == nil {
			return n
		}
	}
	return 0
}

func extractDiscNumber(candidates ...string) (int, bool) {
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if disc := findDiscNumber(candidate); disc > 0 {
			return disc, true
		}
	}
	return 0, false
}

func findDiscNumber(value string) int {
	normalized := strings.ReplaceAll(value, "_", " ")
	normalized = strings.ReplaceAll(normalized, "-", " ")
	match := discNumberPattern.FindStringSubmatch(normalized)
	if len(match) != 2 {
		return 0
	}
	token := strings.TrimSpace(match[1])
	if token == "" {
		return 0
	}
	if n, err := strconv.Atoi(token); err == nil {
		return n
	}
	return romanToInt(token)
}

func romanToInt(input string) int {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return 0
	}
	value := 0
	prev := 0
	for i := len(input) - 1; i >= 0; i-- {
		digit := romanDigitValue(rune(input[i]))
		if digit == 0 {
			return 0
		}
		if digit < prev {
			value -= digit
		} else {
			value += digit
			prev = digit
		}
	}
	return value
}

var romanDigits = map[rune]int{
	'i': 1, 'v': 5, 'x': 10, 'l': 50, 'c': 100, 'd': 500, 'm': 1000,
}

func romanDigitValue(r rune) int {
	return romanDigits[r]
}
