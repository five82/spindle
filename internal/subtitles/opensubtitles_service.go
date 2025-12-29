package subtitles

import (
	"context"
	"errors"
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
						s.logger.Warn("opensubtitles cache unavailable",
							logging.Error(err),
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
			s.logger.Info("opensubtitles authentication ready",
				logging.String("user_agent", userAgent),
				logging.Bool("user_token_present", tokenPresent),
			)
			s.openSubsReadyLogged = true
		}
	})
	return s.openSubsErr
}

func (s *Service) tryOpenSubtitles(ctx context.Context, plan *generationPlan, req GenerateRequest) (GenerateResult, bool, error) {
	if plan == nil {
		return GenerateResult{}, false, nil
	}
	if err := s.ensureOpenSubtitlesReady(); err != nil {
		return GenerateResult{}, false, err
	}
	if s.openSubs == nil {
		return GenerateResult{}, false, errors.New("opensubtitles client unavailable")
	}

	parentID := req.Context.ParentID()
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
	searchReq := opensubtitles.SearchRequest{
		IMDBID:       req.Context.IMDBID,
		Query:        queryTitle,
		Languages:    append([]string(nil), req.Languages...),
		MediaType:    mediaTypeForContext(req.Context),
		Year:         year,
		Season:       req.Context.Season,
		Episode:      req.Context.Episode,
		ParentTMDBID: parentID,
	}

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
		return GenerateResult{}, false, searchErr
	}
	if s.logger != nil {
		s.logger.Debug("opensubtitles search completed",
			logging.Int("results", len(resp.Subtitles)),
			logging.Int("total_reported", resp.Total),
		)
	}
	scored := rankSubtitleCandidates(resp.Subtitles, req.Languages, req.Context)
	if len(scored) == 0 {
		if s.logger != nil {
			s.logger.Debug("opensubtitles no candidate matched",
				logging.Int("results", len(resp.Subtitles)),
				logging.String("languages", strings.Join(req.Languages, ",")),
			)
		}
		return GenerateResult{}, false, nil
	}

	if s.logger != nil {
		limit := len(scored)
		if limit > 5 {
			limit = 5
		}
		for idx := 0; idx < limit; idx++ {
			s.logger.Debug("opensubtitles candidate ranked",
				logging.Int("rank", idx+1),
				logging.String("language", scored[idx].subtitle.Language),
				logging.Int("downloads", scored[idx].subtitle.Downloads),
				logging.Bool("ai_translated", scored[idx].subtitle.AITranslated),
				logging.String("release", scored[idx].subtitle.Release),
				logging.Float64("score", scored[idx].score),
				logging.String("score_reasons", strings.Join(scored[idx].reasons, ",")),
			)
		}
	}

	var (
		lastErr       error
		mismatchErrs  []durationMismatchError
		allDurationMM = true
	)
	for idx, candidate := range scored {
		result, err := s.downloadAndAlignCandidate(ctx, plan, req, candidate.subtitle)
		if err != nil {
			lastErr = err
			var mismatch durationMismatchError
			if errors.As(err, &mismatch) {
				mismatchErrs = append(mismatchErrs, mismatch)
			} else {
				allDurationMM = false
			}
			if s.logger != nil {
				isSoft := errors.As(err, &mismatch)
				if isSoft {
					s.logger.Debug("opensubtitles candidate failed (soft)",
						logging.Error(err),
						logging.Int("rank", idx+1),
						logging.String("language", candidate.subtitle.Language),
						logging.String("release", candidate.subtitle.Release),
						logging.Float64("score", candidate.score),
						logging.Bool("soft_reject", true),
					)
				} else {
					s.logger.Warn("opensubtitles candidate failed",
						logging.Error(err),
						logging.Int("rank", idx+1),
						logging.String("language", candidate.subtitle.Language),
						logging.String("release", candidate.subtitle.Release),
						logging.Float64("score", candidate.score),
						logging.Bool("soft_reject", false),
					)
				}
			}
			continue
		}
		if s.logger != nil {
			s.logger.Debug("opensubtitles candidate selected",
				logging.Int("rank", idx+1),
				logging.String("release", strings.TrimSpace(candidate.subtitle.Release)),
				logging.String("language", candidate.subtitle.Language),
				logging.Int("downloads", candidate.subtitle.Downloads),
				logging.Bool("ai_translated", candidate.subtitle.AITranslated),
				logging.Float64("score", candidate.score),
				logging.String("score_reasons", strings.Join(candidate.reasons, ",")),
			)
		}
		return result, true, nil
	}

	if lastErr != nil {
		if len(mismatchErrs) == len(scored) && len(scored) > 0 && allDurationMM {
			if suspect := buildSuspectError(mismatchErrs); suspect != nil {
				return GenerateResult{}, false, suspect
			}
		}
		return GenerateResult{}, false, lastErr
	}
	return GenerateResult{}, false, nil
}
