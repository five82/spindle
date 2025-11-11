package subtitles

import (
	"context"
	"errors"
	"fmt"
	"math"
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
	searchReq := opensubtitles.SearchRequest{
		IMDBID:       req.Context.IMDBID,
		Query:        strings.TrimSpace(req.Context.Title),
		Languages:    append([]string(nil), req.Languages...),
		MediaType:    mediaTypeForContext(req.Context),
		Year:         strings.TrimSpace(req.Context.Year),
		Season:       req.Context.Season,
		Episode:      req.Context.Episode,
		ParentTMDBID: parentID,
	}
	if req.Context.IsMovie() {
		searchReq.TMDBID = req.Context.TMDBID
	} else if episodeID := req.Context.EpisodeID(); episodeID > 0 {
		searchReq.TMDBID = episodeID
	}

	resp, err := s.openSubs.Search(ctx, searchReq)
	if err != nil {
		return GenerateResult{}, false, err
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

	var lastErr error
	for idx, candidate := range scored {
		result, err := s.downloadAndAlignCandidate(ctx, plan, req, candidate.subtitle)
		if err != nil {
			lastErr = err
			if s.logger != nil {
				s.logger.Warn("opensubtitles candidate failed",
					logging.Error(err),
					logging.Int("rank", idx+1),
					logging.String("language", candidate.subtitle.Language),
					logging.String("release", candidate.subtitle.Release),
					logging.Float64("score", candidate.score),
				)
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
		return GenerateResult{}, false, lastErr
	}
	return GenerateResult{}, false, nil
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
	if mismatch {
		if s.logger != nil {
			s.logger.Warn("opensubtitles candidate rejected due to duration mismatch",
				logging.Float64("delta_seconds", delta),
				logging.String("release", candidate.Release),
			)
		}
		return GenerateResult{}, services.Wrap(services.ErrTransient, "subtitles", "duration mismatch", fmt.Sprintf("subtitle duration delta %.1fs exceeds tolerance", delta), nil)
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
