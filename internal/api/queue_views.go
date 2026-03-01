package api

import (
	"sort"
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
