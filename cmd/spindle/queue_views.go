package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
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

func buildQueueListRows(items []queueItemView) [][]string {
	if len(items) == 0 {
		return nil
	}
	sorted := make([]queueItemView, len(items))
	copy(sorted, items)

	sort.Slice(sorted, func(i, j int) bool {
		ti := parseQueueTime(sorted[i].CreatedAt)
		tj := parseQueueTime(sorted[j].CreatedAt)
		if ti.Equal(tj) {
			return sorted[i].ID > sorted[j].ID
		}
		return ti.After(tj)
	})

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
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC().Format("2006-01-02 15:04")
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.UTC().Format("2006-01-02 15:04")
	}
	return value
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
