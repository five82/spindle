package identification

import (
	"context"
	"fmt"

	"log/slog"

	"spindle/internal/disc"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
)

func (i *Identifier) performTMDBSearch(ctx context.Context, logger *slog.Logger, title string, opts tmdb.SearchOptions, hint mediaKind) (*tmdb.Response, searchMode, error) {
	orders := searchOrderForHint(hint)
	var lastErr error
	var lastResp *tmdb.Response
	modeUsed := searchModeMovie
	modeLabels := make([]string, 0, len(orders))
	for _, mode := range orders {
		modeLabels = append(modeLabels, string(mode))
	}
	attrs := []logging.Attr{
		logging.String("query", title),
		logging.Int("mode_count", len(modeLabels)),
		logging.Int("year", opts.Year),
		logging.String("studio", opts.Studio),
		logging.Int("runtime_minutes", opts.Runtime),
		logging.String("runtime_range", fmt.Sprintf("%d-%d", opts.Runtime-10, opts.Runtime+10)),
		logging.String(logging.FieldEventType, "decision_summary"),
		logging.String(logging.FieldDecisionType, "tmdb_search"),
		logging.String("decision_result", "planned"),
		logging.String("decision_reason", fmt.Sprintf("media_hint=%s", hint.String())),
		logging.String("decision_options", "search, skip"),
	}
	for idx, modeLabel := range modeLabels {
		attrs = append(attrs, logging.String(fmt.Sprintf("mode_%d", idx+1), modeLabel))
	}
	logger.Debug("tmdb search plan", logging.Args(attrs...)...)
	for _, mode := range orders {
		logger.Debug("tmdb query details",
			logging.String("query", title),
			logging.String("mode", string(mode)),
			logging.Int("year", opts.Year),
			logging.String("studio", opts.Studio),
			logging.Int("runtime_minutes", opts.Runtime),
			logging.String("runtime_range", fmt.Sprintf("%d-%d", opts.Runtime-10, opts.Runtime+10)))
		resp, err := i.tmdb.search(ctx, title, opts, mode)
		if err != nil {
			lastErr = err
			logger.Warn("tmdb search attempt failed",
				logging.String("mode", string(mode)),
				logging.String("query", title),
				logging.Error(err),
				logging.String("error_message", "TMDB search attempt failed"),
				logging.String(logging.FieldEventType, "tmdb_search_failed"),
				logging.String(logging.FieldErrorHint, "verify TMDB credentials and retry"),
				logging.String("impact", "retrying next search mode"),
			)
			continue
		}
		if resp != nil {
			lastResp = resp
			modeUsed = mode
			if len(resp.Results) > 0 {
				return resp, mode, nil
			}
		}
	}
	return lastResp, modeUsed, lastErr
}

func searchOrderForHint(h mediaKind) []searchMode {
	switch h {
	case mediaKindTV:
		return []searchMode{searchModeTV, searchModeMovie, searchModeMulti}
	case mediaKindMovie:
		return []searchMode{searchModeMovie, searchModeTV, searchModeMulti}
	default:
		return []searchMode{searchModeMovie, searchModeTV, searchModeMulti}
	}
}

type episodeAnnotation struct {
	Season  int
	Episode int
	Title   string
	Air     string
}

func (i *Identifier) annotateEpisodes(ctx context.Context, logger *slog.Logger, tmdbID int64, seasonNumber int, discNumber int, scanResult *disc.ScanResult) (map[int]episodeAnnotation, []int) {
	if tmdbID == 0 || seasonNumber <= 0 || scanResult == nil || len(scanResult.Titles) == 0 {
		return nil, nil
	}
	if i.tmdbInfo == nil {
		logger.Warn("tmdb season lookup unavailable",
			logging.String("reason", "tmdb client missing"),
			logging.String(logging.FieldEventType, "tmdb_season_lookup_unavailable"),
			logging.String(logging.FieldErrorHint, "check TMDB client initialization"),
		)
		return nil, nil
	}
	season, err := i.tmdbInfo.GetSeasonDetails(ctx, tmdbID, seasonNumber)
	if err != nil {
		logger.Warn("tmdb season lookup failed",
			logging.Int64("tmdb_id", tmdbID),
			logging.Int("season", seasonNumber),
			logging.Error(err),
			logging.String("error_message", "Failed to fetch TMDB season details"),
			logging.String(logging.FieldEventType, "tmdb_season_lookup_failed"),
			logging.String(logging.FieldErrorHint, "verify TMDB connectivity and API key"),
		)
		return nil, nil
	}
	if season == nil || len(season.Episodes) == 0 {
		logger.Debug("tmdb season lookup returned no episodes",
			logging.Int64("tmdb_id", tmdbID),
			logging.Int("season", seasonNumber),
			logging.String("reason", "season has no episodes"))
		return nil, nil
	}
	matches, numbers := mapEpisodesToTitles(scanResult.Titles, season.Episodes, discNumber)
	return matches, numbers
}
