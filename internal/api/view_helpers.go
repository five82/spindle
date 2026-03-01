package api

import (
	"encoding/json"
	"regexp"
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

// metadataFields holds all commonly extracted metadata fields from a single JSON parse.
type metadataFields struct {
	title    string
	year     string
	edition  string
	filename string
}

// parseMetadataFields extracts all common metadata fields with a single JSON parse.
func parseMetadataFields(metadataJSON string) metadataFields {
	if metadataJSON == "" {
		return metadataFields{title: "Unknown", year: "Unknown"}
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &raw); err != nil {
		return metadataFields{title: "Unknown", year: "Unknown"}
	}

	str := func(key, fallback string) string {
		if v, ok := raw[key].(string); ok && v != "" {
			return v
		}
		return fallback
	}

	year := "Unknown"
	if rd := str("release_date", ""); rd != "" {
		if match := yearPattern.FindString(rd); match != "" {
			year = match
		}
	}

	return metadataFields{
		title:    str("title", "Unknown"),
		year:     year,
		edition:  str("edition", ""),
		filename: str("filename", ""),
	}
}
