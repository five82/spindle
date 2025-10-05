package queue

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// Metadata provides a minimal implementation of organizer.MetadataProvider.
type Metadata struct {
	TitleValue    string `json:"title"`
	LibraryPath   string `json:"library_path"`
	FilenameValue string `json:"filename"`
	Movie         bool   `json:"movie"`
	MediaType     string `json:"media_type"`
}

// MetadataFromJSON builds metadata from stored JSON, falling back to basic inference.
func MetadataFromJSON(data, fallbackTitle string) Metadata {
	meta := Metadata{TitleValue: fallbackTitle, FilenameValue: fallbackTitle}
	_ = json.Unmarshal([]byte(data), &meta)
	return meta
}

// NewBasicMetadata constructs a metadata record using the provided title and
// content type flag. Filenames are sanitized for filesystem safety.
func NewBasicMetadata(title string, movie bool) Metadata {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Manual Import"
	}
	mediaType := "tv"
	if movie {
		mediaType = "movie"
	}
	return Metadata{
		TitleValue:    title,
		FilenameValue: sanitizeFilename(title),
		Movie:         movie,
		MediaType:     mediaType,
	}
}

func (m Metadata) GetLibraryPath(root, moviesDir, tvDir string) string {
	if m.LibraryPath != "" {
		return m.LibraryPath
	}
	if m.IsMovie() {
		folder := m.GetFilename()
		if folder == "" {
			folder = "Manual Import"
		}
		return filepath.Join(root, moviesDir, folder)
	}
	return filepath.Join(root, tvDir)
}

func (m Metadata) GetFilename() string {
	value := m.FilenameValue
	if strings.TrimSpace(value) == "" {
		value = m.TitleValue
	}
	return sanitizeFilename(value)
}

func (m Metadata) IsMovie() bool {
	mediaType := strings.ToLower(strings.TrimSpace(m.MediaType))
	switch mediaType {
	case "movie", "film":
		return true
	case "tv", "tv_show", "television", "series":
		return false
	}
	return m.Movie
}

func (m Metadata) Title() string { return m.TitleValue }

func sanitizeFilename(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "manual-import"
	}
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		"*", "-",
		"?", "",
		"\"", "",
		"<", "",
		">", "",
		"|", "",
		"\n", " ",
		"\t", " ",
	)
	cleaned := replacer.Replace(value)
	fields := strings.Fields(cleaned)
	if len(fields) == 0 {
		return "manual-import"
	}
	return strings.Join(fields, " ")
}
