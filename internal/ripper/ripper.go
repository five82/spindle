package ripper

import (
	"context"
	"fmt"
	"log/slog"
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
			// Map cached files to assets (no titleFileMap for cache path).
			h.mapRippedAssets(&env, rippedDir, nil)
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

	// Select titles to rip based on media type.
	targets := h.selectRipTargets(logger, &env)

	// Rip selected titles, tracking TitleID -> file path mapping.
	titleFileMap := make(map[int]string, len(targets)) // titleID -> ripped file path
	for i, title := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		logger.Info(fmt.Sprintf("Phase %d/%d - Ripping title %d", i+1, len(targets), title.ID),
			"event_type", "rip_title_start",
		)

		item.ProgressMessage = fmt.Sprintf("Phase %d/%d - Ripping title %d", i+1, len(targets), title.ID)
		_ = h.store.UpdateProgress(item)

		// Snapshot files before rip to detect the new file.
		before := listMKVFiles(rippedDir)

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

		// Find the new file produced by this rip.
		after := listMKVFiles(rippedDir)
		newFile := findNewFile(before, after)
		if newFile != "" {
			titleFileMap[title.ID] = newFile
		}
	}

	// Map ripped files to assets using TitleID mapping.
	h.mapRippedAssets(&env, rippedDir, titleFileMap)

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

// selectRipTargets determines which titles to rip based on media type.
func (h *Handler) selectRipTargets(logger *slog.Logger, env *ripspec.Envelope) []ripspec.Title {
	switch env.Metadata.MediaType {
	case "movie":
		// Select the single longest title above MinTitleLength.
		var best *ripspec.Title
		for i := range env.Titles {
			t := &env.Titles[i]
			if t.Duration < h.cfg.MakeMKV.MinTitleLength {
				continue
			}
			if best == nil || t.Duration > best.Duration {
				best = t
			}
		}
		if best != nil {
			logger.Info("primary title selected for movie",
				"decision_type", "title_selection",
				"decision_result", fmt.Sprintf("title %d (%ds)", best.ID, best.Duration),
				"decision_reason", "longest title above minimum duration",
			)
			return []ripspec.Title{*best}
		}
		logger.Warn("no titles above minimum duration for movie",
			"event_type", "title_selection_empty",
			"error_hint", fmt.Sprintf("min_title_length=%d", h.cfg.MakeMKV.MinTitleLength),
			"impact", "no titles will be ripped",
		)
		return nil

	case "tv":
		// Rip only titles referenced by episodes.
		needed := make(map[int]bool)
		for _, ep := range env.Episodes {
			needed[ep.TitleID] = true
		}
		var targets []ripspec.Title
		for _, t := range env.Titles {
			if needed[t.ID] {
				targets = append(targets, t)
			}
		}
		logger.Info("TV titles selected for ripping",
			"decision_type", "title_selection",
			"decision_result", fmt.Sprintf("%d titles from %d episodes", len(targets), len(env.Episodes)),
			"decision_reason", "episode-referenced titles only",
		)
		return targets

	default:
		// Unknown/fallback: rip all titles above MinTitleLength.
		var targets []ripspec.Title
		for _, t := range env.Titles {
			if t.Duration >= h.cfg.MakeMKV.MinTitleLength {
				targets = append(targets, t)
			}
		}
		logger.Info("fallback title selection",
			"decision_type", "title_selection",
			"decision_result", fmt.Sprintf("%d titles above minimum duration", len(targets)),
			"decision_reason", "unknown media type, using duration filter",
		)
		return targets
	}
}

// mapRippedAssets maps ripped files to envelope assets using TitleID mapping.
// titleFileMap maps titleID -> file path from the rip loop. When nil (cache
// restore path), falls back to directory scanning with index-based mapping.
func (h *Handler) mapRippedAssets(env *ripspec.Envelope, dir string, titleFileMap map[int]string) {
	if env.Metadata.MediaType == "tv" && len(env.Episodes) > 0 && titleFileMap != nil {
		// TV with TitleID mapping: connect files to episodes via TitleID.
		for _, ep := range env.Episodes {
			path, ok := titleFileMap[ep.TitleID]
			if !ok {
				continue
			}
			env.Assets.AddAsset("ripped", ripspec.Asset{
				EpisodeKey: ep.Key,
				TitleID:    ep.TitleID,
				Path:       path,
				Status:     "completed",
			})
		}
		return
	}

	// Movie, unknown, or cache restore: scan directory.
	// os.ReadDir returns entries sorted by filename.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())

		var episodeKey string
		if env.Metadata.MediaType == "tv" && len(env.Episodes) > 0 {
			// Cache restore fallback: map files in order to episodes.
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

// listMKVFiles returns a set of .mkv file paths in dir.
func listMKVFiles(dir string) map[string]bool {
	files := make(map[string]bool)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".mkv" {
			files[filepath.Join(dir, e.Name())] = true
		}
	}
	return files
}

// findNewFile returns the first file in after that is not in before.
func findNewFile(before, after map[string]bool) string {
	for f := range after {
		if !before[f] {
			return f
		}
	}
	return ""
}
