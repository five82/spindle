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

// ForcedLookupRequest describes a standalone or stage forced-subtitle lookup.
type ForcedLookupRequest struct {
	VideoPath  string
	TMDBID     int
	Season     int
	Episode    int
	Language   string
	Languages  []string
	EpisodeKey string
}

// ForcedLookupResult reports the forced-subtitle lookup outcome.
type ForcedLookupResult struct {
	Path     string
	Decision string
}

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

	languages := req.Languages
	if len(languages) == 0 && cfg != nil {
		languages = cfg.Subtitles.OpenSubtitlesLanguages
	}
	if len(languages) == 0 {
		languages = []string{"en"}
	}

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

	raw, err := os.ReadFile(destPath)
	if err == nil {
		cleaned := opensubtitles.CleanSRT(string(raw))
		if writeErr := os.WriteFile(destPath, []byte(cleaned), 0o644); writeErr != nil {
			logger.Warn("failed to write cleaned forced SRT",
				"event_type", "file_write_error",
				"error_hint", writeErr.Error(),
				"impact", "forced subtitle may contain HTML tags",
			)
		}
	}

	logger.Info("forced subtitle downloaded",
		"decision_type", logs.DecisionForcedSubtitle,
		"decision_result", "downloaded",
		"decision_reason", fmt.Sprintf("best match: %d downloads", best.Attributes.DownloadCount),
		"subtitle_file", destPath,
	)
	return ForcedLookupResult{Path: destPath, Decision: "downloaded"}, nil
}
