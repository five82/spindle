package subtitle

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/language"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/opensubtitles"
)

// OpenSubtitlesLookupRequest describes a standalone OpenSubtitles lookup.
type OpenSubtitlesLookupRequest struct {
	VideoPath  string
	TMDBID     int
	Season     int
	Episode    int
	Language   string
	Languages  []string
	EpisodeKey string
}

// OpenSubtitlesLookupResult reports an OpenSubtitles lookup outcome.
type OpenSubtitlesLookupResult struct {
	Path     string
	Decision string
}

// ForcedLookupRequest describes a standalone or stage forced-subtitle lookup.
type ForcedLookupRequest = OpenSubtitlesLookupRequest

// ForcedLookupResult reports the forced-subtitle lookup outcome.
type ForcedLookupResult = OpenSubtitlesLookupResult

// FetchForcedSubtitle searches OpenSubtitles for a foreign-parts-only subtitle,
// downloads the best candidate, and writes it as a cleaned SRT beside VideoPath.
func FetchForcedSubtitle(
	ctx context.Context,
	logger *slog.Logger,
	cfg *config.Config,
	client *opensubtitles.Client,
	req ForcedLookupRequest,
) (ForcedLookupResult, error) {
	logger = logs.Default(logger)
	if strings.TrimSpace(req.EpisodeKey) != "" {
		logger = logger.With("episode_key", req.EpisodeKey)
	}
	if req.TMDBID == 0 {
		logger.Info("forced subtitle search skipped",
			"decision_type", logs.DecisionForcedSubtitleSearch,
			"decision_result", "skipped",
			"decision_reason", "no TMDB ID available",
		)
		return ForcedLookupResult{Decision: "skipped:no_tmdb_id"}, nil
	}
	if cfg == nil || !cfg.Subtitles.OpenSubtitlesEnabled {
		logger.Info("forced subtitle search skipped",
			"decision_type", logs.DecisionForcedSubtitleSearch,
			"decision_result", "skipped",
			"decision_reason", "opensubtitles_enabled is false",
		)
		return ForcedLookupResult{Decision: "skipped:opensubtitles_disabled"}, nil
	}
	if client == nil {
		return ForcedLookupResult{Decision: "error:client_unavailable"}, fmt.Errorf("opensubtitles client unavailable")
	}

	languages := opensubtitlesLookupLanguages(req.Languages, cfg)

	results, err := client.Search(ctx, req.TMDBID, req.Season, req.Episode, languages)
	if err != nil {
		logger.Warn("opensubtitles search failed",
			"event_type", "opensubtitles_error",
			"error_hint", err.Error(),
			"impact", "forced subtitle lookup skipped",
		)
		return ForcedLookupResult{Decision: "error:search_failed"}, nil
	}

	bestIndex, hasBest := rankForcedSubtitleCandidates(results, languages)
	for i, r := range results {
		var result string
		switch {
		case !r.Attributes.ForeignPartsOnly || forcedSubtitleGarbageSource(r):
			result = "skipped"
		case hasBest && i == bestIndex:
			result = "selected"
		default:
			result = "candidate"
		}
		logger.Info("forced subtitle candidate",
			"decision_type", logs.DecisionSubtitleRank,
			"decision_result", result,
			"foreign_parts_only", r.Attributes.ForeignPartsOnly,
			"language", r.Attributes.Language,
			"downloads", r.Attributes.DownloadCount,
			"files", len(r.Attributes.Files),
			"release", r.Attributes.Release,
		)
	}

	if !hasBest {
		logger.Info("no forced subtitles found on OpenSubtitles",
			"decision_type", logs.DecisionForcedSubtitle,
			"decision_result", "none_available",
			"decision_reason", "no foreign_parts_only results",
		)
		return ForcedLookupResult{Decision: "none_available"}, nil
	}

	best := results[bestIndex]
	logger.Info("forced subtitle candidate selected",
		"decision_type", logs.DecisionForcedSubtitleRanking,
		"decision_result", "selected",
		"decision_reason", fmt.Sprintf("candidates=%d best_downloads=%d", len(results), best.Attributes.DownloadCount),
	)
	if len(best.Attributes.Files) == 0 {
		logger.Warn("forced subtitle has no downloadable files",
			"event_type", "opensubtitles_no_files",
			"error_hint", "best forced subtitle result has zero files",
			"impact", "forced subtitle not downloaded",
		)
		return ForcedLookupResult{Decision: "error:no_files"}, nil
	}

	lang := req.Language
	if strings.TrimSpace(lang) == "" && len(languages) > 0 {
		lang = languages[0]
	}
	if language.ToISO2(lang) == "" {
		lang = "en"
	}
	destPath := displayForcedSubtitlePath(req.VideoPath, lang)
	fileID := best.Attributes.Files[0].FileID
	if err := client.DownloadToFile(ctx, fileID, destPath); err != nil {
		logger.Warn("forced subtitle download failed",
			"event_type", "opensubtitles_error",
			"error_hint", err.Error(),
			"impact", "forced subtitle not available",
		)
		return ForcedLookupResult{Decision: "error:download_failed"}, nil
	}

	cleanDownloadedSubtitle(logger, destPath, "failed to write cleaned forced SRT", "forced subtitle may contain HTML tags")

	logger.Info("forced subtitle downloaded",
		"decision_type", logs.DecisionForcedSubtitle,
		"decision_result", "downloaded",
		"decision_reason", fmt.Sprintf("best match: %d downloads", best.Attributes.DownloadCount),
		"subtitle_file", destPath,
	)
	return ForcedLookupResult{Path: destPath, Decision: "downloaded"}, nil
}

// FetchRegularSubtitle searches OpenSubtitles for a full subtitle, downloads
// the best candidate, and writes it as a cleaned SRT beside VideoPath.
func FetchRegularSubtitle(
	ctx context.Context,
	logger *slog.Logger,
	cfg *config.Config,
	client *opensubtitles.Client,
	req OpenSubtitlesLookupRequest,
) (OpenSubtitlesLookupResult, error) {
	logger = logs.Default(logger)
	if strings.TrimSpace(req.EpisodeKey) != "" {
		logger = logger.With("episode_key", req.EpisodeKey)
	}
	if req.TMDBID == 0 {
		logger.Info("regular subtitle search skipped",
			"decision_type", logs.DecisionForcedSubtitleSearch,
			"decision_result", "skipped",
			"decision_reason", "no TMDB ID available",
		)
		return OpenSubtitlesLookupResult{Decision: "skipped:no_tmdb_id"}, nil
	}
	if cfg == nil || !cfg.Subtitles.OpenSubtitlesEnabled {
		logger.Info("regular subtitle search skipped",
			"decision_type", logs.DecisionForcedSubtitleSearch,
			"decision_result", "skipped",
			"decision_reason", "opensubtitles_enabled is false",
		)
		return OpenSubtitlesLookupResult{Decision: "skipped:opensubtitles_disabled"}, nil
	}
	if client == nil {
		return OpenSubtitlesLookupResult{Decision: "error:client_unavailable"}, fmt.Errorf("opensubtitles client unavailable")
	}

	languages := opensubtitlesLookupLanguages(req.Languages, cfg)

	results, err := client.Search(ctx, req.TMDBID, req.Season, req.Episode, languages)
	if err != nil {
		logger.Warn("opensubtitles search failed",
			"event_type", "opensubtitles_error",
			"error_hint", err.Error(),
			"impact", "regular subtitle lookup skipped",
		)
		return OpenSubtitlesLookupResult{Decision: "error:search_failed"}, nil
	}

	bestIndex, hasBest := rankRegularSubtitleCandidates(results, languages)
	for i, r := range results {
		result := "candidate"
		switch {
		case r.Attributes.ForeignPartsOnly || forcedSubtitleGarbageSource(r) || len(r.Attributes.Files) == 0:
			result = "skipped"
		case hasBest && i == bestIndex:
			result = "selected"
		}
		logger.Info("regular subtitle candidate",
			"decision_type", logs.DecisionSubtitleRank,
			"decision_result", result,
			"foreign_parts_only", r.Attributes.ForeignPartsOnly,
			"hearing_impaired", r.Attributes.HearingImpaired,
			"language", r.Attributes.Language,
			"downloads", r.Attributes.DownloadCount,
			"files", len(r.Attributes.Files),
			"release", r.Attributes.Release,
		)
	}

	if !hasBest {
		logger.Info("no regular subtitles found on OpenSubtitles",
			"decision_type", logs.DecisionForcedSubtitle,
			"decision_result", "none_available",
			"decision_reason", "no full subtitle results",
		)
		return OpenSubtitlesLookupResult{Decision: "none_available"}, nil
	}

	best := results[bestIndex]
	lang := req.Language
	if strings.TrimSpace(lang) == "" {
		lang = best.Attributes.Language
	}
	if language.ToISO2(lang) == "" {
		lang = "en"
	}
	destPath := displaySubtitlePath(req.VideoPath, lang)
	fileID := best.Attributes.Files[0].FileID
	if err := client.DownloadToFile(ctx, fileID, destPath); err != nil {
		logger.Warn("regular subtitle download failed",
			"event_type", "opensubtitles_error",
			"error_hint", err.Error(),
			"impact", "regular subtitle not available",
		)
		return OpenSubtitlesLookupResult{Decision: "error:download_failed"}, nil
	}

	cleanDownloadedSubtitle(logger, destPath, "failed to write cleaned regular SRT", "regular subtitle may contain HTML tags")

	logger.Info("regular subtitle downloaded",
		"decision_type", logs.DecisionForcedSubtitle,
		"decision_result", "downloaded",
		"decision_reason", fmt.Sprintf("best match: %d downloads", best.Attributes.DownloadCount),
		"subtitle_file", destPath,
	)
	return OpenSubtitlesLookupResult{Path: destPath, Decision: "downloaded"}, nil
}

func opensubtitlesLookupLanguages(requested []string, cfg *config.Config) []string {
	languages := requested
	if len(languages) == 0 && cfg != nil {
		languages = cfg.Subtitles.OpenSubtitlesLanguages
	}
	if len(languages) == 0 {
		languages = []string{"en"}
	}
	return languages
}

func cleanDownloadedSubtitle(logger *slog.Logger, path, message, impact string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	cleaned := opensubtitles.CleanSRT(string(raw))
	if writeErr := os.WriteFile(path, []byte(cleaned), 0o644); writeErr != nil {
		logger.Warn(message,
			"event_type", "file_write_error",
			"error_hint", writeErr.Error(),
			"impact", impact,
		)
	}
}

func rankRegularSubtitleCandidates(results []opensubtitles.SubtitleResult, preferredLanguages []string) (int, bool) {
	preferredLanguages = language.NormalizeList(preferredLanguages)
	if len(preferredLanguages) == 0 {
		preferredLanguages = []string{"en"}
	}

	bestIndex := -1
	for i, result := range results {
		if result.Attributes.ForeignPartsOnly || forcedSubtitleGarbageSource(result) || len(result.Attributes.Files) == 0 {
			continue
		}
		if bestIndex < 0 || regularSubtitleCandidateBetter(result, results[bestIndex], preferredLanguages) {
			bestIndex = i
		}
	}
	return bestIndex, bestIndex >= 0
}

func regularSubtitleCandidateBetter(candidate, incumbent opensubtitles.SubtitleResult, preferredLanguages []string) bool {
	candidateRank := forcedSubtitleLanguageRank(candidate.Attributes.Language, preferredLanguages)
	incumbentRank := forcedSubtitleLanguageRank(incumbent.Attributes.Language, preferredLanguages)
	if candidateRank != incumbentRank {
		return candidateRank < incumbentRank
	}
	if candidate.Attributes.HearingImpaired != incumbent.Attributes.HearingImpaired {
		return !candidate.Attributes.HearingImpaired
	}
	if candidate.Attributes.DownloadCount != incumbent.Attributes.DownloadCount {
		return candidate.Attributes.DownloadCount > incumbent.Attributes.DownloadCount
	}
	return firstForcedSubtitleFileID(candidate) < firstForcedSubtitleFileID(incumbent)
}
