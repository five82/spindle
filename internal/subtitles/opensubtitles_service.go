package subtitles

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"spindle/internal/logging"
	"spindle/internal/services"
	"spindle/internal/subtitles/opensubtitles"
)

const maxLoggedOpenSubCandidates = 6

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

	// Limit candidates to avoid excessive processing time.
	// If top candidates fail, lower-ranked ones are unlikely to succeed.
	candidatesToTry := scored
	if len(candidatesToTry) > maxOpenSubtitlesCandidates {
		candidatesToTry = candidatesToTry[:maxOpenSubtitlesCandidates]
	}

	var (
		lastErr       error
		mismatchErrs  []durationMismatchError
		allDurationMM = true
		summaryLines  []string
	)

	for idx, candidate := range candidatesToTry {
		result, err := s.downloadAndAlignCandidate(ctx, plan, req, candidate.subtitle)

		status := "Rejected"
		var reason string
		details := ""

		if err != nil {
			lastErr = err
			var mismatch durationMismatchError
			var earlyReject earlyDurationRejectError
			if errors.As(err, &mismatch) {
				mismatchErrs = append(mismatchErrs, mismatch)
				reason = "duration_mismatch"
				details = fmt.Sprintf(" [Diff: %.1fs]", mismatch.deltaSeconds)
			} else if errors.As(err, &earlyReject) {
				// Early rejection counts as duration mismatch for summary purposes
				mismatchErrs = append(mismatchErrs, durationMismatchError{
					deltaSeconds: earlyReject.deltaSeconds,
					videoSeconds: plan.totalSeconds,
					release:      earlyReject.release,
				})
				reason = "early_duration_reject"
				details = fmt.Sprintf(" [Diff: %.1fs, skipped alignment]", earlyReject.deltaSeconds)
			} else {
				allDurationMM = false
				reason = "download_or_align_failed"
				details = fmt.Sprintf(" [%v]", err)
			}
		} else {
			status = "Selected"
			reason = "best_match"
		}

		// Build summary line for this candidate
		factors := strings.Join(candidate.reasons, ", ")
		line := fmt.Sprintf("#%d (Score: %.1f): %s (%s)%s [Lang: %s, DLs: %d, Rel: %s] {%s}",
			idx+1,
			candidate.score,
			status,
			reason,
			details,
			candidate.subtitle.Language,
			candidate.subtitle.Downloads,
			strings.TrimSpace(candidate.subtitle.Release),
			factors,
		)
		summaryLines = append(summaryLines, line)

		if err == nil {
			// Success
			if s.logger != nil {
				infoAttrs := buildCandidateSummaryAttrs(
					"decision_summary",
					"opensubtitles_candidate_summary",
					"selected",
					"match_found",
					summaryLines,
					maxLoggedOpenSubCandidates,
				)
				s.logger.Info("opensubtitles candidate summary", logging.Args(infoAttrs...)...)

				debugAttrs := buildCandidateSummaryAttrs(
					"decision_summary_full",
					"opensubtitles_candidate_summary",
					"selected",
					"match_found",
					summaryLines,
					0,
				)
				s.logger.Debug("opensubtitles candidate summary", logging.Args(debugAttrs...)...)
			}
			return result, true, nil
		}
	}

	// Determine the actual failure reason for logging
	failureReason := "no_match"
	var returnErr error
	if lastErr != nil {
		if len(mismatchErrs) == len(candidatesToTry) && len(candidatesToTry) > 0 && allDurationMM {
			failureReason = fmt.Sprintf("all_%d_candidates_duration_mismatch", len(candidatesToTry))
			returnErr = buildSuspectError(mismatchErrs)
			if returnErr == nil {
				// buildSuspectError returned nil, but we still want to indicate duration mismatch
				returnErr = suspectMisIdentificationError{deltas: extractDeltas(mismatchErrs)}
			}
		} else {
			failureReason = "candidate_errors"
			returnErr = lastErr
		}
	}

	if s.logger != nil {
		infoAttrs := buildCandidateSummaryAttrs(
			"decision_summary",
			"opensubtitles_candidate_summary",
			"rejected",
			failureReason,
			summaryLines,
			maxLoggedOpenSubCandidates,
		)
		s.logger.Info("opensubtitles candidate summary", logging.Args(infoAttrs...)...)

		debugAttrs := buildCandidateSummaryAttrs(
			"decision_summary_full",
			"opensubtitles_candidate_summary",
			"rejected",
			failureReason,
			summaryLines,
			0,
		)
		s.logger.Debug("opensubtitles candidate summary", logging.Args(debugAttrs...)...)
	}

	if returnErr != nil {
		return GenerateResult{}, false, returnErr
	}
	return GenerateResult{}, false, nil
}

func extractDeltas(errs []durationMismatchError) []float64 {
	deltas := make([]float64, 0, len(errs))
	for _, e := range errs {
		deltas = append(deltas, e.deltaSeconds)
	}
	return deltas
}

func buildCandidateSummaryAttrs(eventType, decisionType, result, reason string, summaryLines []string, limit int) []logging.Attr {
	attrs := []logging.Attr{
		logging.String(logging.FieldEventType, eventType),
		logging.String(logging.FieldDecisionType, decisionType),
		logging.String("decision_result", result),
		logging.String("decision_reason", reason),
		logging.Int("candidate_count", len(summaryLines)),
	}
	if limit <= 0 || limit > len(summaryLines) {
		limit = len(summaryLines)
	}
	if limit < len(summaryLines) {
		attrs = append(attrs, logging.Int("candidate_hidden_count", len(summaryLines)-limit))
	}
	for idx := 0; idx < limit; idx++ {
		attrs = append(attrs, logging.String(fmt.Sprintf("candidate_%d", idx+1), summaryLines[idx]))
	}
	return attrs
}
