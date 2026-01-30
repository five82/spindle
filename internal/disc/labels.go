package disc

import (
	"regexp"
	"strings"
)

var (
	allDigitsPattern = regexp.MustCompile(`^\d+$`)
	shortCodePattern = regexp.MustCompile(`^[A-Z0-9_]{1,4}$`)
)

// IsUnusableLabel returns true if the label cannot be used for content identification.
// This includes generic labels, technical labels, and patterns that don't represent
// meaningful content titles.
func IsUnusableLabel(label string) bool {
	label = strings.TrimSpace(label)
	if label == "" {
		return true
	}

	upper := strings.ToUpper(label)

	// Generic/technical patterns that indicate the label is not a real title
	patterns := []string{
		"LOGICAL_VOLUME_ID", "VOLUME_ID", "DVD_VIDEO", "BLURAY", "BD_ROM",
		"UNTITLED", "UNKNOWN DISC", "VOLUME_", "VOLUME ID", "DISK_", "TRACK_",
	}
	for _, pattern := range patterns {
		if strings.Contains(upper, pattern) {
			return true
		}
	}

	// All digits (e.g., "12345")
	if allDigitsPattern.MatchString(label) {
		return true
	}

	// Very short codes (e.g., "ABC", "X1")
	if shortCodePattern.MatchString(upper) {
		return true
	}

	// Disc label pattern (e.g., "MOVIE_DISC_1", "FILM_DISK_2")
	if (strings.Contains(upper, "DISC") || strings.Contains(upper, "DISK")) &&
		strings.Contains(upper, "_") {
		return true
	}

	// All uppercase with underscores, longer than 8 chars (technical label like "SOME_MOVIE_TITLE_DISC")
	if strings.Contains(label, "_") && label == upper && len(label) > 8 {
		return true
	}

	return false
}
