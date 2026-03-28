package ripper

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/discmonitor"
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
	monitor  *discmonitor.Monitor
}

// New creates a ripping handler.
func New(cfg *config.Config, store *queue.Store, notifier *notify.Notifier, cache *ripcache.Store, monitor *discmonitor.Monitor) *Handler {
	return &Handler{cfg: cfg, store: store, notifier: notifier, cache: cache, monitor: monitor}
}

// Run executes the ripping stage.
func (h *Handler) Run(ctx context.Context, item *queue.Item) error {
	logger := stage.LoggerFromContext(ctx)
	logger.Info("ripping stage started", "event_type", "stage_start", "stage", "ripping")

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
				"decision_type", logs.DecisionRipCache,
				"decision_result", "restored",
				"decision_reason", fmt.Sprintf("%d titles from cache", meta.TitleCount),
			)
			if h.notifier != nil {
				_ = h.notifier.Send(ctx, notify.EventRipCacheHit,
					"Rip Cache Hit",
					fmt.Sprintf("%s (%d titles from cache)", item.DiscTitle, meta.TitleCount),
				)
			}
			// Map cached files to assets (no titleFileMap for cache path).
			h.mapRippedAssets(logger, &env, rippedDir, nil)
			if n := len(env.Assets.Ripped); n > 0 {
				item.RippedFile = env.Assets.Ripped[n-1].Path
			}
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

	// Pause disc monitor during ripping to prevent polling interference.
	if h.monitor != nil {
		h.monitor.PauseDisc()
		logger.Info("disc monitor paused for ripping",
			"decision_type", logs.DecisionDiscMonitorControl,
			"decision_result", "paused",
			"decision_reason", "ripping requires exclusive disc access",
		)
		defer func() {
			h.monitor.ResumeDisc()
			logger.Info("disc monitor resumed after ripping",
				"decision_type", logs.DecisionDiscMonitorControl,
				"decision_result", "resumed",
				"decision_reason", "ripping complete, restoring disc polling",
			)
		}()
	}

	// Drive readiness check for /dev/ device paths.
	if strings.HasPrefix(h.cfg.MakeMKV.OpticalDrive, "/dev/") {
		if err := discmonitor.WaitForReady(ctx, h.cfg.MakeMKV.OpticalDrive, logger); err != nil {
			return fmt.Errorf("drive readiness: %w", err)
		}
	}

	// Ensure MakeMKV settings are configured for track selection.
	if err := makemkv.EnsureSettings(logger); err != nil {
		logger.Warn("MakeMKV settings configuration failed",
			"event_type", "makemkv_settings_warning",
			"error_hint", err.Error(),
			"impact", "ripping continues with existing MakeMKV settings",
		)
	}

	// Select titles to rip based on media type.
	targets := h.selectRipTargets(logger, &env)
	rippedCount := len(targets)

	if h.notifier != nil && len(targets) > 0 {
		_ = h.notifier.Send(ctx, notify.EventRipStarted,
			"Rip Started",
			fmt.Sprintf("Ripping %s (%d titles)", item.DiscTitle, len(targets)),
		)
	}

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
		if err := h.store.UpdateProgress(item); err != nil {
			logger.Warn("progress persistence failed",
				"event_type", "progress_persist_failed",
				"error_hint", "rip progress message not persisted",
				"impact", "rip progress not reflected in queue",
				"error", err,
			)
		}

		// Snapshot files before rip to detect the new file.
		before := listMKVFiles(rippedDir)

		err := makemkv.Rip(ctx, h.cfg.MakeMKV.OpticalDrive, title.ID, rippedDir,
			time.Duration(h.cfg.MakeMKV.RipTimeout)*time.Second,
			h.cfg.MakeMKV.MinTitleLength,
			func(p makemkv.RipProgress) {
				item.ProgressPercent = p.Percent
				item.ProgressMessage = p.Message
				_ = h.store.UpdateProgress(item)
			}, logger,
		)
		if err != nil {
			return fmt.Errorf("rip title %d: %w", title.ID, err)
		}

		// Find the new file produced by this rip.
		after := listMKVFiles(rippedDir)
		newFile := findNewFile(before, after)
		if newFile != "" {
			titleFileMap[title.ID] = newFile
			logger.Info("title rip completed",
				"decision_type", logs.DecisionTitleRip,
				"decision_result", "completed",
				"decision_reason", fmt.Sprintf("title_id=%d file=%s", title.ID, newFile),
			)
		} else {
			logger.Info("title rip completed but no new file detected",
				"decision_type", logs.DecisionFileDiscovery,
				"decision_result", "not_found",
				"decision_reason", fmt.Sprintf("title_id=%d", title.ID),
			)
		}
	}

	// Map ripped files to assets using TitleID mapping.
	h.mapRippedAssets(logger, &env, rippedDir, titleFileMap)
	if n := len(env.Assets.Ripped); n > 0 {
		item.RippedFile = env.Assets.Ripped[n-1].Path
	}

	// Persist envelope.
	if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
		return err
	}

	// Cache ripped files.
	if h.cache != nil && item.DiscFingerprint != "" {
		var totalBytes int64
		if dirEntries, err := os.ReadDir(rippedDir); err == nil {
			for _, de := range dirEntries {
				if info, err := de.Info(); err == nil {
					totalBytes += info.Size()
				}
			}
		}
		meta := ripcache.EntryMetadata{
			Version:      1,
			Fingerprint:  item.DiscFingerprint,
			DiscTitle:    item.DiscTitle,
			CachedAt:     time.Now(),
			TitleCount:   rippedCount,
			TotalBytes:   totalBytes,
			RipSpecData:  item.RipSpecData,
			MetadataJSON: item.MetadataJSON,
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
			fmt.Sprintf("Ripped %s (%d titles)", item.DiscTitle, rippedCount),
		)
	}

	logger.Info("ripping stage completed", "event_type", "stage_complete", "stage", "ripping")
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
				logger.Info("rip candidate below minimum duration",
					"decision_type", logs.DecisionTrackSelect,
					"decision_result", "skipped",
					"title_id", t.ID,
					"duration_s", t.Duration,
					"min_title_length_s", h.cfg.MakeMKV.MinTitleLength,
				)
				continue
			}
			logger.Debug("rip candidate evaluated",
				"decision_type", logs.DecisionTrackSelect,
				"decision_result", "candidate",
				"title_id", t.ID,
				"duration_s", t.Duration,
			)
			if best == nil || t.Duration > best.Duration {
				best = t
			}
		}
		if best != nil {
			logger.Info("primary title selected for movie",
				"decision_type", logs.DecisionTitleSelection,
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
				logger.Debug("rip candidate evaluated",
					"decision_type", logs.DecisionTrackSelect,
					"decision_result", "selected",
					"title_id", t.ID,
					"duration_s", t.Duration,
					"episode_referenced", true,
				)
				targets = append(targets, t)
			} else {
				logger.Debug("rip candidate evaluated",
					"decision_type", logs.DecisionTrackSelect,
					"decision_result", "skipped",
					"title_id", t.ID,
					"duration_s", t.Duration,
					"episode_referenced", false,
				)
			}
		}
		logger.Info("TV titles selected for ripping",
			"decision_type", logs.DecisionTitleSelection,
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
			"decision_type", logs.DecisionTitleSelection,
			"decision_result", fmt.Sprintf("%d titles above minimum duration", len(targets)),
			"decision_reason", "unknown media type, using duration filter",
		)
		return targets
	}
}

// mapRippedAssets maps ripped files to envelope assets using TitleID mapping.
// titleFileMap maps titleID -> file path from the rip loop. When nil (cache
// restore path), falls back to directory scanning with index-based mapping.
func (h *Handler) mapRippedAssets(logger *slog.Logger, env *ripspec.Envelope, dir string, titleFileMap map[int]string) {
	if env.Metadata.MediaType == "tv" && len(env.Episodes) > 0 && titleFileMap != nil {
		logger.Info("asset mapping strategy selected",
			"decision_type", logs.DecisionAssetMapping,
			"decision_result", "title_file_map",
			"decision_reason", fmt.Sprintf("media_type=%s episodes=%d", env.Metadata.MediaType, len(env.Episodes)),
		)
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
	logger.Info("asset mapping strategy selected",
		"decision_type", logs.DecisionAssetMapping,
		"decision_result", "directory_scan",
		"decision_reason", fmt.Sprintf("media_type=%s episodes=%d", env.Metadata.MediaType, len(env.Episodes)),
	)
	// os.ReadDir returns entries sorted by filename.
	entries, err := os.ReadDir(dir)
	if err != nil {
		logger.Warn("asset mapping directory read failed",
			"event_type", "asset_mapping_readdir_failed",
			"error_hint", err.Error(),
			"impact", "ripped assets may not be tracked",
		)
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
