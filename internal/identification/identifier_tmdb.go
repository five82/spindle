package identification

import (
	"context"
	"fmt"

	"log/slog"

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
	logger.Info("tmdb search mode decision", logging.Args(attrs...)...)
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
				logging.String(logging.FieldImpact, "retrying next search mode"),
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
	if h == mediaKindTV {
		return []searchMode{searchModeTV, searchModeMovie, searchModeMulti}
	}
	// Default order for movies and unknown media types
	return []searchMode{searchModeMovie, searchModeTV, searchModeMulti}
}

type episodeAnnotation struct {
	Season  int
	Episode int
	Title   string
	Air     string
}
