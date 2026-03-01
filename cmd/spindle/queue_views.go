package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"spindle/internal/api"
)

func buildQueueStatusRows(stats map[string]int) [][]string {
	if len(stats) == 0 {
		return nil
	}
	keys := make([]string, 0, len(stats))
	for key := range stats {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	rows := make([][]string, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, []string{formatStatusLabel(key), fmt.Sprintf("%d", stats[key])})
	}
	return rows
}

func buildQueueListRows(items []api.QueueItem) [][]string {
	if len(items) == 0 {
		return nil
	}
	sorted := api.SortQueueItemsNewestFirst(items)

	rows := make([][]string, 0, len(sorted))
	for _, item := range sorted {
		title := strings.TrimSpace(item.DiscTitle)
		if title == "" {
			source := strings.TrimSpace(item.SourcePath)
			if source != "" {
				title = filepath.Base(source)
			} else {
				title = "Unknown"
			}
		}
		status := formatStatusLabel(item.Status)
		created := formatDisplayTime(item.CreatedAt)
		fingerprint := formatFingerprint(item.DiscFingerprint)
		rows = append(rows, []string{
			fmt.Sprintf("%d", item.ID),
			title,
			status,
			created,
			fingerprint,
		})
	}
	return rows
}

func formatStatusLabel(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return ""
	}
	parts := strings.Split(status, "_")
	for i, part := range parts {
		lower := strings.ToLower(part)
		if lower == "" {
			continue
		}
		parts[i] = strings.ToUpper(lower[:1]) + lower[1:]
	}
	return strings.Join(parts, " ")
}

func formatDisplayTime(value string) string {
	t := api.ParseQueueTime(strings.TrimSpace(value))
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04")
}

func formatFingerprint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	if len(value) > 12 {
		return value[:12]
	}
	return value
}
