package api

import (
	"encoding/json"
	"slices"
	"time"

	"spindle/internal/queue"
	"spindle/internal/stage"
	"spindle/internal/workflow"
)

// FromQueueItem converts a queue record to its API representation.
func FromQueueItem(item *queue.Item) QueueItem {
	if item == nil {
		return QueueItem{}
	}

	dto := QueueItem{
		ID:             item.ID,
		DiscTitle:      item.DiscTitle,
		SourcePath:     item.SourcePath,
		Status:         string(item.Status),
		ProcessingLane: string(queue.LaneForItem(item)),
		Progress: QueueProgress{
			Stage:   item.ProgressStage,
			Percent: item.ProgressPercent,
			Message: item.ProgressMessage,
		},
		ErrorMessage:      item.ErrorMessage,
		DiscFingerprint:   item.DiscFingerprint,
		RippedFile:        item.RippedFile,
		EncodedFile:       item.EncodedFile,
		FinalFile:         item.FinalFile,
		BackgroundLogPath: item.BackgroundLogPath,
		NeedsReview:       item.NeedsReview,
		ReviewReason:      item.ReviewReason,
	}

	if !item.CreatedAt.IsZero() {
		dto.CreatedAt = item.CreatedAt.UTC().Format(dateTimeFormat)
	}
	if !item.UpdatedAt.IsZero() {
		dto.UpdatedAt = item.UpdatedAt.UTC().Format(dateTimeFormat)
	}
	if raw := item.MetadataJSON; raw != "" {
		dto.Metadata = json.RawMessage(raw)
	}
	if raw := item.RipSpecData; raw != "" {
		dto.RipSpec = json.RawMessage(raw)
	}
	return dto
}

// FromQueueItems converts a slice of queue records into API DTOs.
func FromQueueItems(items []*queue.Item) []QueueItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]QueueItem, 0, len(items))
	for _, item := range items {
		out = append(out, FromQueueItem(item))
	}
	return out
}

// FromStatusSummary converts a workflow status summary to API payload.
func FromStatusSummary(summary workflow.StatusSummary) WorkflowStatus {
	healthNames := make([]string, 0, len(summary.StageHealth))
	for name := range summary.StageHealth {
		healthNames = append(healthNames, name)
	}
	slices.Sort(healthNames)

	health := make([]StageHealth, 0, len(healthNames))
	for _, name := range healthNames {
		h := summary.StageHealth[name]
		health = append(health, StageHealth{
			Name:   name,
			Ready:  h.Ready,
			Detail: h.Detail,
		})
	}

	stats := make(map[string]int, len(summary.QueueStats))
	for status, count := range summary.QueueStats {
		stats[string(status)] = count
	}

	wf := WorkflowStatus{
		Running:     summary.Running,
		QueueStats:  stats,
		StageHealth: health,
	}

	if summary.LastError != "" {
		wf.LastError = summary.LastError
	}
	if summary.LastItem != nil {
		last := FromQueueItem(summary.LastItem)
		wf.LastItem = &last
	}
	return wf
}

// MergeQueueStats produces a string-keyed representation of queue stats.
func MergeQueueStats(stats map[queue.Status]int) map[string]int {
	out := make(map[string]int, len(stats))
	for status, count := range stats {
		out[string(status)] = count
	}
	return out
}

// StageHealthSlice converts a stage health map into a deterministic slice.
func StageHealthSlice(health map[string]stage.Health) []StageHealth {
	if len(health) == 0 {
		return nil
	}
	names := make([]string, 0, len(health))
	for name := range health {
		names = append(names, name)
	}
	slices.Sort(names)

	out := make([]StageHealth, 0, len(names))
	for _, name := range names {
		h := health[name]
		out = append(out, StageHealth{Name: name, Ready: h.Ready, Detail: h.Detail})
	}
	return out
}

// FormatTime converts a time to RFC3339 or returns empty string.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(dateTimeFormat)
}
