package api

import (
	"encoding/json"
	"regexp"
	"strings"
)

// MetadataField extracts a string field from metadata JSON.
func MetadataField(metadataJSON, field, fallback string) string {
	if metadataJSON == "" {
		return fallback
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return fallback
	}
	value, ok := metadata[field].(string)
	if !ok || value == "" {
		return fallback
	}
	return value
}

var yearPattern = regexp.MustCompile(`^\d{4}`)

// MetadataYear extracts year from the release_date metadata field.
func MetadataYear(metadataJSON string) string {
	releaseDate := MetadataField(metadataJSON, "release_date", "")
	if releaseDate == "" {
		return "Unknown"
	}
	if match := yearPattern.FindString(releaseDate); match != "" {
		return match
	}
	return "Unknown"
}

// MetadataTitle extracts title from metadata JSON.
func MetadataTitle(metadataJSON string) string {
	return MetadataField(metadataJSON, "title", "Unknown")
}

// MetadataFilename extracts filename from metadata JSON.
func MetadataFilename(metadataJSON string) string {
	return MetadataField(metadataJSON, "filename", "")
}

// MetadataEdition extracts edition from metadata JSON.
func MetadataEdition(metadataJSON string) string {
	return MetadataField(metadataJSON, "edition", "")
}

// EpisodeTotalsFromStatuses derives totals from per-episode status payloads.
func EpisodeTotalsFromStatuses(episodes []EpisodeStatus) EpisodeTotals {
	var totals EpisodeTotals
	for _, ep := range episodes {
		totals.Planned++
		if strings.TrimSpace(ep.RippedPath) != "" {
			totals.Ripped++
		}
		if strings.TrimSpace(ep.EncodedPath) != "" {
			totals.Encoded++
		}
		if strings.TrimSpace(ep.FinalPath) != "" {
			totals.Final++
		}
	}
	return totals
}
