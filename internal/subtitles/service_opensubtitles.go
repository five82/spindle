package subtitles

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"spindle/internal/logging"
	"spindle/internal/services"
	"spindle/internal/subtitles/opensubtitles"
)

const (
	openSubtitlesMinInterval      = time.Second
	openSubtitlesMaxRateRetries   = 4
	openSubtitlesInitialBackoff   = 2 * time.Second
	openSubtitlesMaxBackoff       = 12 * time.Second
	subtitleIntroAllowanceSeconds = 45.0
	subtitleIntroMinimumSeconds   = 5.0
	suspectOffsetSeconds          = 60.0
	suspectRuntimeMismatchRatio   = 0.07
)

type (
	durationMismatchError struct {
		deltaSeconds float64
		videoSeconds float64
		release      string
	}

	suspectMisIdentificationError struct {
		deltas []float64
	}
)

func (s *Service) shouldUseOpenSubtitles() bool {
	if s == nil || s.config == nil {
		return false
	}
	if !s.config.OpenSubtitlesEnabled {
		return false
	}
	if strings.TrimSpace(s.config.OpenSubtitlesAPIKey) == "" {
		return false
	}
	return true
}

func (e durationMismatchError) Error() string {
	return fmt.Sprintf("subtitle duration delta %.1fs exceeds tolerance", e.deltaSeconds)
}

func (e suspectMisIdentificationError) Error() string {
	return "opensubtitles candidates suggest mis-identification (large consistent offset)"
}

func (e suspectMisIdentificationError) medianAbsDelta() float64 {
	if len(e.deltas) == 0 {
		return 0
	}
	values := append([]float64(nil), e.deltas...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	mid := len(values) / 2
	median := math.Abs(values[mid])
	if len(values)%2 == 0 {
		median = (math.Abs(values[mid-1]) + math.Abs(values[mid])) / 2
	}
	return median
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
			APIKey:    s.config.OpenSubtitlesAPIKey,
			UserAgent: s.config.OpenSubtitlesUserAgent,
			UserToken: s.config.OpenSubtitlesUserToken,
		})
		if err != nil {
			s.openSubsErr = err
			return
		}
		s.openSubs = client
		if s.openSubsCache == nil {
			dir := strings.TrimSpace(s.config.OpenSubtitlesCacheDir)
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
			userAgent := strings.TrimSpace(s.config.OpenSubtitlesUserAgent)
			tokenPresent := strings.TrimSpace(s.config.OpenSubtitlesUserToken) != ""
			s.logger.Info("opensubtitles authentication ready",
				logging.String("user_agent", userAgent),
				logging.Bool("user_token_present", tokenPresent),
			)
			s.openSubsReadyLogged = true
		}
	})
	return s.openSubsErr
}

const subtitleDurationToleranceSeconds = 8.0

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
		s.logger.Info("opensubtitles search completed",
			logging.Int("results", len(resp.Subtitles)),
			logging.Int("total_reported", resp.Total),
		)
	}
	scored := rankSubtitleCandidates(resp.Subtitles, req.Languages, req.Context)
	if len(scored) == 0 {
		if s.logger != nil {
			s.logger.Info("opensubtitles no candidate matched",
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
			s.logger.Info("opensubtitles candidate ranked",
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
					s.logger.Info("opensubtitles candidate failed (soft)",
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
			s.logger.Info("opensubtitles candidate selected",
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

func buildSuspectError(errors []durationMismatchError) error {
	if len(errors) == 0 {
		return nil
	}
	deltas := make([]float64, 0, len(errors))
	for _, e := range errors {
		if e.videoSeconds <= 0 {
			return nil
		}
		deltas = append(deltas, e.deltaSeconds)
		rel := math.Abs(e.deltaSeconds) / e.videoSeconds
		if math.Abs(e.deltaSeconds) < suspectOffsetSeconds && rel < suspectRuntimeMismatchRatio {
			return nil
		}
	}
	return suspectMisIdentificationError{deltas: deltas}
}

func (s *Service) alignDownloadedSubtitles(ctx context.Context, plan *generationPlan, inputPath, outputPath, language string) error {
	if plan == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "align", "Subtitle generation plan not initialized", nil)
	}
	device := whisperXCPUDevice
	if plan.cudaEnabled {
		device = whisperXCUDADevice
	}
	lang := normalizeWhisperLanguage(language)
	if lang == "" {
		lang = "en"
	}
	args := []string{
		"--from", whisperXPackage,
		"python", "-c", whisperXAlignerScript,
		plan.audioPath,
		inputPath,
		outputPath,
		lang,
		device,
	}
	if err := s.run(ctx, whisperXCommand, args...); err != nil {
		return services.Wrap(services.ErrExternalTool, "subtitles", "align", "Failed to align downloaded subtitles", err)
	}
	return nil
}

func mediaTypeForContext(ctx SubtitleContext) string {
	if ctx.IsMovie() {
		return "movie"
	}
	if strings.EqualFold(ctx.MediaType, "episode") {
		return "episode"
	}
	return "episode"
}

func movieVariantSignature(req opensubtitles.SearchRequest) string {
	return fmt.Sprintf("tmdb:%d|parent:%d|imdb:%s|q:%s|y:%s|type:%s", req.TMDBID, req.ParentTMDBID, strings.TrimSpace(req.IMDBID), strings.TrimSpace(req.Query), strings.TrimSpace(req.Year), strings.TrimSpace(req.MediaType))
}

func sanitizeIMDB(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "tt")
	if _, err := strconv.ParseInt(value, 10, 64); err != nil {
		return ""
	}
	return value
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
	if imdb := sanitizeIMDB(base.IMDBID); imdb != "" {
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

	for idx, variant := range unique {
		if s.logger != nil {
			s.logger.Info("opensubtitles search variant",
				logging.Int("attempt", idx+1),
				logging.String("query", variant.Query),
				logging.String("year", variant.Year),
				logging.Int64("tmdb_id", variant.TMDBID),
				logging.String("imdb_id", sanitizeIMDB(variant.IMDBID)),
				logging.String("media_type", variant.MediaType),
			)
		}
		resp, err := s.invokeOpenSubtitlesSearch(ctx, variant)
		if err != nil {
			lastErr := err
			if s.logger != nil {
				s.logger.Warn("opensubtitles search variant failed", logging.Error(err))
			}
			if idx == len(unique)-1 {
				return opensubtitles.SearchResponse{}, lastErr
			}
			continue
		}
		if len(resp.Subtitles) > 0 {
			return resp, nil
		}
	}

	return opensubtitles.SearchResponse{}, nil
}

func (s *Service) fetchOpenSubtitlesPayload(ctx context.Context, req GenerateRequest, candidate opensubtitles.Subtitle) (opensubtitles.DownloadResult, error) {
	if s.openSubs == nil {
		return opensubtitles.DownloadResult{}, errors.New("opensubtitles client unavailable")
	}
	if s.openSubsCache != nil && candidate.FileID > 0 {
		if cached, ok, err := s.openSubsCache.Load(candidate.FileID); err != nil {
			if s.logger != nil {
				s.logger.Warn("opensubtitles cache load failed", logging.Error(err))
			}
		} else if ok {
			if s.logger != nil {
				s.logger.Info("opensubtitles cache hit",
					logging.Int64("file_id", candidate.FileID),
					logging.String("language", cached.Entry.Language),
				)
			}
			return cached.DownloadResult(), nil
		}
	}
	payload, err := s.openSubs.Download(ctx, candidate.FileID, opensubtitles.DownloadOptions{Format: "srt"})
	if err != nil {
		return opensubtitles.DownloadResult{}, err
	}
	s.storeOpenSubtitlesPayload(candidate, payload, req)
	return payload, nil
}

func (s *Service) storeOpenSubtitlesPayload(candidate opensubtitles.Subtitle, payload opensubtitles.DownloadResult, req GenerateRequest) {
	if s.openSubsCache == nil || candidate.FileID <= 0 || len(payload.Data) == 0 {
		return
	}
	entry := opensubtitles.CacheEntry{
		FileID:       candidate.FileID,
		Language:     payload.Language,
		FileName:     payload.FileName,
		DownloadURL:  payload.DownloadURL,
		FeatureTitle: candidate.FeatureTitle,
		FeatureYear:  candidate.FeatureYear,
		Season:       req.Context.Season,
		Episode:      req.Context.Episode,
	}
	if req.Context.IsMovie() {
		entry.TMDBID = req.Context.TMDBID
		entry.ParentTMDBID = req.Context.ParentID()
	} else {
		entry.ParentTMDBID = req.Context.ParentID()
		entry.TMDBID = req.Context.EpisodeID()
	}
	if _, err := s.openSubsCache.Store(entry, payload.Data); err != nil && s.logger != nil {
		s.logger.Warn("opensubtitles cache store failed", logging.Error(err))
	}
}

func (s *Service) searchEpisodeWithVariants(ctx context.Context, base opensubtitles.SearchRequest, showTitle string, season, episode int, episodeTMDBID int64) (opensubtitles.SearchResponse, error) {
	variants := opensubtitles.EpisodeSearchVariants(base, showTitle, season, episode, episodeTMDBID)
	var resp opensubtitles.SearchResponse
	for attempt, variant := range variants {
		if s.logger != nil && attempt == 0 {
			s.logger.Info("opensubtitles search variant",
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
				)
			}
			continue
		}
		if attempt > 0 && s.logger != nil {
			s.logger.Info("opensubtitles fallback search succeeded",
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

func (s *Service) invokeOpenSubtitles(ctx context.Context, op func() error) error {
	if op == nil {
		return errors.New("opensubtitles operation unavailable")
	}
	attempt := 0
	for {
		if err := s.waitForOpenSubtitlesWindow(ctx); err != nil {
			return err
		}
		err := op()
		s.markOpenSubtitlesCall()
		if err == nil {
			return nil
		}
		if !isOpenSubtitlesRetriable(err) || attempt >= openSubtitlesMaxRateRetries {
			return err
		}
		attempt++
		backoff := openSubtitlesInitialBackoff * time.Duration(1<<uint(attempt-1))
		if backoff > openSubtitlesMaxBackoff {
			backoff = openSubtitlesMaxBackoff
		}
		if s.logger != nil {
			s.logger.Warn("opensubtitles rate limited",
				logging.Duration("backoff", backoff),
				logging.Int("attempt", attempt),
			)
		}
		if err := sleepWithContext(ctx, backoff); err != nil {
			return err
		}
	}
}

func (s *Service) waitForOpenSubtitlesWindow(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context unavailable")
	}
	s.openSubsMu.Lock()
	lastCall := s.openSubsLastCall
	s.openSubsMu.Unlock()
	if lastCall.IsZero() {
		return nil
	}
	elapsed := time.Since(lastCall)
	if elapsed >= openSubtitlesMinInterval {
		return nil
	}
	return sleepWithContext(ctx, openSubtitlesMinInterval-elapsed)
}

func (s *Service) markOpenSubtitlesCall() {
	s.openSubsMu.Lock()
	s.openSubsLastCall = time.Now()
	s.openSubsMu.Unlock()
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isOpenSubtitlesRetriable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "429") || strings.Contains(message, "rate limit") {
		return true
	}
	timeoutTokens := []string{
		"timeout",
		"deadline exceeded",
		"client.timeout exceeded",
		"connection reset",
		"connection refused",
		"temporary failure",
		"awaiting headers",
	}
	for _, token := range timeoutTokens {
		if strings.Contains(message, token) {
			return true
		}
	}
	return false
}

func (s *Service) downloadAndAlignCandidate(ctx context.Context, plan *generationPlan, req GenerateRequest, candidate opensubtitles.Subtitle) (GenerateResult, error) {
	if plan == nil {
		return GenerateResult{}, errors.New("generation plan not initialized")
	}
	payload, err := s.fetchOpenSubtitlesPayload(ctx, req, candidate)
	if err != nil {
		return GenerateResult{}, err
	}

	cleaned, stats := CleanSRT(payload.Data)
	cleanedPath := filepath.Join(plan.runDir, "opensubtitles-clean.srt")
	if err := os.WriteFile(cleanedPath, cleaned, 0o644); err != nil {
		return GenerateResult{}, fmt.Errorf("write cleaned subtitles: %w", err)
	}
	if s.logger != nil {
		s.logger.Info("opensubtitles subtitles cleaned",
			logging.String("cleaned_path", cleanedPath),
			logging.Int("removed_cues", stats.RemovedCues),
		)
	}

	inputPath := cleanedPath
	if syncedPath, err := s.applyFFSubsync(ctx, plan, cleanedPath); err != nil {
		if s.logger != nil {
			s.logger.Warn("ffsubsync alignment skipped",
				logging.Error(err),
				logging.String("input", cleanedPath),
			)
		}
	} else if syncedPath != "" {
		inputPath = syncedPath
		if s.logger != nil {
			s.logger.Info("ffsubsync alignment complete",
				logging.String("output_path", syncedPath),
			)
		}
	}

	alignLanguage := req.Context.Language
	if alignLanguage == "" {
		alignLanguage = plan.language
	}
	if s.logger != nil {
		s.logger.Info("opensubtitles aligning subtitles",
			logging.String("language", alignLanguage),
			logging.Bool("cuda_enabled", plan.cudaEnabled),
		)
	}
	if err := s.alignDownloadedSubtitles(ctx, plan, inputPath, plan.outputFile, alignLanguage); err != nil {
		return GenerateResult{}, err
	}
	if s.logger != nil {
		s.logger.Info("opensubtitles alignment complete",
			logging.String("output_path", plan.outputFile),
		)
	}

	segmentCount, err := countSRTCues(plan.outputFile)
	if err != nil {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "analyze srt", "Failed to inspect downloaded subtitles", err)
	}
	if segmentCount == 0 {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "opensubtitles", "Fetched subtitles contained no cues", nil)
	}

	delta, mismatch, err := checkSubtitleDuration(plan.outputFile, plan.totalSeconds)
	if err != nil {
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "duration inspect", "Failed to compare subtitle duration", err)
	}
	if mismatch && delta > 0 {
		if start, last, boundsErr := subtitleBounds(plan.outputFile); boundsErr == nil {
			introGap := start
			if introGap < 0 {
				introGap = 0
			}
			tailDelta := plan.totalSeconds - last
			if introGap >= subtitleIntroMinimumSeconds && tailDelta > 0 && tailDelta <= subtitleIntroAllowanceSeconds {
				if s.logger != nil {
					s.logger.Info("opensubtitles accepted with intro gap",
						logging.Float64("intro_gap_seconds", introGap),
						logging.Float64("tail_delta_seconds", tailDelta),
						logging.String("release", candidate.Release),
					)
				}
				mismatch = false
			}
		}
	}
	if mismatch {
		if s.logger != nil {
			s.logger.Info("opensubtitles candidate soft-rejected (duration mismatch)",
				logging.Float64("delta_seconds", delta),
				logging.String("release", candidate.Release),
			)
		}
		return GenerateResult{}, durationMismatchError{
			deltaSeconds: delta,
			videoSeconds: plan.totalSeconds,
			release:      candidate.Release,
		}
	}

	if s.logger != nil {
		s.logger.Info("open subtitles download complete",
			logging.String("release", strings.TrimSpace(candidate.Release)),
			logging.String("language", candidate.Language),
			logging.Int("segments", segmentCount),
			logging.Bool("ai_translated", candidate.AITranslated),
			logging.Int("removed_cues", stats.RemovedCues),
		)
	}

	finalDuration := plan.totalSeconds
	if finalDuration <= 0 {
		if last, err := lastSRTTimestamp(plan.outputFile); err == nil && last > 0 {
			finalDuration = last
		}
	}

	return GenerateResult{
		SubtitlePath: plan.outputFile,
		SegmentCount: segmentCount,
		Duration:     time.Duration(finalDuration * float64(time.Second)),
		Source:       "opensubtitles",
	}, nil
}

type scoredSubtitle struct {
	subtitle opensubtitles.Subtitle
	score    float64
	reasons  []string
}

func rankSubtitleCandidates(subs []opensubtitles.Subtitle, preferred []string, ctx SubtitleContext) []scoredSubtitle {
	preferred = normalizeLanguageList(preferred)
	if len(subs) == 0 {
		return nil
	}
	var (
		preferredHuman []scoredSubtitle
		preferredAI    []scoredSubtitle
		fallbackHuman  []scoredSubtitle
		fallbackAI     []scoredSubtitle
	)
	for _, sub := range subs {
		if sub.FileID == 0 {
			continue
		}
		entry := scoredSubtitle{
			subtitle: sub,
		}
		entry.score, entry.reasons = scoreSubtitleCandidate(sub, ctx)
		if len(preferred) == 0 {
			if sub.AITranslated {
				fallbackAI = append(fallbackAI, entry)
				continue
			}
			fallbackHuman = append(fallbackHuman, entry)
			continue
		}
		if languageMatches(sub.Language, preferred) {
			if sub.AITranslated {
				preferredAI = append(preferredAI, entry)
			} else {
				preferredHuman = append(preferredHuman, entry)
			}
			continue
		}
		if sub.AITranslated {
			fallbackAI = append(fallbackAI, entry)
		} else {
			fallbackHuman = append(fallbackHuman, entry)
		}
	}
	ordered := make([]scoredSubtitle, 0, len(subs))
	for _, bucket := range [][]scoredSubtitle{preferredHuman, preferredAI, fallbackHuman, fallbackAI} {
		if len(bucket) == 0 {
			continue
		}
		sort.Slice(bucket, func(i, j int) bool {
			if bucket[i].score == bucket[j].score {
				if bucket[i].subtitle.Downloads == bucket[j].subtitle.Downloads {
					return bucket[i].subtitle.FileID < bucket[j].subtitle.FileID
				}
				return bucket[i].subtitle.Downloads > bucket[j].subtitle.Downloads
			}
			return bucket[i].score > bucket[j].score
		})
		ordered = append(ordered, bucket...)
	}
	return ordered
}

func scoreSubtitleCandidate(sub opensubtitles.Subtitle, ctx SubtitleContext) (float64, []string) {
	var reasons []string
	base := math.Log1p(math.Max(0, float64(sub.Downloads)))
	score := base
	reasons = append(reasons, fmt.Sprintf("downloads=%.2f", base))

	releaseScore, releaseReasons := releaseMatchScore(sub.Release)
	score += releaseScore
	reasons = append(reasons, releaseReasons...)

	if ctxYear := parseContextYear(ctx.Year); ctxYear > 0 && sub.FeatureYear > 0 {
		delta := math.Abs(float64(ctxYear - sub.FeatureYear))
		switch {
		case delta == 0:
			score += 1.5
			reasons = append(reasons, "year=exact")
		case delta <= 1:
			score += 1.0
			reasons = append(reasons, "year=close")
		case delta <= 3:
			score -= 0.5
			reasons = append(reasons, "year=off")
		default:
			score -= 1.0
			reasons = append(reasons, "year=far")
		}
	}

	ctxType := canonicalMediaType(ctx.MediaType)
	candidateType := canonicalMediaType(sub.FeatureType)
	if ctxType != "" && candidateType != "" && ctxType != candidateType {
		score -= 1.0
		reasons = append(reasons, "media_type=mismatch")
	}

	if sub.HD {
		score += 0.5
		reasons = append(reasons, "flag=hd")
	}
	if sub.HearingImpaired {
		score -= 0.5
		reasons = append(reasons, "flag=hi")
	}
	if sub.AITranslated {
		score -= 4.0
		reasons = append(reasons, "flag=ai")
	}

	return score, reasons
}

func releaseMatchScore(release string) (float64, []string) {
	release = strings.ToLower(strings.TrimSpace(release))
	if release == "" {
		return 0, nil
	}
	var (
		score   float64
		reasons []string
	)
	apply := func(delta float64, label string, patterns ...string) {
		for _, pattern := range patterns {
			if strings.Contains(release, pattern) {
				score += delta
				reasons = append(reasons, label)
				return
			}
		}
	}
	apply(3.0, "release=bluray", "bluray", "blu-ray", "bdrip", "brrip")
	apply(2.5, "release=remux", "remux")
	apply(1.5, "release=uhd", "2160p", "uhd", "4k")
	apply(1.0, "release=1080p", "1080p")
	apply(0.5, "release=720p", "720p")
	apply(-2.0, "release=web", "webrip", "web-dl", "webdl")
	apply(-1.0, "release=sd", "hdrip", "dvdrip", "tvrip", "hdtv")
	apply(-4.0, "release=cam", "cam", "telesync", "telecine", "ts", "tc", "scr", "screener")
	apply(-1.5, "release=hardcoded", "hcsub", "hardcoded")
	return score, reasons
}

func parseContextYear(value string) int {
	value = strings.TrimSpace(value)
	if len(value) >= 4 {
		year, err := strconv.Atoi(value[:4])
		if err == nil {
			return year
		}
	}
	return 0
}

func canonicalMediaType(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	switch v {
	case "movie", "film":
		return "movie"
	case "episode", "tv", "series", "tv_show", "television":
		return "episode"
	default:
		return ""
	}
}

func languageMatches(language string, preferred []string) bool {
	if len(preferred) == 0 {
		return true
	}
	for _, lang := range preferred {
		if strings.EqualFold(lang, language) {
			return true
		}
	}
	return false
}

func (s *Service) applyFFSubsync(ctx context.Context, plan *generationPlan, inputPath string) (string, error) {
	if plan == nil {
		return "", errors.New("generation plan not initialized")
	}
	audioPath := strings.TrimSpace(plan.audioPath)
	if audioPath == "" {
		return "", errors.New("primary audio reference unavailable for ffsubsync")
	}
	if _, err := os.Stat(audioPath); err != nil {
		return "", fmt.Errorf("ffsubsync audio reference missing: %w", err)
	}
	if _, err := os.Stat(inputPath); err != nil {
		return "", fmt.Errorf("ffsubsync input missing: %w", err)
	}
	outputPath := filepath.Join(plan.runDir, "opensubtitles-ffsubsync.srt")
	args := []string{
		"--from", ffsubsyncPackage,
		"ffsubsync",
		audioPath,
		"-i", inputPath,
		"-o", outputPath,
	}
	if err := s.run(ctx, ffsubsyncCommand, args...); err != nil {
		return "", services.Wrap(services.ErrExternalTool, "subtitles", "ffsubsync", "Failed to synchronize downloaded subtitles", err)
	}
	return outputPath, nil
}

func checkSubtitleDuration(path string, videoSeconds float64) (float64, bool, error) {
	if videoSeconds <= 0 {
		return 0, false, nil
	}
	last, err := lastSRTTimestamp(path)
	if err != nil {
		return 0, false, err
	}
	if last <= 0 {
		return 0, false, nil
	}
	delta := videoSeconds - last
	if math.Abs(delta) <= subtitleDurationToleranceSeconds {
		return delta, false, nil
	}
	return delta, true, nil
}
