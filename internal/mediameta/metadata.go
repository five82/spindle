// Package mediameta centralizes user-facing media metadata projection and
// filesystem naming rules shared by queue persistence, organization, and tests.
package mediameta

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/five82/spindle/internal/textutil"
)

// Metadata holds denormalized media metadata for display and path computation.
type Metadata struct {
	ID           int       `json:"id,omitempty"`
	Title        string    `json:"title,omitempty"`
	MediaType    string    `json:"media_type,omitempty"`
	ShowTitle    string    `json:"show_title,omitempty"`
	Year         string    `json:"year,omitempty"`
	SeasonNumber int       `json:"season_number,omitempty"`
	Movie        bool      `json:"movie,omitempty"`
	Episodes     []Episode `json:"episodes,omitempty"`
	DisplayTitle string    `json:"display_title,omitempty"`
}

// Episode represents a single TV episode in projected metadata.
type Episode struct {
	Season     int    `json:"season"`
	Episode    int    `json:"episode"`
	EpisodeEnd int    `json:"episode_end,omitempty"`
	Title      string `json:"title,omitempty"`
}

// FromJSON deserializes metadata JSON. On error or empty data, it returns basic
// metadata with the fallback title.
func FromJSON(data string, fallbackTitle string) Metadata {
	if data == "" {
		return NewBasic(fallbackTitle, false)
	}
	var m Metadata
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return NewBasic(fallbackTitle, false)
	}
	if m.Title == "" {
		m.Title = fallbackTitle
	}
	return m
}

// NewBasic constructs minimal metadata with a title and movie flag.
func NewBasic(title string, isMovie bool) Metadata {
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

// IsMovie returns true if the metadata indicates movie content.
func (m *Metadata) IsMovie() bool {
	switch strings.ToLower(m.MediaType) {
	case "movie", "film":
		return true
	case "tv", "tv_show", "television", "series":
		return false
	}
	return m.Movie
}

// LibraryPath computes the target library folder via SafeJoin.
// Movies: {root}/{moviesDir}/{baseFilename}
// TV: {root}/{tvDir}/{show}/Season {NN}
func (m *Metadata) LibraryPath(root, moviesDir, tvDir string) (string, error) {
	if m.IsMovie() {
		base := m.BaseFilename()
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

// Filename returns the final output filename without extension.
func (m *Metadata) Filename() string {
	if m.IsMovie() {
		return m.BaseFilename()
	}
	return buildEpisodeFilename(m)
}

// BaseFilename returns the movie/base filename: "Title (Year)".
func (m *Metadata) BaseFilename() string {
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

// DestFilename builds the destination filename, including ext, for an asset key.
func DestFilename(meta *Metadata, key, ext string) string {
	if meta == nil {
		return textutil.SanitizeDisplayName(key) + ext
	}
	if meta.IsMovie() {
		return meta.Filename() + ext
	}

	season, episode, episodeEnd := ParseEpisodeKey(key)
	if season > 0 && episode > 0 {
		epMeta := Metadata{
			Title:        meta.Title,
			ShowTitle:    meta.ShowTitle,
			MediaType:    "tv",
			SeasonNumber: meta.SeasonNumber,
			Episodes:     []Episode{{Season: season, Episode: episode, EpisodeEnd: episodeEnd}},
			DisplayTitle: meta.DisplayTitle,
		}
		return textutil.SanitizeDisplayName(epMeta.Filename()) + ext
	}

	show := textutil.SanitizeDisplayName(meta.ShowTitle)
	if show == "" || show == "manual-import" {
		show = textutil.SanitizeDisplayName(meta.Title)
	}
	return textutil.SanitizeDisplayName(show+" - "+key) + ext
}

// ParseEpisodeKey extracts season and episode numbers from keys like "s01e03"
// or "s01e01-e02". Returns zeros if the key does not match the expected format.
func ParseEpisodeKey(key string) (season, episode, episodeEnd int) {
	lower := strings.ToLower(key)
	if _, err := fmt.Sscanf(lower, "s%02de%02d-e%02d", &season, &episode, &episodeEnd); err == nil {
		return season, episode, episodeEnd
	}
	if _, err := fmt.Sscanf(lower, "s%02de%02d", &season, &episode); err == nil {
		return season, episode, 0
	}
	return 0, 0, 0
}

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
		if ep.EpisodeEnd > ep.Episode {
			return fmt.Sprintf("%s - S%02dE%02d-E%02d", show, ep.Season, ep.Episode, ep.EpisodeEnd)
		}
		return fmt.Sprintf("%s - S%02dE%02d", show, ep.Season, ep.Episode)
	}

	first := m.Episodes[0]
	last := m.Episodes[len(m.Episodes)-1]
	lastEpisode := last.Episode
	if last.EpisodeEnd > lastEpisode {
		lastEpisode = last.EpisodeEnd
	}
	return fmt.Sprintf("%s - S%02dE%02d-E%02d", show, first.Season, first.Episode, lastEpisode)
}
