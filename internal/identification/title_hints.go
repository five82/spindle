package identification

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	whitespacePattern = regexp.MustCompile(`\s+`)
)

var parenthesesStripper = strings.NewReplacer("(", " ", ")", " ")
var discNoisePattern = regexp.MustCompile(`(?i)\b(?:disc|dvd|blu[- ]?ray|bd)\s*[0-9ivxlcdm]*\b`)
var trailingYearPattern = regexp.MustCompile(`(?i)\s*(?:\(|\b)(\d{4})\)?\s*$`)

func sanitizeQueryCandidate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	cleaned := strings.ReplaceAll(value, "_", " ")
	cleaned = strings.ReplaceAll(cleaned, "-", " ")
	cleaned = strings.ReplaceAll(cleaned, "–", " ")
	cleaned = parenthesesStripper.Replace(cleaned)
	cleaned = strings.ReplaceAll(cleaned, "-", " ")
	cleaned = whitespacePattern.ReplaceAllString(cleaned, " ")
	return strings.TrimSpace(cleaned)
}

func splitTitleYear(value string) (string, int) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", 0
	}
	matches := trailingYearPattern.FindStringSubmatch(trimmed)
	if len(matches) != 2 {
		return trimmed, 0
	}
	year, err := strconv.Atoi(matches[1])
	if err != nil || year < 1880 || year > 2100 {
		return trimmed, 0
	}
	cleaned := strings.TrimSpace(trailingYearPattern.ReplaceAllString(trimmed, ""))
	if cleaned == "" {
		return trimmed, 0
	}
	cleaned = whitespacePattern.ReplaceAllString(cleaned, " ")
	return cleaned, year
}

func deriveShowHint(values ...string) (string, int) {
	bestTitle := ""
	bestSeason := 0
	for _, candidate := range values {
		show, season := splitShowSeason(candidate)
		if show == "" {
			continue
		}
		if bestTitle == "" {
			bestTitle = show
			bestSeason = season
			continue
		}
		if bestSeason == 0 && season > 0 {
			bestSeason = season
		}
	}
	return bestTitle, bestSeason
}

func splitShowSeason(value string) (string, int) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", 0
	}
	cleaned := strings.ReplaceAll(value, "_", " ")
	cleaned = parenthesesStripper.Replace(cleaned)
	season := 0
	if s, ok := extractSeasonNumber(cleaned); ok {
		season = s
	}
	cleaned = seasonPattern.ReplaceAllString(cleaned, " ")
	cleaned = sPattern.ReplaceAllString(cleaned, " ")
	cleaned = discNoisePattern.ReplaceAllString(cleaned, " ")
	cleaned = whitespacePattern.ReplaceAllString(cleaned, " ")
	cleaned = strings.TrimSpace(cleaned)
	cleaned = trimLeadingNoise(cleaned)
	cleaned = stripPunctuationTokens(cleaned)
	return cleaned, season
}

func trimLeadingNoise(value string) string {
	if value == "" {
		return ""
	}
	tokens := strings.Fields(value)
	start := 0
	for start < len(tokens) {
		if hasLower(tokens[start]) {
			break
		}
		start++
	}
	if start >= len(tokens) {
		return value
	}
	return strings.Join(tokens[start:], " ")
}

func hasLower(value string) bool {
	for _, r := range value {
		if unicode.IsLower(r) {
			return true
		}
	}
	return false
}

func stripPunctuationTokens(value string) string {
	if value == "" {
		return ""
	}
	tokens := strings.Fields(value)
	filtered := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if hasAlphaNumeric(token) {
			filtered = append(filtered, token)
		}
	}
	return strings.Join(filtered, " ")
}

func hasAlphaNumeric(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func buildQueryList(candidates ...string) []string {
	seen := make(map[string]struct{}, len(candidates))
	ordered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		clean := sanitizeQueryCandidate(candidate)
		if clean == "" {
			continue
		}
		key := strings.ToLower(clean)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ordered = append(ordered, clean)
	}
	return ordered
}
