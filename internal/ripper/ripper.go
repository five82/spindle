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
	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/makemkv"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

const driveAvailableMsg = "Drive is available for next disc."

const ripProgressLogInterval = 3 * time.Minute

// NoTitleOverride means automatic title selection based on media type.
const NoTitleOverride = -1

// Handler implements stage.Handler for disc ripping.
type Handler struct {
	cfg           *config.Config
	notifier      *notify.Notifier
	cache         *ripcache.Store
	monitor       *discmonitor.Monitor
	titleOverride int // NoTitleOverride = auto-select; >=0 = rip only this MakeMKV title ID
}

// New creates a ripping handler.
func New(cfg *config.Config, notifier *notify.Notifier, cache *ripcache.Store, monitor *discmonitor.Monitor, titleOverride int) *Handler {
	return &Handler{cfg: cfg, notifier: notifier, cache: cache, monitor: monitor, titleOverride: titleOverride}
}

// Run executes the ripping stage.
func (h *Handler) Run(ctx context.Context, sess *stage.Session) error {
	logger := sess.Logger
	logger.Info("ripping stage started", "event_type", "stage_start", "stage", "ripping")

	rippedDir, err := h.prepareRipStaging(sess)
	if err != nil {
		return err
	}

	if restored, err := h.restoreFromRipCache(ctx, sess, rippedDir); restored || err != nil {
		if err == nil {
			logger.Info("ripping stage completed",
				"event_type", "stage_complete",
				"stage", "ripping",
				"rip_cache_restored", true,
			)
		}
		return err
	}

	cleanup, err := h.prepareFreshRip(ctx, sess, rippedDir)
	if err != nil {
		return err
	}
	defer cleanup()

	targets, err := h.selectRipTargets(logger, sess.Env)
	if err != nil {
		return err
	}
	logger.Info("ripping plan",
		"event_type", "ripping_plan",
		"titles", len(targets),
		"media_type", sess.Env.Metadata.MediaType,
	)

	if err := h.ripTitles(ctx, sess, rippedDir, targets); err != nil {
		return err
	}
	_ = sess.ClearActiveEpisode()

	if err := h.mapAndValidateAssets(ctx, logger, sess, rippedDir, nil); err != nil {
		return err
	}
	if err := sess.Save(); err != nil {
		return err
	}

	h.cacheFreshRip(logger, sess, rippedDir, len(targets))
	h.notifyRipComplete(ctx, logger, sess, len(targets))

	logger.Info("ripping stage completed",
		"event_type", "stage_complete",
		"stage", "ripping",
		"titles_ripped", len(targets),
	)
	return nil
}

func (h *Handler) prepareRipStaging(sess *stage.Session) (string, error) {
	item := sess.Item
	logger := sess.Logger

	stagingRoot, err := item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		return "", fmt.Errorf("staging root: %w", err)
	}
	rippedDir := filepath.Join(stagingRoot, "ripped")

	// Staging directories are ephemeral. Wipe any leftover state from a
	// previous run so file discovery starts clean. The rip cache is the
	// durable layer; staging has no reuse value between pipeline runs.
	if err := os.RemoveAll(stagingRoot); err != nil {
		return "", fmt.Errorf("reset staging dir: %w", err)
	}
	logger.Info("staging directory reset for clean rip",
		"decision_type", logs.DecisionStagingCleanup,
		"decision_result", "reset",
		"decision_reason", "ephemeral staging",
	)
	return rippedDir, nil
}

func (h *Handler) restoreFromRipCache(ctx context.Context, sess *stage.Session, rippedDir string) (bool, error) {
	item := sess.Item
	logger := sess.Logger
	env := sess.Env
	if h.cache == nil || item.DiscFingerprint == "" {
		return false, nil
	}

	meta, err := h.cache.Restore(item.DiscFingerprint, rippedDir, h.cacheProgressFunc(sess, "Restoring from cache..."))
	if err != nil || meta == nil {
		return false, nil
	}

	// TV: verify all episode files are present in cache. The scan result is
	// reused below via mapAndValidateAssets to avoid a second ReadDir on the
	// same directory.
	cacheUsable := true
	var cachedTitleFiles map[int]string
	if len(env.Episodes) > 0 {
		files, missing := cacheHasAllEpisodeFiles(env, rippedDir)
		cachedTitleFiles = files
		if len(missing) > 0 {
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
	if !cacheUsable {
		return false, nil
	}

	logger.Info("rip cache hit",
		"decision_type", logs.DecisionRipCache,
		"decision_result", "restored",
		"decision_reason", fmt.Sprintf("%d titles from cache", meta.TitleCount),
	)
	msg := fmt.Sprintf("%s (%d titles from cache)", item.DisplayTitle(), meta.TitleCount)
	msg += "\n" + driveAvailableMsg
	msg += queue.FormatAlsoProcessing(sess.Store, item.ID)
	_ = notify.SendLogged(ctx, h.notifier, logger, notify.EventRipCacheHit,
		"Rip Cache Hit: "+item.DisplayTitle(),
		msg,
		"item_id", item.ID,
	)

	if err := h.mapAndValidateAssets(ctx, logger, sess, rippedDir, cachedTitleFiles); err != nil {
		return true, err
	}
	h.restoreTitlesFromCachedEnvelope(logger, env, meta.RipSpecData)
	if err := sess.Save(); err != nil {
		return true, err
	}
	return true, nil
}

func (h *Handler) restoreTitlesFromCachedEnvelope(logger *slog.Logger, env *ripspec.Envelope, ripSpecData string) {
	// Restore titles from cached envelope when identification used the disc ID
	// cache fast-path (no MakeMKV scan).
	if len(env.Titles) != 0 || ripSpecData == "" {
		return
	}
	cachedEnv, err := ripspec.Parse(ripSpecData)
	if err != nil || len(cachedEnv.Titles) == 0 {
		return
	}
	env.Titles = cachedEnv.Titles
	logger.Info("titles restored from rip cache",
		"decision_type", logs.DecisionRipCacheTitles,
		"decision_result", "restored",
		"decision_reason", fmt.Sprintf("%d titles from cached envelope", len(cachedEnv.Titles)),
	)
}

func (h *Handler) prepareFreshRip(ctx context.Context, sess *stage.Session, rippedDir string) (func(), error) {
	logger := sess.Logger
	noop := func() {}

	if err := os.MkdirAll(rippedDir, 0o755); err != nil {
		return noop, fmt.Errorf("create ripped dir: %w", err)
	}

	cleanup := noop
	if h.monitor != nil {
		h.monitor.PauseDisc()
		logger.Info("disc monitor paused for ripping",
			"decision_type", logs.DecisionDiscMonitorControl,
			"decision_result", "paused",
			"decision_reason", "ripping requires exclusive disc access",
		)
		cleanup = func() {
			h.monitor.ResumeDisc()
			logger.Info("disc monitor resumed after ripping",
				"decision_type", logs.DecisionDiscMonitorControl,
				"decision_result", "resumed",
				"decision_reason", "ripping complete, restoring disc polling",
			)
		}
	}

	if strings.HasPrefix(h.cfg.MakeMKV.OpticalDrive, "/dev/") {
		if err := discmonitor.WaitForReady(ctx, h.cfg.MakeMKV.OpticalDrive, logger); err != nil {
			cleanup()
			return noop, fmt.Errorf("drive readiness: %w", err)
		}
	}

	if err := makemkv.EnsureSettings(logger); err != nil {
		logger.Warn("MakeMKV settings configuration failed",
			"event_type", "makemkv_settings_warning",
			"error_hint", err.Error(),
			"impact", "ripping continues with existing MakeMKV settings",
		)
	}
	return cleanup, nil
}

func (h *Handler) ripTitles(ctx context.Context, sess *stage.Session, rippedDir string, targets []ripspec.Title) error {
	titleEpisodeKey := make(map[int]string, len(sess.Env.Episodes))
	for _, ep := range sess.Env.Episodes {
		titleEpisodeKey[ep.TitleID] = ep.Key
	}

	// Rip selected titles one by one, persisting per-title progress so external
	// consumers can show both aggregate stage progress and completed episode
	// counts while the stage is still running.
	for i, title := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := h.ripTitle(ctx, sess, rippedDir, title, i, len(targets), titleEpisodeKey[title.ID]); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) ripTitle(ctx context.Context, sess *stage.Session, rippedDir string, title ripspec.Title, index, total int, episodeKey string) error {
	item := sess.Item
	logger := sess.Logger

	logger.Info(fmt.Sprintf("Phase %d/%d - Ripping title %d", index+1, total, title.ID),
		"event_type", "rip_title_start",
	)

	if err := sess.Progress(overallRipPercent(index, total, 0), fmt.Sprintf("Phase %d/%d - Ripping title %d", index+1, total, title.ID), stage.WithActiveEpisode(episodeKey)); err != nil {
		logger.Warn("progress persistence failed",
			"event_type", "progress_persist_failed",
			"error_hint", "rip progress message not persisted",
			"impact", "rip progress not reflected in queue",
			"error", err,
		)
	}

	before := listMKVFiles(rippedDir)
	var lastRipLog time.Time
	err := makemkv.Rip(ctx, h.cfg.MakeMKV.OpticalDrive, title.ID, rippedDir,
		time.Duration(h.cfg.MakeMKV.RipTimeout)*time.Second,
		h.cfg.MakeMKV.MinTitleLength,
		func(p makemkv.RipProgress) {
			message := item.ProgressMessage
			if strings.TrimSpace(p.Message) != "" {
				message = p.Message
			}
			_ = sess.Progress(overallRipPercent(index, total, p.Percent), message)

			now := time.Now()
			if lastRipLog.IsZero() || now.Sub(lastRipLog) >= ripProgressLogInterval || p.Percent >= 100 {
				lastRipLog = now
				logger.Info("rip progress",
					"event_type", "rip_progress",
					"title_id", title.ID,
					"episode_key", episodeKey,
					"percent", p.Percent,
					"current", p.Current,
					"total", p.Total,
					"message", message,
				)
			}
		}, logger,
	)
	if err != nil {
		return fmt.Errorf("rip title %d: %w", title.ID, err)
	}

	newFile, err := h.discoverNewRippedFile(logger, rippedDir, title.ID, before)
	if err != nil {
		return err
	}
	if episodeKey != "" {
		if err := sess.SaveAssetSuccess(ripspec.AssetKindRipped, ripspec.Asset{
			EpisodeKey: episodeKey,
			TitleID:    title.ID,
			Path:       newFile,
		}); err != nil {
			return err
		}
	}

	if err := sess.Progress(overallRipPercent(index+1, total, 0), fmt.Sprintf("Phase %d/%d - Ripped title %d", index+1, total, title.ID)); err != nil {
		logger.Warn("progress persistence failed",
			"event_type", "progress_persist_failed",
			"error_hint", "rip completion progress not persisted",
			"impact", "rip progress not reflected in queue",
			"error", err,
		)
	}
	return nil
}

func (h *Handler) discoverNewRippedFile(logger *slog.Logger, rippedDir string, titleID int, before map[string]bool) (string, error) {
	after := listMKVFiles(rippedDir)
	newFile := findNewFile(before, after)
	if newFile == "" {
		logger.Error("title rip produced no new file",
			"decision_type", logs.DecisionFileDiscovery,
			"decision_result", "not_found",
			"decision_reason", fmt.Sprintf("title_id=%d", titleID),
			"event_type", "rip_output_missing",
			"error_hint", "makemkv rip returned success but no new mkv appeared in staging",
			"title_id", titleID,
			"ripped_dir", rippedDir,
		)
		return "", fmt.Errorf("rip title %d: no new mkv file in %s after rip", titleID, rippedDir)
	}

	var newFileSize int64
	if fi, statErr := os.Stat(newFile); statErr == nil {
		newFileSize = fi.Size()
	}
	logger.Info("title rip completed",
		"decision_type", logs.DecisionTitleRip,
		"decision_result", "completed",
		"decision_reason", fmt.Sprintf("title_id=%d file=%s size=%d", titleID, newFile, newFileSize),
		"title_id", titleID,
		"file", newFile,
		"size_bytes", newFileSize,
	)
	return newFile, nil
}

func (h *Handler) cacheFreshRip(logger *slog.Logger, sess *stage.Session, rippedDir string, rippedCount int) {
	item := sess.Item
	if h.cache == nil || item.DiscFingerprint == "" {
		return
	}

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
	if err := h.cache.Register(item.DiscFingerprint, rippedDir, h.cacheProgressFunc(sess, "Caching rip...")); err != nil {
		logger.Warn("rip cache write failed",
			"event_type", "cache_write_error",
			"error_hint", err.Error(),
			"impact", "no cache for next rip of this disc",
		)
		return
	}
	if err := h.cache.WriteMetadata(item.DiscFingerprint, meta); err != nil {
		logger.Warn("rip cache metadata write failed",
			"event_type", "cache_metadata_error",
			"error_hint", err.Error(),
			"impact", "cache entry may not be reused",
		)
	}
}

func (h *Handler) notifyRipComplete(ctx context.Context, logger *slog.Logger, sess *stage.Session, rippedCount int) {
	item := sess.Item
	msg := fmt.Sprintf("Ripped %s (%d titles)", item.DisplayTitle(), rippedCount)
	msg += "\n" + driveAvailableMsg
	msg += queue.FormatAlsoProcessing(sess.Store, item.ID)
	_ = notify.SendLogged(ctx, h.notifier, logger, notify.EventRipComplete,
		"Rip Complete: "+item.DisplayTitle(),
		msg,
		"item_id", item.ID,
	)
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
// directory for the first MKV. Validates all mapped assets with ffprobe. When
// titleFiles is non-nil, it is used as a pre-scanned view of dir (set by the
// rip-cache hit path to avoid rescanning).
func (h *Handler) mapAndValidateAssets(ctx context.Context, logger *slog.Logger, sess *stage.Session, dir string, titleFiles map[int]string) error {
	env := sess.Env
	if env.Metadata.MediaType == "tv" && len(env.Episodes) > 0 {
		logger.Info("asset mapping strategy selected",
			"decision_type", logs.DecisionAssetMapping,
			"decision_result", "title_id_scan",
			"decision_reason", fmt.Sprintf("media_type=%s episodes=%d", env.Metadata.MediaType, len(env.Episodes)),
		)
		result := assignEpisodeAssets(env, dir, titleFiles, logger)
		if result.Assigned == 0 {
			return fmt.Errorf("episode asset mapping: zero matches (expected %d episodes)", len(env.Episodes))
		}
		if len(result.Missing) > 0 {
			reason := fmt.Sprintf("missing %d episode(s): %s", len(result.Missing), strings.Join(result.Missing, ", "))
			for _, key := range result.Missing {
				sess.AddEpisodeReviewReason(key, "Rip asset missing")
			}
			sess.AddReviewReason(reason)
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
			env.Assets.AddAsset(ripspec.AssetKindRipped, ripspec.Asset{
				EpisodeKey: "main",
				Path:       filepath.Join(dir, entry.Name()),
				Status:     ripspec.AssetStatusCompleted,
			})
		}
	}

	// Validate all ripped artifacts with ffprobe.
	visited := make(map[string]struct{})
	var validationErrors int
	for i, asset := range env.Assets.Ripped {
		if _, seen := visited[asset.Path]; seen {
			continue
		}
		visited[asset.Path] = struct{}{}
		if err := h.validateRippedArtifact(ctx, asset.Path); err != nil {
			if env.Metadata.MediaType == "tv" && len(env.Episodes) > 0 {
				// Per-episode failure isolation: mark failed, continue.
				logger.Warn("ripped episode failed validation",
					"event_type", "rip_validation_failed",
					"error_hint", err.Error(),
					"impact", "episode excluded from pipeline",
					"episode_key", asset.EpisodeKey,
					"path", asset.Path,
				)
				env.Assets.Ripped[i].Status = ripspec.AssetStatusFailed
				env.Assets.Ripped[i].ErrorMsg = err.Error()
				validationErrors++
				continue
			}
			// Movies: fatal (single title).
			return fmt.Errorf("ripped artifact invalid (%s): %w", filepath.Base(asset.Path), err)
		}
	}

	if env.Metadata.MediaType == "tv" && validationErrors > 0 {
		valid := len(visited) - validationErrors
		if valid == 0 {
			return fmt.Errorf("all %d ripped episodes failed validation", validationErrors)
		}
		for _, asset := range env.Assets.Ripped {
			if asset.IsFailed() {
				sess.AddEpisodeReviewReason(asset.EpisodeKey, "Rip validation failed")
			}
		}
		reason := fmt.Sprintf("%d episode(s) failed rip validation", validationErrors)
		sess.AddReviewReason(reason)
		logger.Warn("partial rip validation",
			"event_type", "rip_validation_partial",
			"error_hint", "some episodes failed ffprobe validation",
			"impact", fmt.Sprintf("%d of %d episodes excluded", validationErrors, len(visited)),
		)
	}

	return nil
}

// cacheProgressFunc returns a throttled progress callback for cache operations.
func (h *Handler) cacheProgressFunc(sess *stage.Session, message string) ripcache.ProgressFunc {
	var lastPush time.Time
	return func(p ripcache.CopyProgress) {
		now := time.Now()
		if now.Sub(lastPush) < 2*time.Second {
			return
		}
		lastPush = now
		percent := float64(p.BytesCopied) / float64(p.TotalBytes) * 100
		_ = sess.Progress(percent, message, stage.WithProgressBytes(p.BytesCopied, p.TotalBytes))
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

func overallRipPercent(completedTitles, totalTitles int, currentTitlePercent float64) float64 {
	return stage.OverallPercent(completedTitles, totalTitles, currentTitlePercent)
}
