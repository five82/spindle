package subtitles

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"spindle/internal/logging"
	"spindle/internal/services"
	"spindle/internal/subtitles/opensubtitles"
)

func (s *Service) shouldUseOpenSubtitles() bool {
	if s == nil || s.config == nil {
		return false
	}
	if !s.config.Subtitles.OpenSubtitlesEnabled {
		return false
	}
	if strings.TrimSpace(s.config.Subtitles.OpenSubtitlesAPIKey) == "" {
		return false
	}
	return true
}

func (s *Service) ensureOpenSubtitlesReady() error {
	if s == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "opensubtitles init", "Subtitle service unavailable", nil)
	}
	s.openSubsOnce.Do(func() {
		if s.openSubs != nil {
			return
		}
		if s.config == nil {
			s.openSubsErr = services.Wrap(services.ErrConfiguration, "subtitles", "opensubtitles init", "Configuration unavailable", nil)
			return
		}
		client, err := opensubtitles.New(opensubtitles.Config{
			APIKey:    s.config.Subtitles.OpenSubtitlesAPIKey,
			UserAgent: s.config.Subtitles.OpenSubtitlesUserAgent,
			UserToken: s.config.Subtitles.OpenSubtitlesUserToken,
		})
		if err != nil {
			s.openSubsErr = err
			return
		}
		s.openSubs = client
		if s.openSubsCache == nil {
			dir := strings.TrimSpace(s.config.Paths.OpenSubtitlesCacheDir)
			if dir != "" {
				cache, err := opensubtitles.NewCache(dir, s.logger)
				if err != nil {
					if s.logger != nil {
						s.logger.Warn("opensubtitles cache unavailable; caching disabled",
							logging.Error(err),
							logging.String(logging.FieldEventType, "opensubtitles_cache_unavailable"),
							logging.String(logging.FieldErrorHint, "check opensubtitles_cache_dir permissions"),
						)
					}
				} else {
					s.openSubsCache = cache
				}
			}
		}
		if s.logger != nil && !s.openSubsReadyLogged {
			userAgent := strings.TrimSpace(s.config.Subtitles.OpenSubtitlesUserAgent)
			tokenPresent := strings.TrimSpace(s.config.Subtitles.OpenSubtitlesUserToken) != ""
			s.logger.Debug("opensubtitles authentication ready",
				logging.String("user_agent", userAgent),
				logging.Bool("user_token_present", tokenPresent),
			)
			s.openSubsReadyLogged = true
		}
	})
	return s.openSubsErr
}

// tryForcedSubtitles searches OpenSubtitles for foreign-parts-only (forced) subtitles.
// referenceSubtitle is the path to an aligned regular subtitle file used as the alignment
// reference instead of audio. Subtitle-to-subtitle alignment is more reliable for sparse
// forced subtitles.
// Returns the output path if successful, empty string if no match found or an error occurred.
func (s *Service) tryForcedSubtitles(ctx context.Context, plan *generationPlan, req GenerateRequest, baseName, referenceSubtitle string) (string, error) {
	if plan == nil {
		return "", nil
	}
	if err := s.ensureOpenSubtitlesReady(); err != nil {
		return "", err
	}
	if s.openSubs == nil {
		return "", errors.New("opensubtitles client unavailable")
	}

	searchReq := s.buildForcedSubtitleSearchRequest(req)
	queryTitle := searchReq.Query

	if s.logger != nil {
		s.logger.Debug("searching opensubtitles for forced subtitles",
			logging.String("title", queryTitle),
			logging.Int64("tmdb_id", req.Context.TMDBID),
			logging.String("languages", strings.Join(req.Languages, ",")),
		)
	}

	// Use the same variant search strategy as regular subtitles
	var (
		resp      opensubtitles.SearchResponse
		searchErr error
	)
	if req.Context.IsMovie() || req.Context.Season <= 0 || req.Context.Episode <= 0 {
		resp, searchErr = s.searchMovieWithVariants(ctx, searchReq)
	} else {
		base := searchReq
		base.TMDBID = 0
		showTitle := req.Context.SeriesTitle()
		resp, searchErr = s.searchEpisodeWithVariants(ctx, base, showTitle, req.Context.Season, req.Context.Episode, req.Context.EpisodeID())
	}
	if searchErr != nil {
		if s.logger != nil {
			s.logger.Warn("forced subtitle search failed",
				logging.Error(searchErr),
				logging.String(logging.FieldEventType, "forced_subtitle_search_failed"),
				logging.String(logging.FieldErrorHint, "check OpenSubtitles connectivity"),
			)
		}
		return "", nil
	}

	if len(resp.Subtitles) == 0 {
		if s.logger != nil {
			s.logger.Debug("no forced subtitles found on opensubtitles",
				logging.String("title", queryTitle),
				logging.Int64("tmdb_id", req.Context.TMDBID),
			)
		}
		return "", nil
	}

	scored := rankSubtitleCandidates(resp.Subtitles, req.Languages, req.Context)
	if len(scored) == 0 {
		if s.logger != nil {
			s.logger.Debug("no forced subtitle candidate matched language criteria",
				logging.Int("results", len(resp.Subtitles)),
				logging.String("languages", strings.Join(req.Languages, ",")),
			)
		}
		return "", nil
	}

	// Forced subtitles require stricter title matching than regular subtitles.
	// Partial word overlap (e.g., "Star Trek: Generations" vs "Star Trek III") is
	// not acceptable - we need the title to contain or exactly match the expected title.
	scored = filterForcedSubtitleCandidates(scored, req.Context.Title, s.logger)
	if len(scored) == 0 {
		if s.logger != nil {
			s.logger.Debug("no forced subtitle candidate passed strict title matching",
				logging.Int("results", len(resp.Subtitles)),
				logging.String("title", req.Context.Title),
			)
		}
		return "", nil
	}

	forcedPlan := *plan
	forcedPlan.outputFile = fmt.Sprintf("%s.forced.srt", baseName)
	forcedPlan.referenceSubtitlePath = referenceSubtitle

	candidatesToTry := scored
	if len(candidatesToTry) > maxOpenSubtitlesCandidates {
		candidatesToTry = candidatesToTry[:maxOpenSubtitlesCandidates]
	}

	for _, candidate := range candidatesToTry {
		result, err := s.downloadAndAlignForcedCandidate(ctx, &forcedPlan, req, candidate.subtitle)
		if err != nil {
			if errors.Is(err, services.ErrExternalTool) {
				if s.logger != nil {
					s.logger.Warn("forced subtitle alignment tool failure, aborting candidates",
						logging.Error(err),
						logging.String(logging.FieldEventType, "forced_subtitle_tool_failure"),
					)
				}
				break
			}
			if s.logger != nil {
				s.logger.Debug("forced subtitle candidate rejected",
					logging.String("release", candidate.subtitle.Release),
					logging.Error(err),
				)
			}
			continue
		}

		if s.logger != nil {
			s.logger.Info("forced subtitle downloaded",
				logging.String(logging.FieldEventType, "forced_subtitle_downloaded"),
				logging.String("subtitle_file", result.SubtitlePath),
				logging.String("release", candidate.subtitle.Release),
				logging.Int("segments", result.SegmentCount),
			)
		}
		return result.SubtitlePath, nil
	}

	if s.logger != nil {
		s.logger.Warn("all forced subtitle candidates rejected",
			logging.Int("candidates_tried", len(candidatesToTry)),
			logging.String("title", queryTitle),
			logging.String(logging.FieldEventType, "forced_subtitle_no_match"),
			logging.String(logging.FieldErrorHint, "forced subtitles may not be available on OpenSubtitles for this title"),
		)
	}
	return "", nil
}

// buildForcedSubtitleSearchRequest constructs a search request for forced subtitles.
func (s *Service) buildForcedSubtitleSearchRequest(req GenerateRequest) opensubtitles.SearchRequest {
	searchReq := buildBaseSearchRequest(req)
	foreignPartsOnly := true
	searchReq.ForeignPartsOnly = &foreignPartsOnly
	return searchReq
}

// filterForcedSubtitleCandidates filters candidates to only those with strict title matches.
// Forced subtitles require stricter matching than regular subtitles because partial word
// overlap (e.g., "Star Trek: Generations" vs "Star Trek III") can match wrong movies
// in a franchise.
func filterForcedSubtitleCandidates(candidates []scoredSubtitle, expectedTitle string, logger *slog.Logger) []scoredSubtitle {
	result := make([]scoredSubtitle, 0, len(candidates))
	for _, c := range candidates {
		if isTitleStrictMismatch(expectedTitle, c.subtitle.FeatureTitle) {
			if logger != nil {
				logger.Debug("forced subtitle candidate rejected for title mismatch",
					logging.String("expected", expectedTitle),
					logging.String("candidate", c.subtitle.FeatureTitle),
					logging.String("release", c.subtitle.Release),
				)
			}
			continue
		}
		result = append(result, c)
	}
	return result
}

// buildBaseSearchRequest constructs a common search request from a GenerateRequest.
func buildBaseSearchRequest(req GenerateRequest) opensubtitles.SearchRequest {
	queryTitle := strings.TrimSpace(req.Context.Title)
	if !req.Context.IsMovie() {
		if series := strings.TrimSpace(req.Context.SeriesTitle()); series != "" {
			queryTitle = series
		}
	}

	year := strings.TrimSpace(req.Context.Year)
	if !req.Context.IsMovie() {
		// Episode metadata rarely includes per-episode air dates, so avoid sending a
		// season-level year that can incorrectly exclude matches.
		year = ""
	}

	return opensubtitles.SearchRequest{
		TMDBID:       req.Context.TMDBID,
		IMDBID:       req.Context.IMDBID,
		Query:        queryTitle,
		Languages:    append([]string(nil), req.Languages...),
		MediaType:    mediaTypeForContext(req.Context),
		Year:         year,
		Season:       req.Context.Season,
		Episode:      req.Context.Episode,
		ParentTMDBID: req.Context.ParentID(),
	}
}
