package contentid

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/subtitles/opensubtitles"
)

func (m *Matcher) fetchReferenceFingerprints(ctx context.Context, info episodeContext, season *tmdb.SeasonDetails, candidates []int, progress func(phase string, current, total int, episodeKey string)) ([]referenceFingerprint, error) {
	references := make([]referenceFingerprint, 0, len(candidates))
	unique := make([]int, 0, len(candidates))
	seen := make(map[int]struct{}, len(candidates))
	for _, num := range candidates {
		if _, ok := seen[num]; ok {
			continue
		}
		seen[num] = struct{}{}
		unique = append(unique, num)
	}
	var lastAPICall time.Time
	for idx, num := range unique {
		episodeKey := fmt.Sprintf("s%02de%02d", season.SeasonNumber, num)
		episodeData, ok := findEpisodeByNumber(season, num)
		if !ok {
			if progress != nil {
				progress(PhaseReference, idx+1, len(unique), episodeKey)
			}
			continue
		}
		episodeYear := strings.TrimSpace(episodeData.AirDate)
		if len(episodeYear) >= 4 {
			episodeYear = episodeYear[:4]
		} else {
			episodeYear = ""
		}
		parentID := info.SubtitleCtx.ParentID()
		searchReq := opensubtitles.SearchRequest{
			ParentTMDBID: parentID,
			Query:        info.ShowTitle,
			Languages:    append([]string(nil), m.languages...),
			Season:       season.SeasonNumber,
			Episode:      episodeData.EpisodeNumber,
			MediaType:    "episode",
			Year:         episodeYear,
		}
		searchVariants := opensubtitles.EpisodeSearchVariants(searchReq, info.ShowTitle, season.SeasonNumber, episodeData.EpisodeNumber, episodeData.ID)
		var (
			resp       opensubtitles.SearchResponse
			selected   opensubtitles.SearchRequest
			searchErr  error
			foundMatch bool
		)
		for attempt, variant := range searchVariants {
			searchErr = m.invokeOpenSubtitles(ctx, &lastAPICall, func() error {
				var err error
				resp, err = m.openSubs.Search(ctx, variant)
				return err
			})
			if searchErr != nil {
				return nil, fmt.Errorf("opensubtitles search s%02de%02d attempt %d: %w", season.SeasonNumber, num, attempt+1, searchErr)
			}
			if len(resp.Subtitles) == 0 {
				if m.logger != nil {
					m.logger.Warn("opensubtitles returned no candidates",
						logging.Int("season", season.SeasonNumber),
						logging.Int("episode", num),
						logging.Int("attempt", attempt+1),
						logging.String(logging.FieldEventType, "opensubtitles_no_candidates"),
						logging.String(logging.FieldImpact, "episode matching may fall back to WhisperX-only heuristics"),
						logging.String(logging.FieldErrorHint, "Verify OpenSubtitles languages and TMDB metadata"),
					)
				}
				continue
			}
			selected = variant
			foundMatch = true
			if m.logger != nil {
				m.logger.Debug("opensubtitles reference search selected",
					logging.String(logging.FieldEventType, "decision_summary"),
					logging.String(logging.FieldDecisionType, "opensubtitles_reference_search"),
					logging.String("decision_result", "selected"),
					logging.String("decision_reason", "candidates_available"),
					logging.String("decision_options", "search, skip"),
					logging.Int("season", season.SeasonNumber),
					logging.Int("episode", num),
					logging.Int("attempt", attempt+1),
					logging.Int("attempts_total", len(searchVariants)),
					logging.Int("candidates", len(resp.Subtitles)),
				)
			}
			break
		}
		if !foundMatch {
			if m.logger != nil {
				m.logger.Debug("opensubtitles reference search skipped",
					logging.String(logging.FieldEventType, "decision_summary"),
					logging.String(logging.FieldDecisionType, "opensubtitles_reference_search"),
					logging.String("decision_result", "skipped"),
					logging.String("decision_reason", "no_candidates"),
					logging.String("decision_options", "search, skip"),
					logging.Int("season", season.SeasonNumber),
					logging.Int("episode", num),
					logging.Int("attempts_total", len(searchVariants)),
				)
			}
			if progress != nil {
				progress(PhaseReference, idx+1, len(unique), episodeKey)
			}
			continue
		}
		candidate, selectedIdx, selectionReason := selectReferenceCandidate(resp.Subtitles, episodeData.Name, season)
		if m.logger != nil {
			attrs := []logging.Attr{
				logging.String(logging.FieldEventType, "decision_summary"),
				logging.String(logging.FieldDecisionType, "opensubtitles_reference_pick"),
				logging.String("decision_result", "selected"),
				logging.String("decision_reason", selectionReason),
				logging.String("decision_options", "select, skip"),
				logging.Int("season", season.SeasonNumber),
				logging.Int("episode", episodeData.EpisodeNumber),
				logging.Int("candidate_count", len(resp.Subtitles)),
				logging.Int64("file_id", candidate.FileID),
				logging.String("language", strings.TrimSpace(candidate.Language)),
				logging.Int("downloads", candidate.Downloads),
				logging.String("release", strings.TrimSpace(candidate.Release)),
				logging.Bool("hearing_impaired", candidate.HearingImpaired),
			}
			if selectedIdx > 0 {
				attrs = append(attrs, logging.Int("skipped_candidates", selectedIdx))
			}
			if len(resp.Subtitles) > 1 {
				attrs = append(attrs, logging.Int("candidate_hidden_count", len(resp.Subtitles)-1))
			}
			m.logger.Debug("opensubtitles reference selection",
				logging.Args(attrs...)...,
			)
		}
		var (
			payload   opensubtitles.DownloadResult
			cachePath string
			cacheHit  bool
		)
		if m.cache != nil && candidate.FileID > 0 {
			if cached, ok, err := m.cache.Load(candidate.FileID); err != nil {
				m.logger.Warn("opensubtitles cache load failed",
					logging.Error(err),
					logging.String(logging.FieldEventType, "opensubtitles_cache_load_failed"),
					logging.String(logging.FieldImpact, "cache miss forces network download"),
					logging.String(logging.FieldErrorHint, "Check opensubtitles_cache_dir permissions"))
			} else if ok {
				payload = cached.DownloadResult()
				cachePath = cached.Path
				cacheHit = true
				m.logger.Debug("opensubtitles cache hit",
					logging.Int("season", season.SeasonNumber),
					logging.Int("episode", episodeData.EpisodeNumber),
					logging.Int64("file_id", candidate.FileID),
				)
			}
		}
		if !cacheHit {
			if err := m.invokeOpenSubtitles(ctx, &lastAPICall, func() error {
				var err error
				payload, err = m.openSubs.Download(ctx, candidate.FileID, opensubtitles.DownloadOptions{Format: "srt"})
				return err
			}); err != nil {
				return nil, fmt.Errorf("download opensubtitles file %d: %w", candidate.FileID, err)
			}
			if m.cache != nil && len(payload.Data) > 0 {
				entry := opensubtitles.CacheEntry{
					FileID:       candidate.FileID,
					Language:     payload.Language,
					FileName:     payload.FileName,
					DownloadURL:  payload.DownloadURL,
					TMDBID:       selected.TMDBID,
					ParentTMDBID: selected.ParentTMDBID,
					Season:       season.SeasonNumber,
					Episode:      episodeData.EpisodeNumber,
					FeatureTitle: candidate.FeatureTitle,
					FeatureYear:  candidate.FeatureYear,
				}
				if entry.ParentTMDBID == 0 {
					entry.ParentTMDBID = parentID
				}
				if entry.TMDBID == 0 {
					entry.TMDBID = info.SubtitleCtx.EpisodeID()
				}
				if path, err := m.cache.Store(entry, payload.Data); err != nil {
					m.logger.Warn("opensubtitles cache store failed",
						logging.Error(err),
						logging.String(logging.FieldEventType, "opensubtitles_cache_store_failed"),
						logging.String(logging.FieldImpact, "future runs will re-download reference subtitles"),
						logging.String(logging.FieldErrorHint, "Check opensubtitles_cache_dir permissions and free space"))
				} else {
					cachePath = path
				}
			}
		}
		text, err := normalizeSubtitlePayload(payload.Data)
		if err != nil {
			return nil, fmt.Errorf("normalize opensubtitles payload: %w", err)
		}
		fp := newFingerprint(text)
		if fp == nil {
			if progress != nil {
				progress(PhaseReference, idx+1, len(unique), episodeKey)
			}
			return nil, fmt.Errorf("empty opensubtitles transcript for S%02dE%02d", season.SeasonNumber, num)
		}
		references = append(references, referenceFingerprint{
			EpisodeNumber: episodeData.EpisodeNumber,
			Title:         strings.TrimSpace(episodeData.Name),
			Vector:        fp,
			FileID:        candidate.FileID,
			Language:      payload.Language,
			CachePath:     cachePath,
		})
		if progress != nil {
			progress(PhaseReference, idx+1, len(unique), episodeKey)
		}
		m.logger.Debug("opensubtitles reference downloaded",
			logging.Int("season", episodeData.SeasonNumber),
			logging.Int("episode", episodeData.EpisodeNumber),
			logging.String("title", episodeData.Name),
			logging.Int("token_count", fp.TokenCount()),
		)
	}
	return references, nil
}

// selectReferenceCandidate picks the best candidate from OpenSubtitles results.
// It skips candidates whose release name contains a different episode's TMDB
// title but not the expected episode's title, which indicates mislabeled metadata
// on OpenSubtitles. Among title-consistent candidates it prefers non-HI subtitles,
// since HI annotations dilute similarity scores against WhisperX transcripts.
// Falls back to the top result (highest download count) if all candidates appear
// suspect.
//
// Returned reason values:
//
//	"top_result"               – first candidate selected with no reranking
//	"title_consistency_rerank" – skipped higher-ranked candidates for title mismatch
//	"non_hi_preferred"         – selected a non-HI candidate over a higher-ranked HI one
//	"hi_fallback"              – all acceptable candidates are HI; picked the first
func selectReferenceCandidate(candidates []opensubtitles.Subtitle, episodeTitle string, season *tmdb.SeasonDetails) (opensubtitles.Subtitle, int, string) {
	if len(candidates) <= 1 {
		return candidates[0], 0, "top_result"
	}

	currentTitle := strings.ToLower(strings.TrimSpace(episodeTitle))
	const minTitleLen = 5
	if len(currentTitle) < minTitleLen {
		return preferNonHI(candidates)
	}

	// Collect TMDB titles from other episodes in the season.
	var otherTitles []string
	for _, ep := range season.Episodes {
		t := strings.ToLower(strings.TrimSpace(ep.Name))
		if t == currentTitle || len(t) < minTitleLen {
			continue
		}
		otherTitles = append(otherTitles, t)
	}
	if len(otherTitles) == 0 {
		return preferNonHI(candidates)
	}

	// Collect candidates that pass the title-consistency check.
	type indexedCandidate struct {
		sub opensubtitles.Subtitle
		idx int
	}
	var acceptable []indexedCandidate
	for i, c := range candidates {
		release := strings.ToLower(strings.TrimSpace(c.Release))
		if release == "" {
			acceptable = append(acceptable, indexedCandidate{c, i})
			continue
		}
		referencesOther := containsAnySubstring(release, otherTitles)
		if !referencesOther || strings.Contains(release, currentTitle) {
			acceptable = append(acceptable, indexedCandidate{c, i})
			continue
		}
		// Skip: release clearly references a different episode.
	}

	if len(acceptable) > 0 {
		// Among acceptable candidates, prefer non-HI.
		for j, ac := range acceptable {
			if !ac.sub.HearingImpaired {
				reason := "top_result"
				if j > 0 {
					// Skipped earlier acceptable candidates because they were HI.
					reason = "non_hi_preferred"
				} else if ac.idx > 0 {
					// First acceptable candidate, but title-consistency skipped earlier originals.
					reason = "title_consistency_rerank"
				}
				return ac.sub, ac.idx, reason
			}
		}
		// All acceptable candidates are HI -- return the first acceptable.
		first := acceptable[0]
		reason := "hi_fallback"
		if first.idx > 0 {
			reason = "title_consistency_rerank"
		}
		return first.sub, first.idx, reason
	}

	// All candidates look suspect -- prefer non-HI among them.
	return preferNonHI(candidates)
}

// preferNonHI returns the first non-HI candidate from the slice, falling back
// to the first element if all are HI.
func preferNonHI(candidates []opensubtitles.Subtitle) (opensubtitles.Subtitle, int, string) {
	for i, c := range candidates {
		if !c.HearingImpaired {
			if i > 0 {
				return c, i, "non_hi_preferred"
			}
			return c, 0, "top_result"
		}
	}
	return candidates[0], 0, "hi_fallback"
}

// containsAnySubstring reports whether s contains any of the given substrings.
func containsAnySubstring(s string, substrings []string) bool {
	for _, sub := range substrings {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func (m *Matcher) invokeOpenSubtitles(ctx context.Context, lastCall *time.Time, op func() error) error {
	if op == nil {
		return errors.New("opensubtitles operation unavailable")
	}
	attempt := 0
	for {
		if err := waitForOpenSubtitlesWindow(ctx, lastCall); err != nil {
			return err
		}
		err := op()
		if lastCall != nil {
			*lastCall = time.Now()
		}
		if err == nil {
			return nil
		}
		if !opensubtitles.IsRetriable(err) || attempt >= opensubtitles.MaxRateRetries {
			return err
		}
		attempt++
		backoff := opensubtitles.InitialBackoff * time.Duration(1<<uint(attempt-1))
		if backoff > opensubtitles.MaxBackoff {
			backoff = opensubtitles.MaxBackoff
		}
		if m.logger != nil {
			m.logger.Warn("opensubtitles rate limited",
				logging.Duration("backoff", backoff),
				logging.Int("attempt", attempt),
				logging.String(logging.FieldEventType, "opensubtitles_rate_limited"),
				logging.String(logging.FieldImpact, "episode matching delayed while respecting API limits"),
				logging.String(logging.FieldErrorHint, "Wait and retry or check OpenSubtitles rate limits"),
			)
		}
		if err := opensubtitles.SleepWithContext(ctx, backoff); err != nil {
			return err
		}
	}
}

func waitForOpenSubtitlesWindow(ctx context.Context, lastCall *time.Time) error {
	if ctx == nil {
		return errors.New("context unavailable")
	}
	if lastCall == nil || lastCall.IsZero() {
		return nil
	}
	elapsed := time.Since(*lastCall)
	if elapsed >= opensubtitles.MinInterval {
		return nil
	}
	return opensubtitles.SleepWithContext(ctx, opensubtitles.MinInterval-elapsed)
}
