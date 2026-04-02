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

const driveAvailableMsg = "Drive is available for next disc."

// NoTitleOverride means automatic title selection based on media type.
const NoTitleOverride = -1

// Handler implements stage.Handler for disc ripping.
type Handler struct {
	cfg           *config.Config
	store         *queue.Store
	notifier      *notify.Notifier
	cache         *ripcache.Store
	monitor       *discmonitor.Monitor
	titleOverride int // NoTitleOverride = auto-select; >=0 = rip only this MakeMKV title ID
}

// New creates a ripping handler.
func New(cfg *config.Config, store *queue.Store, notifier *notify.Notifier, cache *ripcache.Store, monitor *discmonitor.Monitor, titleOverride int) *Handler {
	return &Handler{cfg: cfg, store: store, notifier: notifier, cache: cache, monitor: monitor, titleOverride: titleOverride}
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

	// Staging directories are ephemeral. Wipe any leftover state from a
	// previous run so file discovery starts clean. The rip cache is the
	// durable layer; staging has no reuse value between pipeline runs.
	if err := os.RemoveAll(stagingRoot); err != nil {
		return fmt.Errorf("reset staging dir: %w", err)
	}
	logger.Info("staging directory reset for clean rip",
		"decision_type", logs.DecisionStagingCleanup,
		"decision_result", "reset",
		"decision_reason", "ephemeral staging",
	)

	// Check rip cache first.
	if h.cache != nil && item.DiscFingerprint != "" {
		if meta, err := h.cache.Restore(item.DiscFingerprint, rippedDir, h.cacheProgressFunc(item, "Restoring from cache...")); err == nil && meta != nil {
			// TV: verify all episode files are present in cache.
			cacheUsable := true
			if len(env.Episodes) > 0 {
				if missing := cacheHasAllEpisodeFiles(&env, rippedDir); len(missing) > 0 {
					cacheUsable = false
					logger.Info("rip cache incomplete",
						"decision_type", logs.DecisionRipCache,
						"decision_result", "incomplete",
						"decision_reason", "missing_episode_files",
						"missing_episodes", strings.Join(missing, ","),
						"missing_count", len(missing),
					)
				}
			}

			if cacheUsable {
				logger.Info("rip cache hit",
					"decision_type", logs.DecisionRipCache,
					"decision_result", "restored",
					"decision_reason", fmt.Sprintf("%d titles from cache", meta.TitleCount),
				)
				if h.notifier != nil {
					msg := fmt.Sprintf("%s (%d titles from cache)", item.DiscTitle, meta.TitleCount)
					msg += "\n" + driveAvailableMsg
					msg += queue.FormatAlsoProcessing(h.store, item.ID)
					_ = h.notifier.Send(ctx, notify.EventRipCacheHit,
						"Rip Cache Hit",
						msg,
					)
				}
				// Map cached files to assets via title ID parsing.
				if err := h.mapAndValidateAssets(ctx, logger, &env, item, rippedDir); err != nil {
					return err
				}
				// Restore titles from cached envelope when identification
				// used the disc ID cache fast-path (no MakeMKV scan).
				if len(env.Titles) == 0 && meta.RipSpecData != "" {
					if cachedEnv, err := ripspec.Parse(meta.RipSpecData); err == nil && len(cachedEnv.Titles) > 0 {
						env.Titles = cachedEnv.Titles
						logger.Info("titles restored from rip cache",
							"decision_type", logs.DecisionRipCacheTitles,
							"decision_result", "restored",
							"decision_reason", fmt.Sprintf("%d titles from cached envelope", len(cachedEnv.Titles)),
						)
					}
				}
				if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
					return err
				}
				return nil
			}
			// Cache incomplete — fall through to fresh rip.
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
	targets, err := h.selectRipTargets(logger, &env)
	if err != nil {
		return err
	}
	rippedCount := len(targets)

	if h.notifier != nil && len(targets) > 0 {
		msg := fmt.Sprintf("Ripping %s (%d titles)", item.DiscTitle, len(targets))
		msg += queue.FormatAlsoProcessing(h.store, item.ID)
		_ = h.notifier.Send(ctx, notify.EventRipStarted,
			"Rip Started",
			msg,
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

	// Map ripped files to assets and validate.
	if err := h.mapAndValidateAssets(ctx, logger, &env, item, rippedDir); err != nil {
		return err
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
		if err := h.cache.Register(item.DiscFingerprint, rippedDir, h.cacheProgressFunc(item, "Caching rip...")); err != nil {
			logger.Warn("rip cache write failed",
				"event_type", "cache_write_error",
				"error_hint", err.Error(),
				"impact", "no cache for next rip of this disc",
			)
			// Degraded, not fatal.
		} else if err := h.cache.WriteMetadata(item.DiscFingerprint, meta); err != nil {
			logger.Warn("rip cache metadata write failed",
				"event_type", "cache_metadata_error",
				"error_hint", err.Error(),
				"impact", "cache entry may not be reused",
			)
		}
	}

	// Notification.
	if h.notifier != nil {
		msg := fmt.Sprintf("Ripped %s (%d titles)", item.DiscTitle, rippedCount)
		msg += "\n" + driveAvailableMsg
		msg += queue.FormatAlsoProcessing(h.store, item.ID)
		_ = h.notifier.Send(ctx, notify.EventRipComplete,
			"Rip Complete",
			msg,
		)
	}

	logger.Info("ripping stage completed", "event_type", "stage_complete", "stage", "ripping")
	return nil
}

// selectRipTargets determines which titles to rip based on media type.
func (h *Handler) selectRipTargets(logger *slog.Logger, env *ripspec.Envelope) ([]ripspec.Title, error) {
	// User-specified title override bypasses media-type selection.
	if h.titleOverride >= 0 {
		for _, t := range env.Titles {
			if t.ID == h.titleOverride {
				logger.Info("title override selected",
					"decision_type", logs.DecisionTitleSelection,
					"decision_result", fmt.Sprintf("title %d (%ds)", t.ID, t.Duration),
					"decision_reason", "user-specified --title override",
				)
				return []ripspec.Title{t}, nil
			}
		}
		var ids []string
		for _, t := range env.Titles {
			ids = append(ids, fmt.Sprintf("%d (%ds)", t.ID, t.Duration))
		}
		return nil, fmt.Errorf("title %d not found on disc; available titles: %s",
			h.titleOverride, strings.Join(ids, ", "))
	}

	switch env.Metadata.MediaType {
	case "movie":
		selection, ok, candidates, rejects := PrimaryTitleDecisionSummary(env.Titles)
		if ok {
			attrs := []any{
				"decision_type", logs.DecisionTitleSelection,
				"decision_result", fmt.Sprintf("title %d (%ds)", selection.ID, selection.Duration),
				"decision_reason", "primary_title_selector",
				"title_id", selection.ID,
				"duration_seconds", selection.Duration,
				"playlist", strings.TrimSpace(selection.Playlist),
				"candidate_count", len(candidates),
				"rejected_count", len(rejects),
			}
			for i, c := range candidates {
				attrs = append(attrs, fmt.Sprintf("candidate_%d", i+1), c)
			}
			for i, r := range rejects {
				attrs = append(attrs, fmt.Sprintf("rejected_%d", i+1), r)
			}
			logger.Info("primary title decision", attrs...)
			return []ripspec.Title{selection}, nil
		}
		logger.Warn("no titles above minimum duration for movie",
			"event_type", "title_selection_empty",
			"error_hint", "no valid candidates after filtering",
			"impact", "no titles will be ripped",
		)
		return nil, fmt.Errorf("no titles above minimum duration for movie (%d titles in envelope)", len(env.Titles))

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
		return targets, nil

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
		return targets, nil
	}
}

// mapAndValidateAssets maps ripped files to envelope assets and validates them.
// For TV content, uses title ID parsing from filenames. For movies, scans the
// directory for the first MKV. Validates all mapped assets with ffprobe.
func (h *Handler) mapAndValidateAssets(ctx context.Context, logger *slog.Logger, env *ripspec.Envelope, item *queue.Item, dir string) error {
	if env.Metadata.MediaType == "tv" && len(env.Episodes) > 0 {
		logger.Info("asset mapping strategy selected",
			"decision_type", logs.DecisionAssetMapping,
			"decision_result", "title_id_scan",
			"decision_reason", fmt.Sprintf("media_type=%s episodes=%d", env.Metadata.MediaType, len(env.Episodes)),
		)
		result := assignEpisodeAssets(env, dir, logger)
		if result.Assigned == 0 {
			return fmt.Errorf("episode asset mapping: zero matches (expected %d episodes)", len(env.Episodes))
		}
		if len(result.Missing) > 0 {
			reason := fmt.Sprintf("missing %d episode(s): %s", len(result.Missing), strings.Join(result.Missing, ", "))
			item.AppendReviewReason(reason)
			logger.Warn("partial episode asset mapping",
				"event_type", "episode_files_missing",
				"error_hint", "check MakeMKV output for failed titles",
				"impact", "some episodes will be missing from final output",
				"assigned", result.Assigned,
				"missing_count", len(result.Missing),
				"missing_episodes", strings.Join(result.Missing, ","),
			)
		}
	} else {
		// Movie or unknown: scan directory for MKV files.
		logger.Info("asset mapping strategy selected",
			"decision_type", logs.DecisionAssetMapping,
			"decision_result", "directory_scan",
			"decision_reason", fmt.Sprintf("media_type=%s", env.Metadata.MediaType),
		)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("asset mapping: read dir: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			env.Assets.AddAsset("ripped", ripspec.Asset{
				EpisodeKey: "main",
				Path:       filepath.Join(dir, entry.Name()),
				Status:     "completed",
			})
		}
	}

	if n := len(env.Assets.Ripped); n > 0 {
		item.RippedFile = env.Assets.Ripped[n-1].Path
	}

	// Validate all ripped artifacts with ffprobe.
	visited := make(map[string]struct{})
	for _, asset := range env.Assets.Ripped {
		if _, seen := visited[asset.Path]; seen {
			continue
		}
		visited[asset.Path] = struct{}{}
		if err := h.validateRippedArtifact(ctx, asset.Path); err != nil {
			return fmt.Errorf("ripped artifact invalid (%s): %w", filepath.Base(asset.Path), err)
		}
	}

	return nil
}

// cacheProgressFunc returns a throttled progress callback for cache operations.
func (h *Handler) cacheProgressFunc(item *queue.Item, message string) ripcache.ProgressFunc {
	var lastPush time.Time
	return func(p ripcache.CopyProgress) {
		now := time.Now()
		if now.Sub(lastPush) < 2*time.Second {
			return
		}
		lastPush = now
		item.ProgressPercent = float64(p.BytesCopied) / float64(p.TotalBytes) * 100
		item.ProgressBytesCopied = p.BytesCopied
		item.ProgressTotalBytes = p.TotalBytes
		item.ProgressMessage = message
		_ = h.store.UpdateProgress(item)
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
