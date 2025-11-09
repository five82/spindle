package identification

import (
	"regexp"
	"strings"
)

var (
	discTokenPattern  = regexp.MustCompile(`(?i)\b(?:disc|dvd|blu[- ]?ray|bd|season)\s*[0-9ivx]+\b`)
	parenthesesNoise  = regexp.MustCompile(`\([^)]*\)`)
	whitespacePattern = regexp.MustCompile(`\s+`)
)

func sanitizeQueryCandidate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	cleaned := strings.ReplaceAll(value, "_", " ")
	cleaned = parenthesesNoise.ReplaceAllString(cleaned, " ")
	cleaned = discTokenPattern.ReplaceAllString(cleaned, " ")
	cleaned = strings.ReplaceAll(cleaned, "-", " ")
	cleaned = whitespacePattern.ReplaceAllString(cleaned, " ")
	return strings.TrimSpace(cleaned)
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
	cleaned = parenthesesNoise.ReplaceAllString(cleaned, " ")
	season := 0
	if s, ok := extractSeasonNumber(cleaned); ok {
		season = s
	}
	cleaned = seasonPattern.ReplaceAllString(cleaned, " ")
	cleaned = sPattern.ReplaceAllString(cleaned, " ")
	cleaned = whitespacePattern.ReplaceAllString(cleaned, " ")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned, season
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
