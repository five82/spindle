package subtitles

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"spindle/internal/logging"
	"spindle/internal/subtitles/opensubtitles"
)

func mediaTypeForContext(ctx SubtitleContext) string {
	if ctx.IsMovie() {
		return "movie"
	}
	return "episode"
}

func movieVariantSignature(req opensubtitles.SearchRequest) string {
	return fmt.Sprintf("tmdb:%d|parent:%d|imdb:%s|q:%s|y:%s|type:%s", req.TMDBID, req.ParentTMDBID, strings.TrimSpace(req.IMDBID), strings.TrimSpace(req.Query), strings.TrimSpace(req.Year), strings.TrimSpace(req.MediaType))
}

func (s *Service) searchMovieWithVariants(ctx context.Context, base opensubtitles.SearchRequest) (opensubtitles.SearchResponse, error) {
	variants := []opensubtitles.SearchRequest{base}

	// TMDB-only (drop title/year filters in case of metadata mismatch)
	if base.TMDBID > 0 {
		variant := base
		variant.Query = ""
		variant.Year = ""
		variant.ParentTMDBID = 0
		variants = append(variants, variant)
	}

	// Title-only (drop TMDB in case the ID is wrong) with and without year
	if strings.TrimSpace(base.Query) != "" {
		variant := base
		variant.TMDBID = 0
		variant.ParentTMDBID = 0
		variants = append(variants, variant)

		variantNoYear := variant
		variantNoYear.Year = ""
		variants = append(variants, variantNoYear)
	}

	// IMDB-only fallback
	if imdb := opensubtitles.SanitizeIMDBID(base.IMDBID); imdb != "" {
		variant := base
		variant.TMDBID = 0
		variant.ParentTMDBID = 0
		variant.Query = ""
		variant.Year = ""
		variant.MediaType = "movie"
		variants = append(variants, variant)
	}

	unique := make([]opensubtitles.SearchRequest, 0, len(variants))
	seen := make(map[string]struct{})
	for _, v := range variants {
		sig := movieVariantSignature(v)
		if _, ok := seen[sig]; ok {
			continue
		}
		seen[sig] = struct{}{}
		unique = append(unique, v)
	}

	if s.logger != nil && len(unique) > 0 {
		s.logger.Info("opensubtitles search strategy",
			logging.String(logging.FieldDecisionType, "opensubtitles_search_strategy"),
			logging.String("decision_result", "movie_variants"),
			logging.String("decision_reason", "searching_with_fallback_variants"),
			logging.Int("variant_count", len(unique)),
		)
	}
	for idx, variant := range unique {
		if s.logger != nil {
			s.logger.Debug("opensubtitles search variant",
				logging.Int("attempt", idx+1),
				logging.String("query", variant.Query),
				logging.String("year", variant.Year),
				logging.Int64("tmdb_id", variant.TMDBID),
				logging.String("imdb_id", opensubtitles.SanitizeIMDBID(variant.IMDBID)),
				logging.String("media_type", variant.MediaType),
			)
		}
		resp, err := s.invokeOpenSubtitlesSearch(ctx, variant)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("opensubtitles search variant failed; trying fallback query",
					logging.Error(err),
					logging.String(logging.FieldEventType, "opensubtitles_search_failed"),
					logging.String(logging.FieldErrorHint, "check OpenSubtitles credentials and connectivity"),
				)
			}
			if idx == len(unique)-1 {
				return opensubtitles.SearchResponse{}, err
			}
			continue
		}
		if len(resp.Subtitles) > 0 {
			return resp, nil
		}
	}

	return opensubtitles.SearchResponse{}, nil
}

func (s *Service) searchEpisodeWithVariants(ctx context.Context, base opensubtitles.SearchRequest, showTitle string, season, episode int, episodeTMDBID int64) (opensubtitles.SearchResponse, error) {
	variants := opensubtitles.EpisodeSearchVariants(base, showTitle, season, episode, episodeTMDBID)
	var resp opensubtitles.SearchResponse
	for attempt, variant := range variants {
		if s.logger != nil && attempt == 0 {
			s.logger.Debug("opensubtitles search variant",
				logging.Int("season", season),
				logging.Int("episode", episode),
				logging.String("query", strings.TrimSpace(variant.Query)),
				logging.Int64("tmdb_id", variant.TMDBID),
				logging.Int64("parent_tmdb_id", variant.ParentTMDBID),
			)
		}
		var err error
		resp, err = s.invokeOpenSubtitlesSearch(ctx, variant)
		if err != nil {
			return opensubtitles.SearchResponse{}, fmt.Errorf("opensubtitles search s%02de%02d attempt %d: %w", season, episode, attempt+1, err)
		}
		if len(resp.Subtitles) == 0 {
			if s.logger != nil {
				s.logger.Warn("opensubtitles returned no candidates",
					logging.Int("season", season),
					logging.Int("episode", episode),
					logging.Int("attempt", attempt+1),
					logging.String(logging.FieldEventType, "opensubtitles_no_candidates"),
					logging.String(logging.FieldErrorHint, "verify episode metadata and OpenSubtitles language filters"),
				)
			}
			continue
		}
		if attempt > 0 && s.logger != nil {
			s.logger.Debug("opensubtitles fallback search succeeded",
				logging.Int("season", season),
				logging.Int("episode", episode),
				logging.Int("attempt", attempt+1),
			)
		}
		return resp, nil
	}
	return opensubtitles.SearchResponse{}, nil
}

func (s *Service) invokeOpenSubtitlesSearch(ctx context.Context, req opensubtitles.SearchRequest) (opensubtitles.SearchResponse, error) {
	if err := s.ensureOpenSubtitlesReady(); err != nil {
		return opensubtitles.SearchResponse{}, err
	}
	if s.openSubs == nil {
		return opensubtitles.SearchResponse{}, errors.New("opensubtitles client unavailable")
	}
	var resp opensubtitles.SearchResponse
	if err := s.invokeOpenSubtitles(ctx, func() error {
		var err error
		resp, err = s.openSubs.Search(ctx, req)
		return err
	}); err != nil {
		return opensubtitles.SearchResponse{}, err
	}
	return resp, nil
}
