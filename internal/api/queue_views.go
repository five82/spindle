package api

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// SortQueueItemsNewestFirst orders queue items by CreatedAt descending, breaking ties by ID descending.
func SortQueueItemsNewestFirst(items []QueueItem) []QueueItem {
	if len(items) == 0 {
		return nil
	}
	sorted := make([]QueueItem, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		ti := parseQueueTime(sorted[i].CreatedAt)
		tj := parseQueueTime(sorted[j].CreatedAt)
		if ti.Equal(tj) {
			return sorted[i].ID > sorted[j].ID
		}
		return ti.After(tj)
	})
	return sorted
}

func parseQueueTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	return time.Time{}
}

// ParseQueueTime exposes queue timestamp parsing for consumers that need display formatting.
func ParseQueueTime(value string) time.Time {
	return parseQueueTime(value)
}

// EpisodeDisplayLabel returns a stable episode label for CLI presentation.
func EpisodeDisplayLabel(ep EpisodeStatus) string {
	if ep.Season > 0 && ep.Episode > 0 {
		return fmt.Sprintf("S%02dE%02d", ep.Season, ep.Episode)
	}
	if strings.TrimSpace(ep.Key) != "" {
		return strings.ToUpper(strings.TrimSpace(ep.Key))
	}
	return "EP"
}

// PrimaryEpisodePath chooses the most complete available asset path.
func PrimaryEpisodePath(ep EpisodeStatus) string {
	if strings.TrimSpace(ep.FinalPath) != "" {
		return strings.TrimSpace(ep.FinalPath)
	}
	if strings.TrimSpace(ep.EncodedPath) != "" {
		return strings.TrimSpace(ep.EncodedPath)
	}
	return strings.TrimSpace(ep.RippedPath)
}

// EpisodeSubtitleSummary returns a compact subtitle summary for CLI output.
func EpisodeSubtitleSummary(ep EpisodeStatus) string {
	lang := strings.ToUpper(strings.TrimSpace(ep.SubtitleLanguage))
	source := strings.TrimSpace(ep.SubtitleSource)
	score := ep.MatchScore
	parts := make([]string, 0, 3)
	if lang != "" {
		parts = append(parts, lang)
	}
	if source != "" {
		parts = append(parts, source)
	}
	if score > 0 {
		parts = append(parts, fmt.Sprintf("score %.2f", score))
	}
	return strings.Join(parts, " · ")
}
