package textutil

import (
	"regexp"
	"strings"
)

var (
	controlCharRe  = regexp.MustCompile(`[\x00-\x1f\x7f]`)
	collapseSpaceRe = regexp.MustCompile(`\s{2,}`)
	notAlnumDashRe = regexp.MustCompile(`[^a-z0-9_-]`)
	multiHyphenRe  = regexp.MustCompile(`-{2,}`)
	multiSpaceRe   = regexp.MustCompile(`\s+`)
)

// SanitizeDisplayName replaces :/\ and control chars with spaces, removes ?"<>|*,
// and collapses whitespace. Falls back to "manual-import" if the result is empty.
func SanitizeDisplayName(name string) string {
	// Replace :/\ with spaces.
	r := strings.NewReplacer(":", " ", "/", " ", "\\", " ")
	s := r.Replace(name)
	// Replace control chars with spaces.
	s = controlCharRe.ReplaceAllString(s, " ")
	// Remove ?"<>|*
	for _, ch := range []string{`?`, `"`, "<", ">", "|", "*"} {
		s = strings.ReplaceAll(s, ch, "")
	}
	// Collapse whitespace and trim.
	s = collapseSpaceRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return "manual-import"
	}
	return s
}

// SanitizePathSegment replaces /\:* with dashes, removes ?"<>|, converts spaces
// to hyphens, and trims leading/trailing hyphens and underscores.
// Falls back to "queue" if the result is empty.
func SanitizePathSegment(name string) string {
	// Replace /\:* with dashes.
	r := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-")
	s := r.Replace(name)
	// Remove ?"<>|
	for _, ch := range []string{`?`, `"`, "<", ">", "|"} {
		s = strings.ReplaceAll(s, ch, "")
	}
	// Spaces to hyphens.
	s = multiSpaceRe.ReplaceAllString(s, "-")
	// Collapse multiple hyphens.
	s = multiHyphenRe.ReplaceAllString(s, "-")
	// Trim leading/trailing hyphens and underscores.
	s = strings.Trim(s, "-_")
	if s == "" {
		return "queue"
	}
	return s
}

// SanitizeToken lowercases the input, keeps [a-z0-9_-], and replaces everything
// else with underscores. Returns "unknown" for empty input.
func SanitizeToken(value string) string {
	s := strings.ToLower(value)
	s = notAlnumDashRe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "unknown"
	}
	return s
}
