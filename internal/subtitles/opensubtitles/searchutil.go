package opensubtitles

import (
	"fmt"
	"strconv"
	"strings"
)

// EpisodeSearchVariants produces ordered OpenSubtitles search variants for a TV episode.
// It prefers parent TMDB identifiers, falls back to episode-level IDs, and finally
// constructs a textual SxxEyy query. Duplicate requests are removed while preserving order.
func EpisodeSearchVariants(base SearchRequest, showTitle string, seasonNumber, episodeNumber int, episodeTMDBID int64) []SearchRequest {
	variants := make([]SearchRequest, 0, 3)

	primary := base
	primary.TMDBID = 0
	variants = append(variants, primary)

	if episodeTMDBID > 0 {
		episodeVariant := base
		episodeVariant.ParentTMDBID = 0
		episodeVariant.TMDBID = episodeTMDBID
		variants = append(variants, episodeVariant)
	}

	queryVariant := base
	queryVariant.ParentTMDBID = 0
	queryVariant.TMDBID = 0
	title := strings.TrimSpace(showTitle)
	if title != "" {
		queryVariant.Query = fmt.Sprintf("%s S%02dE%02d", title, seasonNumber, episodeNumber)
	} else {
		queryVariant.Query = fmt.Sprintf("S%02dE%02d", seasonNumber, episodeNumber)
	}
	variants = append(variants, queryVariant)

	unique := make([]SearchRequest, 0, len(variants))
	seen := make(map[string]struct{}, len(variants))
	for _, variant := range variants {
		key := variantSignature(variant)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, variant)
	}
	return unique
}

func variantSignature(req SearchRequest) string {
	var builder strings.Builder
	builder.Grow(128)
	builder.WriteString("tmdb=")
	builder.WriteString(strconv.FormatInt(req.TMDBID, 10))
	builder.WriteString("|parent=")
	builder.WriteString(strconv.FormatInt(req.ParentTMDBID, 10))
	builder.WriteString("|season=")
	builder.WriteString(strconv.Itoa(req.Season))
	builder.WriteString("|episode=")
	builder.WriteString(strconv.Itoa(req.Episode))
	builder.WriteString("|query=")
	builder.WriteString(strings.TrimSpace(req.Query))
	builder.WriteString("|languages=")
	builder.WriteString(strings.Join(req.Languages, ","))
	return builder.String()
}
