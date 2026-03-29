package queue

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/five82/spindle/internal/textutil"
)

// Metadata holds denormalized TMDB metadata for display and path computation.
type Metadata struct {
	ID           int               `json:"id,omitempty"`
	Title        string            `json:"title,omitempty"`
	MediaType    string            `json:"media_type,omitempty"`
	ShowTitle    string            `json:"show_title,omitempty"`
	Year         string            `json:"year,omitempty"`
	SeasonNumber int               `json:"season_number,omitempty"`
	Movie        bool              `json:"movie,omitempty"`
	Episodes     []MetadataEpisode `json:"episodes,omitempty"`
	DisplayTitle string            `json:"display_title,omitempty"`
}

// MetadataEpisode represents a single episode in TV metadata.
type MetadataEpisode struct {
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	Title   string `json:"title,omitempty"`
}

// MetadataFromJSON deserializes metadata from the metadata_json column.
// On error or empty data, returns basic metadata with the fallback title.
func MetadataFromJSON(data string, fallbackTitle string) Metadata {
	if data == "" {
		return NewBasicMetadata(fallbackTitle, false)
	}
	var m Metadata
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return NewBasicMetadata(fallbackTitle, false)
	}
	if m.Title == "" {
		m.Title = fallbackTitle
	}
	return m
}

// NewBasicMetadata constructs minimal metadata with a title and movie flag.
func NewBasicMetadata(title string, isMovie bool) Metadata {
	mt := "tv"
	if isMovie {
		mt = "movie"
	}
	return Metadata{
		Title:     title,
		MediaType: mt,
		Movie:     isMovie,
	}
}

// NewTVMetadata builds TV-specific metadata with show, season, episodes, and
// display title.
func NewTVMetadata(show string, season int, episodes []MetadataEpisode, display string) Metadata {
	return Metadata{
		Title:        show,
		ShowTitle:    show,
		MediaType:    "tv",
		SeasonNumber: season,
		Episodes:     episodes,
		DisplayTitle: display,
	}
}

// IsMovie returns true if the metadata indicates movie content.
// Checks media_type first ("movie"/"film" = true, "tv"/"tv_show"/"television"/"series" = false),
// then falls back to the Movie bool field.
func (m *Metadata) IsMovie() bool {
	switch strings.ToLower(m.MediaType) {
	case "movie", "film":
		return true
	case "tv", "tv_show", "television", "series":
		return false
	}
	return m.Movie
}

// GetLibraryPath computes the target library folder via SafeJoin.
// Movies: {root}/{moviesDir}/{baseFilename}
// TV: {root}/{tvDir}/{show}/Season {NN}
func (m *Metadata) GetLibraryPath(root, moviesDir, tvDir string) (string, error) {
	if m.IsMovie() {
		base := m.GetBaseFilename()
		dir, err := textutil.SafeJoin(root, moviesDir)
		if err != nil {
			return "", err
		}
		return textutil.SafeJoin(dir, base)
	}

	show := textutil.SanitizeDisplayName(m.ShowTitle)
	if show == "" || show == "manual-import" {
		show = textutil.SanitizeDisplayName(m.Title)
	}

	dir, err := textutil.SafeJoin(root, tvDir)
	if err != nil {
		return "", err
	}
	dir, err = textutil.SafeJoin(dir, show)
	if err != nil {
		return "", err
	}
	season := fmt.Sprintf("Season %02d", m.SeasonNumber)
	return textutil.SafeJoin(dir, season)
}

// GetFilename returns the final output filename.
// Movies: base filename. TV: episode format via buildEpisodeFilename.
func (m *Metadata) GetFilename() string {
	if m.IsMovie() {
		return m.GetBaseFilename()
	}
	return buildEpisodeFilename(m)
}

// GetBaseFilename returns the filename without edition suffix.
func (m *Metadata) GetBaseFilename() string {
	title := m.Title
	if m.DisplayTitle != "" {
		title = m.DisplayTitle
	}
	if title == "" {
		title = "Manual Import"
	}
	name := textutil.SanitizeDisplayName(title)
	if m.Year != "" {
		name += " (" + m.Year + ")"
	}
	return name
}

// buildEpisodeFilename constructs TV episode filenames.
// No episodes: "{Show} - Season {NN}"
// Single episode: "{Show} - S{NN}E{NN}"
// Multi-episode range: "{Show} - S{NN}E{NN}-E{NN}"
func buildEpisodeFilename(m *Metadata) string {
	show := textutil.SanitizeDisplayName(m.ShowTitle)
	if show == "" || show == "manual-import" {
		show = textutil.SanitizeDisplayName(m.Title)
	}
	if show == "" || show == "manual-import" {
		show = "Manual Import"
	}

	season := m.SeasonNumber

	if len(m.Episodes) == 0 {
		return fmt.Sprintf("%s - Season %02d", show, season)
	}

	if len(m.Episodes) == 1 {
		ep := m.Episodes[0]
		return fmt.Sprintf("%s - S%02dE%02d", show, ep.Season, ep.Episode)
	}

	// Multi-episode range: use first and last episode numbers.
	first := m.Episodes[0]
	last := m.Episodes[len(m.Episodes)-1]
	return fmt.Sprintf("%s - S%02dE%02d-E%02d", show, first.Season, first.Episode, last.Episode)
}
