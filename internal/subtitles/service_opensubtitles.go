package subtitles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

	searchReq := opensubtitles.SearchRequest{
		TMDBID:    req.Context.TMDBID,
		IMDBID:    req.Context.IMDBID,
		Query:     strings.TrimSpace(req.Context.Title),
		Languages: append([]string(nil), req.Languages...),
		MediaType: mediaTypeForContext(req.Context),
		Year:      strings.TrimSpace(req.Context.Year),
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
	candidate, ok := selectSubtitleCandidate(resp.Subtitles, req.Languages)
	if !ok {
		if s.logger != nil {
			s.logger.Info("opensubtitles no candidate matched",
				logging.Int("results", len(resp.Subtitles)),
				logging.String("languages", strings.Join(req.Languages, ",")),
			)
		}
		return GenerateResult{}, false, nil
	}

	payload, err := s.openSubs.Download(ctx, candidate.FileID, opensubtitles.DownloadOptions{Format: "srt"})
	if err != nil {
		return GenerateResult{}, false, err
	}

	cleaned, stats := CleanSRT(payload.Data)
	cleanedPath := filepath.Join(plan.runDir, "opensubtitles-clean.srt")
	if err := os.WriteFile(cleanedPath, cleaned, 0o644); err != nil {
		return GenerateResult{}, false, fmt.Errorf("write cleaned subtitles: %w", err)
	}
	if s.logger != nil {
		s.logger.Info("opensubtitles subtitles cleaned",
			logging.String("cleaned_path", cleanedPath),
			logging.Int("removed_cues", stats.RemovedCues),
		)
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
	if err := s.alignDownloadedSubtitles(ctx, plan, cleanedPath, plan.outputFile, alignLanguage); err != nil {
		return GenerateResult{}, false, err
	}
	if s.logger != nil {
		s.logger.Info("opensubtitles alignment complete",
			logging.String("output_path", plan.outputFile),
		)
	}

	segmentCount, err := countSRTCues(plan.outputFile)
	if err != nil {
		return GenerateResult{}, false, services.Wrap(services.ErrTransient, "subtitles", "analyze srt", "Failed to inspect downloaded subtitles", err)
	}
	if segmentCount == 0 {
		return GenerateResult{}, false, services.Wrap(services.ErrTransient, "subtitles", "opensubtitles", "Fetched subtitles contained no cues", nil)
	}

	if s.logger != nil {
		s.logger.Info("open subtitles download complete",
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
	}, true, nil
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

func selectSubtitleCandidate(subs []opensubtitles.Subtitle, preferred []string) (opensubtitles.Subtitle, bool) {
	if len(subs) == 0 {
		return opensubtitles.Subtitle{}, false
	}
	preferred = normalizeLanguageList(preferred)
	matchLanguage := func(sub opensubtitles.Subtitle, allowAI bool) bool {
		if sub.FileID == 0 {
			return false
		}
		if len(preferred) == 0 {
			return allowAI || !sub.AITranslated
		}
		for _, lang := range preferred {
			if strings.EqualFold(lang, sub.Language) {
				if sub.AITranslated && !allowAI {
					return false
				}
				return true
			}
		}
		return false
	}
	for _, sub := range subs {
		if matchLanguage(sub, false) {
			return sub, true
		}
	}
	for _, sub := range subs {
		if matchLanguage(sub, true) {
			return sub, true
		}
	}
	for _, sub := range subs {
		if sub.FileID != 0 && !sub.AITranslated {
			return sub, true
		}
	}
	for _, sub := range subs {
		if sub.FileID != 0 {
			return sub, true
		}
	}
	return opensubtitles.Subtitle{}, false
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
