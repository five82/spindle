package subtitles

import (
	"fmt"
	"strings"
)

// BuildTranscriptCacheKey constructs a stable cache key for queue-specific transcripts.
func BuildTranscriptCacheKey(itemID int64, episodeKey string) string {
	if itemID <= 0 {
		return ""
	}
	token := strings.TrimSpace(strings.ToLower(episodeKey))
	if token == "" {
		return ""
	}
	return fmt.Sprintf("queue-%d/%s", itemID, token)
}
