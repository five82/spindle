package encoding

import (
	"context"

	"log/slog"

	"spindle/internal/commentaryid"
	"spindle/internal/config"
	"spindle/internal/queue"
)

type commentaryRefiner struct {
	cfg    *config.Config
	store  *queue.Store
	logger *slog.Logger
	core   *commentaryid.Detector
}

func newCommentaryRefiner(cfg *config.Config, store *queue.Store, logger *slog.Logger, core *commentaryid.Detector) *commentaryRefiner {
	return &commentaryRefiner{cfg: cfg, store: store, logger: logger, core: core}
}

func (r *commentaryRefiner) SetLogger(logger *slog.Logger) {
	r.logger = logger
	if r.core != nil {
		r.core.SetLogger(logger)
	}
}

func (r *commentaryRefiner) Refine(ctx context.Context, item *queue.Item, sourcePath, stagingRoot, label string, episodeIndex, episodeCount int) (string, error) {
	if r == nil || r.cfg == nil || r.core == nil || !r.cfg.CommentaryDetection.Enabled {
		return sourcePath, nil
	}
	return refineCommentaryTracks(ctx, r.cfg, r.store, r.core, item, sourcePath, stagingRoot, label, episodeIndex, episodeCount, r.logger)
}
