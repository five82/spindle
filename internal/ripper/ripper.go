package ripper

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/makemkv"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

// Handler implements stage.Handler for disc ripping.
type Handler struct {
	cfg      *config.Config
	store    *queue.Store
	notifier *notify.Notifier
	cache    *ripcache.Store
}

// New creates a ripping handler.
func New(cfg *config.Config, store *queue.Store, notifier *notify.Notifier, cache *ripcache.Store) *Handler {
	return &Handler{cfg: cfg, store: store, notifier: notifier, cache: cache}
}

// Run executes the ripping stage.
func (h *Handler) Run(ctx context.Context, item *queue.Item) error {
	logger := stage.LoggerFromContext(ctx)
	logger.Info("ripping stage started", "event_type", "stage_start")

	env, err := stage.ParseRipSpec(item.RipSpecData)
	if err != nil {
		return err
	}

	stagingRoot, err := item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		return fmt.Errorf("staging root: %w", err)
	}
	rippedDir := filepath.Join(stagingRoot, "ripped")

	// Check rip cache first.
	if h.cache != nil && item.DiscFingerprint != "" {
		if meta, err := h.cache.Restore(item.DiscFingerprint, rippedDir); err == nil && meta != nil {
			logger.Info("rip cache hit",
				"decision_type", "rip_cache",
				"decision_result", "restored",
				"decision_reason", fmt.Sprintf("%d titles from cache", meta.TitleCount),
			)
			// Map cached files to assets.
			h.mapRippedAssets(&env, rippedDir)
			if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
				return err
			}
			return nil
		}
	}

	// Create ripped directory.
	if err := os.MkdirAll(rippedDir, 0o755); err != nil {
		return fmt.Errorf("create ripped dir: %w", err)
	}

	// Rip each title.
	for i, title := range env.Titles {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if title.Duration < h.cfg.MakeMKV.MinTitleLength {
			logger.Info("skipping short title",
				"decision_type", "title_filter",
				"decision_result", "skipped",
				"decision_reason", fmt.Sprintf("duration %ds < minimum %ds", title.Duration, h.cfg.MakeMKV.MinTitleLength),
				"title_id", title.ID,
			)
			continue
		}

		logger.Info(fmt.Sprintf("Phase %d/%d - Ripping title %d", i+1, len(env.Titles), title.ID),
			"event_type", "rip_title_start",
		)

		item.ProgressMessage = fmt.Sprintf("Phase %d/%d - Ripping title %d", i+1, len(env.Titles), title.ID)
		_ = h.store.UpdateProgress(item)

		err := makemkv.Rip(ctx, h.cfg.MakeMKV.OpticalDrive, title.ID, rippedDir,
			time.Duration(h.cfg.MakeMKV.RipTimeout)*time.Second,
			func(p makemkv.RipProgress) {
				item.ProgressPercent = p.Percent
				item.ProgressMessage = p.Message
				_ = h.store.UpdateProgress(item)
			},
		)
		if err != nil {
			return fmt.Errorf("rip title %d: %w", title.ID, err)
		}
	}

	// Map ripped files to assets.
	h.mapRippedAssets(&env, rippedDir)

	// Persist envelope.
	if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
		return err
	}

	// Cache ripped files.
	if h.cache != nil && item.DiscFingerprint != "" {
		meta := ripcache.EntryMetadata{
			Fingerprint: item.DiscFingerprint,
			DiscTitle:   item.DiscTitle,
			TitleCount:  len(env.Titles),
		}
		if err := h.cache.Register(item.DiscFingerprint, rippedDir, meta); err != nil {
			logger.Warn("rip cache write failed",
				"event_type", "cache_write_error",
				"error_hint", err.Error(),
				"impact", "no cache for next rip of this disc",
			)
			// Degraded, not fatal.
		}
	}

	// Notification.
	if h.notifier != nil {
		_ = h.notifier.Send(ctx, notify.EventRipComplete,
			"Rip Complete",
			fmt.Sprintf("Ripped %s (%d titles)", item.DiscTitle, len(env.Titles)),
		)
	}

	logger.Info("ripping stage completed", "event_type", "stage_complete")
	return nil
}

// mapRippedAssets maps ripped files in dir to envelope assets.
func (h *Handler) mapRippedAssets(env *ripspec.Envelope, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())

		// For movies, there's typically one title -> one asset.
		// For TV, map by title index to episode key.
		var episodeKey string
		if env.Metadata.MediaType == "tv" && len(env.Episodes) > 0 {
			// Simple: map files in order to episodes.
			idx := len(env.Assets.Ripped)
			if idx < len(env.Episodes) {
				episodeKey = env.Episodes[idx].Key
			}
		} else {
			episodeKey = "main"
		}

		env.Assets.AddAsset("ripped", ripspec.Asset{
			EpisodeKey: episodeKey,
			Path:       path,
			Status:     "completed",
		})
	}
}
