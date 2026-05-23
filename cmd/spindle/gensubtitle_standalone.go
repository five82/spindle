package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/subtitle"
	"github.com/five82/spindle/internal/tmdb"
)

type standaloneSubtitleOptions struct {
	TMDBID    int
	MediaType string
	Season    int
	Episode   int
}

type standaloneSubtitleMetadata struct {
	TMDBID    int
	MediaType string
	Title     string
	Year      string
	Season    int
	Episode   int
}

var (
	standaloneEpisodePattern = regexp.MustCompile(`(?i)(^|[^a-z0-9])s(\d{1,2})e(\d{1,3})([^a-z0-9]|$)`)
	standaloneYearPattern    = regexp.MustCompile(`(?i)(^|[^0-9])((?:18|19|20)\d{2})`)
	standaloneReleaseTagSet  = map[string]struct{}{
		"480p": {}, "576p": {}, "720p": {}, "1080p": {}, "2160p": {}, "4k": {},
		"bluray": {}, "blu-ray": {}, "bdrip": {}, "brrip": {}, "dvdrip": {}, "webrip": {}, "webdl": {}, "web-dl": {},
		"x264": {}, "x265": {}, "h264": {}, "h265": {}, "hevc": {}, "av1": {}, "aac": {}, "dts": {}, "truehd": {}, "atmos": {},
	}
)

func resolveStandaloneSubtitleMetadata(ctx context.Context, cfg *config.Config, logger *slog.Logger, sourcePath string, opts standaloneSubtitleOptions) standaloneSubtitleMetadata {
	meta := inferStandaloneSubtitleMetadata(sourcePath)
	if opts.MediaType != "" {
		meta.MediaType = strings.ToLower(strings.TrimSpace(opts.MediaType))
	}
	if opts.Season > 0 {
		meta.Season = opts.Season
	}
	if opts.Episode > 0 {
		meta.Episode = opts.Episode
	}
	if opts.TMDBID > 0 {
		meta.TMDBID = opts.TMDBID
		return meta
	}
	if cfg == nil || strings.TrimSpace(cfg.TMDB.APIKey) == "" || strings.TrimSpace(meta.Title) == "" {
		return meta
	}

	client := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language, logger)
	results, err := client.SearchMulti(ctx, meta.Title)
	if err != nil {
		if logger != nil {
			logger.Warn("tmdb lookup failed",
				"event_type", "tmdb_lookup_failed",
				"error_hint", err.Error(),
				"impact", "standalone forced subtitle lookup may be skipped",
			)
		}
		return meta
	}
	results = filterStandaloneTMDBResults(results, meta.MediaType)
	best := tmdb.SelectBestResult(results, meta.Title, parseStandaloneYear(meta.Year), 5, logger)
	if best == nil {
		return meta
	}
	meta.TMDBID = best.ID
	if best.MediaType != "" {
		meta.MediaType = strings.ToLower(best.MediaType)
	}
	if title := best.DisplayTitle(); title != "" {
		meta.Title = title
	}
	if year := best.Year(); year != "" {
		meta.Year = year
	}
	if logger != nil {
		logger.Info("tmdb metadata attached",
			"tmdb_id", meta.TMDBID,
			"title", meta.Title,
			"year", meta.Year,
			"media_type", meta.MediaType,
		)
	}
	return meta
}

func inferStandaloneSubtitleMetadata(sourcePath string) standaloneSubtitleMetadata {
	base := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))
	meta := standaloneSubtitleMetadata{MediaType: "movie"}

	if match := standaloneEpisodePattern.FindStringSubmatchIndex(base); match != nil {
		meta.MediaType = "tv"
		if len(match) >= 8 {
			meta.Season = atoiDefault(base[match[4]:match[5]])
			meta.Episode = atoiDefault(base[match[6]:match[7]])
		}
		meta.Title = cleanStandaloneReleaseTitle(base[:match[0]])
		if meta.Title == "" {
			meta.Title = cleanStandaloneReleaseTitle(base)
		}
		return meta
	}

	yearMatch := lastStandaloneYearMatch(base)
	if yearMatch != nil {
		meta.Year = base[yearMatch[4]:yearMatch[5]]
		meta.Title = cleanStandaloneReleaseTitle(base[:yearMatch[0]])
		if meta.Title == "" {
			meta.Title = cleanStandaloneReleaseTitle(base)
		}
		return meta
	}

	meta.Title = cleanStandaloneReleaseTitle(base)
	return meta
}

func filterStandaloneTMDBResults(results []tmdb.SearchResult, mediaType string) []tmdb.SearchResult {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "" {
		mediaType = "movie"
	}
	filtered := make([]tmdb.SearchResult, 0, len(results))
	for _, result := range results {
		if result.MediaType != "movie" && result.MediaType != "tv" {
			continue
		}
		if mediaType != "" && result.MediaType != mediaType {
			continue
		}
		filtered = append(filtered, result)
	}
	if len(filtered) > 0 {
		return filtered
	}
	for _, result := range results {
		if result.MediaType == "movie" || result.MediaType == "tv" {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func lastStandaloneYearMatch(value string) []int {
	matches := standaloneYearPattern.FindAllStringSubmatchIndex(value, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]
		if match[5] >= len(value) || !isASCIIDigit(value[match[5]]) {
			return match
		}
	}
	return nil
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func cleanStandaloneReleaseTitle(value string) string {
	value = strings.NewReplacer(".", " ", "_", " ").Replace(value)
	value = strings.ReplaceAll(value, "-", " ")
	fields := strings.Fields(value)
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		trimmed := strings.Trim(field, "()[]{}")
		lower := strings.ToLower(trimmed)
		if _, skip := standaloneReleaseTagSet[lower]; skip {
			break
		}
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, " ")
}

func parseStandaloneYear(value string) int {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	year, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return year
}

func atoiDefault(value string) int {
	n, _ := strconv.Atoi(value)
	return n
}

func normalizeStandaloneSubtitleSource(source, fallback string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		return fallback
	}
	return source
}

func validateStandaloneSubtitleSource(flag, source string) error {
	switch source {
	case "whisperx", "opensubtitles", "none":
		return nil
	default:
		return fmt.Errorf("invalid %s %q: expected whisperx, opensubtitles, or none", flag, source)
	}
}

func firstStandaloneSubtitleLanguage(languages []string) string {
	for _, lang := range languages {
		if strings.TrimSpace(lang) != "" {
			return lang
		}
	}
	return "en"
}

func standaloneOpenSubtitlesClient(cfg *config.Config, logger *slog.Logger) *opensubtitles.Client {
	return opensubtitles.New(
		cfg.Subtitles.OpenSubtitlesAPIKey,
		cfg.Subtitles.OpenSubtitlesUserAgent,
		cfg.Subtitles.OpenSubtitlesUserToken,
		"",
		logger,
	)
}

func fetchStandaloneRegularSubtitle(ctx context.Context, logger *slog.Logger, cfg *config.Config, videoPath, languageCode string, meta standaloneSubtitleMetadata) (subtitle.OpenSubtitlesLookupResult, error) {
	if meta.TMDBID == 0 {
		return subtitle.OpenSubtitlesLookupResult{Decision: "skipped:no_tmdb_id"}, nil
	}
	if cfg == nil || !cfg.Subtitles.OpenSubtitlesEnabled {
		return subtitle.OpenSubtitlesLookupResult{Decision: "skipped:opensubtitles_disabled"}, nil
	}
	if strings.TrimSpace(cfg.Subtitles.OpenSubtitlesAPIKey) == "" {
		return subtitle.OpenSubtitlesLookupResult{}, fmt.Errorf("regular subtitle lookup requires subtitles.opensubtitles_api_key in config.toml or OPENSUBTITLES_API_KEY")
	}
	return subtitle.FetchRegularSubtitle(ctx, logger, cfg, standaloneOpenSubtitlesClient(cfg, logger), subtitle.OpenSubtitlesLookupRequest{
		VideoPath: videoPath,
		TMDBID:    meta.TMDBID,
		Season:    meta.Season,
		Episode:   meta.Episode,
		Language:  languageCode,
		Languages: cfg.Subtitles.OpenSubtitlesLanguages,
	})
}

func fetchStandaloneForcedSubtitle(ctx context.Context, logger *slog.Logger, cfg *config.Config, videoPath, languageCode string, meta standaloneSubtitleMetadata) (subtitle.ForcedLookupResult, error) {
	if meta.TMDBID == 0 {
		return subtitle.ForcedLookupResult{Decision: "skipped:no_tmdb_id"}, nil
	}
	if cfg == nil || !cfg.Subtitles.OpenSubtitlesEnabled {
		return subtitle.ForcedLookupResult{Decision: "skipped:opensubtitles_disabled"}, nil
	}
	if strings.TrimSpace(cfg.Subtitles.OpenSubtitlesAPIKey) == "" {
		return subtitle.ForcedLookupResult{}, fmt.Errorf("forced subtitle lookup requires subtitles.opensubtitles_api_key in config.toml or OPENSUBTITLES_API_KEY")
	}
	return subtitle.FetchForcedSubtitle(ctx, logger, cfg, standaloneOpenSubtitlesClient(cfg, logger), subtitle.ForcedLookupRequest{
		VideoPath: videoPath,
		TMDBID:    meta.TMDBID,
		Season:    meta.Season,
		Episode:   meta.Episode,
		Language:  languageCode,
		Languages: cfg.Subtitles.OpenSubtitlesLanguages,
	})
}
