package queue

import (
	"fmt"
	"path/filepath"
	"strings"
)

// StagingRoot returns the per-item staging directory rooted at base.
// If a disc fingerprint is available it is used; otherwise it falls
// back to queue-{ID} to avoid collisions.
func (i Item) StagingRoot(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	segment := strings.TrimSpace(i.DiscFingerprint)
	if segment != "" {
		segment = strings.ToUpper(segment)
	} else {
		segment = fmt.Sprintf("queue-%d", i.ID)
	}
	segment = sanitizeSegment(segment)
	return filepath.Join(base, segment)
}

func sanitizeSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		" ", "-",
		":", "-",
		"*", "",
		"?", "",
		"\"", "",
		"<", "",
		">", "",
		"|", "",
	)
	cleaned := replacer.Replace(value)
	cleaned = strings.Trim(cleaned, "-_")
	if cleaned == "" {
		return "queue"
	}
	return cleaned
}
