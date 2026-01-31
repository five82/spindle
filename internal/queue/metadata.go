package queue

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Metadata provides a minimal implementation of organizer.MetadataProvider.
type Metadata struct {
	TitleValue     string `json:"title"`
	LibraryPath    string `json:"library_path"`
	FilenameValue  string `json:"filename"`
	Movie          bool   `json:"movie"`
	MediaType      string `json:"media_type"`
	ShowTitle      string `json:"show_title"`
	SeasonNumber   int    `json:"season_number"`
	EpisodeNumbers []int  `json:"episode_numbers"`
}

// MetadataFromJSON builds metadata from stored JSON, falling back to basic inference.

func MetadataFromJSON(data, fallbackTitle string) Metadata {
	meta := Metadata{TitleValue: fallbackTitle, FilenameValue: fallbackTitle}
	_ = json.Unmarshal([]byte(data), &meta)
	if meta.SeasonNumber < 0 {
		meta.SeasonNumber = 0
	}
	meta.EpisodeNumbers = normalizeEpisodeNumbers(meta.EpisodeNumbers)
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

// NewTVMetadata builds a metadata record for episodic content.
func NewTVMetadata(showTitle string, season int, episodeNumbers []int, displayTitle string) Metadata {
	show := strings.TrimSpace(showTitle)
	if show == "" {
		show = strings.TrimSpace(displayTitle)
	}
	if show == "" {
		show = "Manual Import"
	}
	if season < 1 {
		season = 1
	}
	cleanEpisodes := normalizeEpisodeNumbers(episodeNumbers)
	filename := buildEpisodeFilename(show, season, cleanEpisodes)
	return Metadata{
		TitleValue:     strings.TrimSpace(displayTitle),
		FilenameValue:  filename,
		Movie:          false,
		MediaType:      "tv",
		ShowTitle:      show,
		SeasonNumber:   season,
		EpisodeNumbers: cleanEpisodes,
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
	show := strings.TrimSpace(m.ShowTitle)
	if show == "" {
		show = strings.TrimSpace(m.TitleValue)
	}
	if show == "" {
		show = "Manual Import"
	}
	season := m.SeasonNumber
	if season <= 0 {
		season = 1
	}
	showFolder := sanitizeFilename(show)
	seasonFolder := fmt.Sprintf("Season %02d", season)
	return filepath.Join(root, tvDir, showFolder, seasonFolder)
}

func (m Metadata) GetFilename() string {
	if !m.IsMovie() {
		show := strings.TrimSpace(m.ShowTitle)
		if show == "" && len(m.EpisodeNumbers) == 0 {
			goto fallback
		}
		if show == "" {
			show = strings.TrimSpace(m.TitleValue)
		}
		season := m.SeasonNumber
		if season <= 0 {
			season = 1
		}
		if label := buildEpisodeFilename(show, season, m.EpisodeNumbers); label != "" {
			return label
		}
	}
fallback:
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

func buildEpisodeFilename(show string, season int, episodes []int) string {
	show = sanitizeFilename(show)
	if show == "" {
		return ""
	}
	if len(episodes) == 0 {
		return fmt.Sprintf("%s - Season %02d", show, season)
	}
	first := episodes[0]
	last := episodes[len(episodes)-1]
	if len(episodes) == 1 || first == last {
		return fmt.Sprintf("%s - S%02dE%02d", show, season, first)
	}
	return fmt.Sprintf("%s - S%02dE%02d-E%02d", show, season, first, last)
}

func normalizeEpisodeNumbers(numbers []int) []int {
	filtered := make([]int, 0, len(numbers))
	for _, n := range numbers {
		if n <= 0 {
			continue
		}
		filtered = append(filtered, n)
	}
	if len(filtered) == 0 {
		return nil
	}
	sort.Ints(filtered)
	result := filtered[:1]
	for _, n := range filtered[1:] {
		if n != result[len(result)-1] {
			result = append(result, n)
		}
	}
	return result
}

func sanitizeFilename(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "manual-import"
	}
	// Handle ": " first to get proper spacing ("Mission: Impossible" -> "Mission - Impossible")
	value = strings.ReplaceAll(value, ": ", " - ")
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
