package subtitles

import (
	"encoding/json"
	"strconv"
	"strings"

	"spindle/internal/identification"
	"spindle/internal/queue"
)

// SubtitleContext captures metadata required to discover subtitles from external services.
type SubtitleContext struct {
	TMDBID        int64
	ParentTMDBID  int64
	EpisodeTMDBID int64
	IMDBID        string
	MediaType     string
	Title         string
	ShowTitle     string
	Year          string
	ContentKey    string
	Language      string
	Season        int
	Episode       int
	Edition       string // Edition label (e.g., "Director's Cut", "Extended Edition")
}

// HasTMDBID reports whether a TMDB identifier is available.
func (c SubtitleContext) HasTMDBID() bool {
	return c.TMDBID > 0
}

// ParentID returns the series/movie TMDB identifier.
func (c SubtitleContext) ParentID() int64 {
	if c.ParentTMDBID > 0 {
		return c.ParentTMDBID
	}
	return c.TMDBID
}

// EpisodeID returns the TMDB identifier of a specific episode when known.
func (c SubtitleContext) EpisodeID() int64 {
	return c.EpisodeTMDBID
}

// IsMovie indicates whether the content represents a movie title.
func (c SubtitleContext) IsMovie() bool {
	mediaType := strings.ToLower(strings.TrimSpace(c.MediaType))
	return mediaType == "movie" || mediaType == "film"
}

// SeriesTitle returns the best-known title for the TV series when available.
func (c SubtitleContext) SeriesTitle() string {
	if title := strings.TrimSpace(c.ShowTitle); title != "" {
		return title
	}
	return deriveShowTitle(c.Title)
}

// BuildSubtitleContext extracts high-signal metadata about the queue item to aid subtitle lookups.
func BuildSubtitleContext(item *queue.Item) SubtitleContext {
	var ctx SubtitleContext
	if item == nil {
		return ctx
	}

	meta := queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)
	ctx.Title = strings.TrimSpace(meta.Title())
	if ctx.Title == "" {
		ctx.Title = strings.TrimSpace(item.DiscTitle)
	}
	if ctx.Title == "" {
		ctx.Title = strings.TrimSpace(item.SourcePath)
	}
	ctx.ShowTitle = strings.TrimSpace(meta.ShowTitle)

	if meta.IsMovie() {
		ctx.MediaType = "movie"
	} else if strings.TrimSpace(meta.MediaType) != "" {
		ctx.MediaType = strings.ToLower(strings.TrimSpace(meta.MediaType))
	} else {
		ctx.MediaType = "tv"
	}
	if meta.SeasonNumber > 0 {
		ctx.Season = meta.SeasonNumber
	}
	if len(meta.EpisodeNumbers) > 0 {
		ctx.Episode = meta.EpisodeNumbers[0]
	}

	parseMetadataJSON(item.MetadataJSON, &ctx)
	parseRipSpecData(item.RipSpecData, &ctx)

	if !ctx.IsMovie() {
		if ctx.ParentTMDBID == 0 {
			ctx.ParentTMDBID = parseTMDBFromContentKey(ctx.ContentKey)
		}
		if ctx.ParentTMDBID == 0 {
			ctx.ParentTMDBID = ctx.TMDBID
		}
		if ctx.ParentTMDBID > 0 {
			ctx.TMDBID = ctx.ParentTMDBID
		}
	} else {
		if ctx.ParentTMDBID == 0 {
			ctx.ParentTMDBID = ctx.TMDBID
		} else if ctx.TMDBID == 0 {
			ctx.TMDBID = ctx.ParentTMDBID
		}
	}

	if ctx.Year == "" {
		ctx.Year = extractYearFromTitle(ctx.Title)
	}
	if ctx.ShowTitle == "" && !ctx.IsMovie() {
		ctx.ShowTitle = deriveShowTitle(ctx.Title)
	}
	if ctx.Season <= 0 && !ctx.IsMovie() {
		ctx.Season = 1
	}

	// Extract edition from disc title or source path (for alternate cuts)
	if ctx.Edition == "" {
		if edition, found := identification.ExtractKnownEdition(item.DiscTitle); found {
			ctx.Edition = edition
		} else if edition, found := identification.ExtractKnownEdition(item.SourcePath); found {
			ctx.Edition = edition
		}
	}

	return ctx
}

func parseMetadataJSON(raw string, ctx *SubtitleContext) {
	if strings.TrimSpace(raw) == "" || ctx == nil {
		return
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return
	}
	assignTMDBIdentifiers(payload, ctx)
	if ctx.IMDBID == "" {
		if value, ok := payload["imdb_id"].(string); ok {
			ctx.IMDBID = strings.TrimSpace(value)
		}
	}
	if ctx.MediaType == "" {
		if value, ok := payload["media_type"].(string); ok {
			ctx.MediaType = strings.ToLower(strings.TrimSpace(value))
		}
	}
	if ctx.ShowTitle == "" {
		if value, ok := payload["show_title"].(string); ok {
			ctx.ShowTitle = strings.TrimSpace(value)
		} else if value, ok := payload["series_title"].(string); ok {
			ctx.ShowTitle = strings.TrimSpace(value)
		}
	}
	if ctx.Language == "" {
		if value, ok := payload["language"].(string); ok {
			ctx.Language = strings.ToLower(strings.TrimSpace(value))
		} else if value, ok := payload["original_language"].(string); ok {
			ctx.Language = strings.ToLower(strings.TrimSpace(value))
		}
	}
	if ctx.Year == "" {
		if value, ok := payload["release_date"].(string); ok {
			ctx.Year = extractYear(value)
		} else if value, ok := payload["year"].(string); ok {
			ctx.Year = extractYear(value)
		}
	}
	if ctx.Season <= 0 {
		if value := asInt(payload["season_number"]); value > 0 {
			ctx.Season = int(value)
		}
	}
	if ctx.Episode <= 0 {
		if episodes, ok := payload["episode_numbers"].([]any); ok && len(episodes) > 0 {
			if value := asInt(episodes[0]); value > 0 {
				ctx.Episode = int(value)
			}
		} else if value := asInt(payload["episode_number"]); value > 0 {
			ctx.Episode = int(value)
		}
	}
}

func parseRipSpecData(raw string, ctx *SubtitleContext) {
	if strings.TrimSpace(raw) == "" || ctx == nil {
		return
	}
	var spec struct {
		ContentKey string           `json:"content_key"`
		Metadata   map[string]any   `json:"metadata"`
		Titles     []map[string]any `json:"titles"`
		Extras     map[string]any   `json:"extras"`
	}
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return
	}
	if ctx.ContentKey == "" {
		ctx.ContentKey = strings.TrimSpace(spec.ContentKey)
	}
	assignTMDBIdentifiers(spec.Metadata, ctx)
	if ctx.TMDBID == 0 {
		ctx.TMDBID = parseTMDBFromContentKey(ctx.ContentKey)
	}
	if ctx.MediaType == "" {
		ctx.MediaType = parseMediaTypeFromContentKey(ctx.ContentKey)
	}
	if ctx.Year == "" && len(spec.Metadata) > 0 {
		if release, ok := spec.Metadata["release_date"].(string); ok {
			ctx.Year = extractYear(release)
		} else if year, ok := spec.Metadata["year"].(string); ok {
			ctx.Year = extractYear(year)
		}
	}
	if ctx.IMDBID == "" && len(spec.Metadata) > 0 {
		if imdb, ok := spec.Metadata["imdb_id"].(string); ok {
			ctx.IMDBID = strings.TrimSpace(imdb)
		}
	}
	if ctx.ShowTitle == "" && len(spec.Metadata) > 0 {
		if show, ok := spec.Metadata["show_title"].(string); ok {
			ctx.ShowTitle = strings.TrimSpace(show)
		} else if show, ok := spec.Metadata["series_title"].(string); ok {
			ctx.ShowTitle = strings.TrimSpace(show)
		}
	}
	if ctx.Language == "" && len(spec.Metadata) > 0 {
		if lang, ok := spec.Metadata["language"].(string); ok {
			ctx.Language = strings.ToLower(strings.TrimSpace(lang))
		}
	}
}

func parseTMDBFromContentKey(value string) int64 {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) < 3 {
		return 0
	}
	id, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func parseMediaTypeFromContentKey(value string) string {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) < 2 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parts[1]))
}

func assignTMDBIdentifiers(payload map[string]any, ctx *SubtitleContext) {
	if ctx == nil || len(payload) == 0 {
		return
	}
	if ctx.MediaType == "" {
		if value, ok := payload["media_type"].(string); ok {
			ctx.MediaType = strings.ToLower(strings.TrimSpace(value))
		}
	}
	if id := asInt(payload["parent_tmdb_id"]); id > 0 && ctx.ParentTMDBID == 0 {
		ctx.ParentTMDBID = id
	}
	if id := asInt(payload["series_tmdb_id"]); id > 0 && ctx.ParentTMDBID == 0 {
		ctx.ParentTMDBID = id
	}
	if id := asInt(payload["show_tmdb_id"]); id > 0 && ctx.ParentTMDBID == 0 {
		ctx.ParentTMDBID = id
	}
	if id := asInt(payload["episode_tmdb_id"]); id > 0 && ctx.EpisodeTMDBID == 0 {
		ctx.EpisodeTMDBID = id
	}
	if id := asInt(payload["tmdb_episode_id"]); id > 0 && ctx.EpisodeTMDBID == 0 {
		ctx.EpisodeTMDBID = id
	}
	if id := asInt(payload["id"]); id > 0 {
		mediaType := strings.ToLower(strings.TrimSpace(ctx.MediaType))
		switch mediaType {
		case "movie", "film":
			if ctx.TMDBID == 0 {
				ctx.TMDBID = id
			}
		case "episode":
			if ctx.EpisodeTMDBID == 0 {
				ctx.EpisodeTMDBID = id
			}
		default:
			if ctx.ParentTMDBID == 0 {
				ctx.ParentTMDBID = id
			}
			if ctx.TMDBID == 0 {
				ctx.TMDBID = id
			}
		}
	}
	if ctx.ParentTMDBID == 0 && ctx.TMDBID > 0 && !ctx.IsMovie() {
		ctx.ParentTMDBID = ctx.TMDBID
	}
}

func asInt(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return i
		}
	}
	return 0
}

func extractYear(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 4 {
		if _, err := strconv.Atoi(value[:4]); err == nil {
			return value[:4]
		}
	}
	return ""
}

// SplitTitleAndYear extracts a trailing parenthesized 4-digit year from a title
// string. For example, "The Matrix (1999)" returns ("The Matrix", "1999").
// If no year is found, the full title is returned with an empty year string.
func SplitTitleAndYear(title string) (string, string) {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return "", ""
	}
	idx := strings.LastIndex(trimmed, "(")
	if idx == -1 || !strings.HasSuffix(trimmed, ")") {
		return trimmed, ""
	}
	candidate := strings.TrimSpace(trimmed[idx+1 : len(trimmed)-1])
	if len(candidate) != 4 {
		return trimmed, ""
	}
	if _, err := strconv.Atoi(candidate); err != nil {
		return trimmed, ""
	}
	return strings.TrimSpace(trimmed[:idx]), candidate
}

func extractYearFromTitle(title string) string {
	_, year := SplitTitleAndYear(title)
	return year
}

func deriveShowTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	splitters := []string{" – ", " — ", " - ", ": "}
	for _, sep := range splitters {
		if idx := strings.Index(value, sep); idx > 0 {
			candidate := strings.TrimSpace(value[:idx])
			if candidate != "" {
				return candidate
			}
		}
	}
	return value
}
