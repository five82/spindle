package queue

import (
	"fmt"
	"path/filepath"
	"strings"

	"spindle/internal/textutil"
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
	value = textutil.SanitizeFileName(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.Trim(value, "-_")
	if value == "" {
		return "queue"
	}
	return value
}
