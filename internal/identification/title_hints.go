package identification

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	whitespacePattern      = regexp.MustCompile(`\s+`)
	descriptorNoisePattern = regexp.MustCompile(`(?i)\b(?:TV\s+Series|TV\s+Show|The\s+Complete\s+Series|Complete\s+Series)\b`)
)

var parenthesesStripper = strings.NewReplacer("(", " ", ")", " ")
var discNoisePattern = regexp.MustCompile(`(?i)\b(?:disc|dvd|blu[- ]?ray|bd)\s*[0-9ivxlcdm]*\b`)
var trailingYearPattern = regexp.MustCompile(`(?i)\s*(?:\(|\b)(\d{4})\)?\s*$`)

var separatorReplacer = strings.NewReplacer("_", " ", "-", " ", "–", " ")

// yearFromDate extracts a 4-character year prefix from a date string (e.g. "2024-05-12" -> "2024").
func yearFromDate(date string) string {
	date = strings.TrimSpace(date)
	if len(date) >= 4 {
		return date[:4]
	}
	return ""
}

func sanitizeQueryCandidate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	cleaned := separatorReplacer.Replace(value)
	cleaned = parenthesesStripper.Replace(cleaned)
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

	// Remove season/disc markers and descriptor noise
	cleaned = seasonPattern.ReplaceAllString(cleaned, " ")
	cleaned = sPattern.ReplaceAllString(cleaned, " ")
	cleaned = discNoisePattern.ReplaceAllString(cleaned, " ")
	cleaned = descriptorNoisePattern.ReplaceAllString(cleaned, " ")

	// Normalize whitespace and clean up
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

// discInfoPrefixes are strings that indicate parenthesized content is disc info, not a title.
var discInfoPrefixes = []string{"DISC", "VOL", "DVD", "BD"}

// extractCanonicalTitle parses a keydb-style title in the format
// "DISC_LABEL (CANONICAL_TITLE)" and returns the canonical title and disc label
// separately. If the title doesn't match this format, canonical is empty and
// label contains the original title.
//
// Examples:
//
//	"STAR_TREK_TMP (Star Trek: The Motion Picture)" → ("Star Trek: The Motion Picture", "STAR_TREK_TMP")
//	"GOODFELLAS" → ("", "GOODFELLAS")
//	"Movie (2020)" → ("", "Movie (2020)") // year-only parentheses are not canonical titles
func extractCanonicalTitle(title string) (canonical, label string) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", ""
	}

	if !strings.HasSuffix(title, ")") {
		return "", title
	}

	openIdx := findMatchingOpenParen(title)
	if openIdx <= 0 {
		return "", title
	}

	inner := strings.TrimSpace(title[openIdx+1 : len(title)-1])
	prefix := strings.TrimSpace(title[:openIdx])

	if !isValidCanonicalTitle(inner) {
		return "", title
	}

	return inner, prefix
}

// findMatchingOpenParen returns the index of the opening parenthesis that matches
// the closing parenthesis at the end of the string. Returns -1 if not found.
func findMatchingOpenParen(s string) int {
	depth := 0
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// isValidCanonicalTitle checks if a parenthesized string is a valid canonical title
// (not a year, not too short, not disc/volume info).
func isValidCanonicalTitle(inner string) bool {
	if len(inner) < 3 {
		return false
	}
	if len(inner) == 4 && isYearLike(inner) {
		return false
	}
	innerUpper := strings.ToUpper(inner)
	for _, prefix := range discInfoPrefixes {
		if strings.HasPrefix(innerUpper, prefix) {
			return false
		}
	}
	return true
}
