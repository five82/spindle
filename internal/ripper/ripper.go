package ripper

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/makemkv"
	"github.com/five82/spindle/internal/notify"
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

	rippedDir, err := h.prepareRipStaging(sess)
	if err != nil {
		return err
	}

	if restored, err := h.restoreFromRipCache(ctx, sess, rippedDir); restored || err != nil {
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

	if err := h.checkStagingSpace(logger, targets); err != nil {
		return err
	}

	ripStart := time.Now()
	if err := h.ripTitles(ctx, sess, rippedDir, targets); err != nil {
		return err
	}
	ripSeconds := time.Since(ripStart).Seconds()
	sess.ClearActiveEpisode()

	if err := h.mapAndValidateAssets(ctx, logger, sess, rippedDir, nil); err != nil {
		return err
	}
	if err := persistRipResults(sess); err != nil {
		return err
	}
	h.recordRipStats(logger, sess, rippedDir, len(targets), ripSeconds)

	h.cacheFreshRip(logger, sess, rippedDir, len(targets))
	h.notifyRipComplete(ctx, logger, sess, len(targets))
	return nil
}

func (h *Handler) prepareRipStaging(sess *stage.Session) (string, error) {
	logger := sess.Logger

	stagingRoot, err := sess.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		return "", err
	}
	rippedDir := filepath.Join(stagingRoot, "ripped")

	// Staging is mostly ephemeral, but a rip re-run (daemon restart, retry)
	// must not destroy restart-resumable state: completed title files in
	// ripped/ (recorded in the envelope; ripTitles skips them) and all of
	// encoded/ (finished encodes plus reel's chunk-level resume dirs for the
	// encoding branch that overlaps this stage). Everything else -- partial
	// rips, leftovers from a previous pipeline run of the same disc -- is
	// wiped so file discovery starts clean.
	completed := make(map[string]bool)
	for _, asset := range sess.Env.Assets.Ripped {
		if asset.IsCompleted() {
			completed[asset.Path] = true
		}
	}
	removed := 0
	entries, err := os.ReadDir(stagingRoot)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("reset staging dir: %w", err)
	}
	for _, entry := range entries {
		path := filepath.Join(stagingRoot, entry.Name())
		switch entry.Name() {
		case "encoded":
		case "ripped":
			inner, readErr := os.ReadDir(path)
			if readErr != nil {
				return "", fmt.Errorf("reset staging dir: %w", readErr)
			}
			for _, f := range inner {
				filePath := filepath.Join(path, f.Name())
				if completed[filePath] {
					continue
				}
				if err := os.RemoveAll(filePath); err != nil {
					return "", fmt.Errorf("reset staging dir: %w", err)
				}
				removed++
			}
		default:
			if err := os.RemoveAll(path); err != nil {
				return "", fmt.Errorf("reset staging dir: %w", err)
			}
			removed++
		}
	}
	logger.Info("staging directory reset for rip",
		"decision_type", logs.DecisionStagingCleanup,
		"decision_result", "reset",
		"decision_reason", "wiped non-resumable staging state; kept completed rips and encode resume state",
		"removed_entries", removed,
		"kept_ripped_titles", len(completed),
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
		attrs := []any{
			"decision_type", logs.DecisionRipCache,
			"decision_result", "miss",
		}
		if err != nil {
			attrs = append(attrs, "decision_reason", "cache restore failed", "error", err.Error())
		} else {
			attrs = append(attrs, "decision_reason", "no cache entry for fingerprint")
		}
		logger.Info("rip cache miss, fresh rip required", attrs...)
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
	displayTitle := item.DisplayTitle()
	if env.Metadata.DiscNumber > 0 {
		displayTitle += fmt.Sprintf(" - Disc %d", env.Metadata.DiscNumber)
	}
	titleWord := "titles"
	if meta.TitleCount == 1 {
		titleWord = "title"
	}
	msg := fmt.Sprintf("Restored %d %s from rip cache.\n%s", meta.TitleCount, titleWord, driveAvailableMsg)
	_ = notify.SendLogged(ctx, h.notifier, logger, notify.EventRipCacheHit,
		"Rip ready from cache: "+displayTitle,
		msg,
	)

	if err := h.mapAndValidateAssets(ctx, logger, sess, rippedDir, cachedTitleFiles); err != nil {
		return true, err
	}
	h.restoreTitlesFromCachedEnvelope(logger, env, meta.RipSpecData)
	if err := persistRipResults(sess); err != nil {
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
	// counts while the stage is still running. Titles whose ripped asset the
	// envelope already records as completed (preserved by prepareRipStaging
	// across a restart) are skipped, so an interrupted multi-title rip
	// resumes at the next title instead of re-ripping the disc.
	for i, title := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if key := titleEpisodeKey[title.ID]; key != "" {
			if asset, ok := sess.Env.Assets.FindAsset(ripspec.AssetKindRipped, key); ok && asset.IsCompleted() {
				if _, statErr := os.Stat(asset.Path); statErr == nil {
					sess.Logger.Info("title already ripped",
						"decision_type", logs.DecisionTitleRip,
						"decision_result", "skipped",
						"decision_reason", "completed rip preserved from interrupted run",
						"title_id", title.ID,
						"episode_key", key,
					)
					sess.Progress(overallRipPercent(i+1, len(targets), 0), fmt.Sprintf("Phase %d/%d - Ripped title %d", i+1, len(targets), title.ID))
					continue
				}
			}
		}
		if err := h.ripTitle(ctx, sess, rippedDir, title, i, len(targets), titleEpisodeKey[title.ID]); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) ripTitle(ctx context.Context, sess *stage.Session, rippedDir string, title ripspec.Title, index, total int, episodeKey string) error {
	logger := sess.Logger

	logger.Info(fmt.Sprintf("Phase %d/%d - Ripping title %d", index+1, total, title.ID),
		"event_type", "rip_title_start",
	)

	sess.Progress(overallRipPercent(index, total, 0), fmt.Sprintf("Phase %d/%d - Ripping title %d", index+1, total, title.ID), stage.WithActiveEpisode(episodeKey))

	before := listMKVFiles(rippedDir)
	var lastRipLog time.Time
	err := makemkv.Rip(ctx, h.cfg.MakeMKV.OpticalDrive, title.ID, rippedDir,
		time.Duration(h.cfg.MakeMKV.RipTimeout)*time.Second,
		h.cfg.MakeMKV.MinTitleLength,
		func(p makemkv.RipProgress) {
			message := sess.Task.ProgressMessage
			sess.Progress(overallRipPercent(index, total, p.Percent), message)

			now := time.Now()
			if lastRipLog.IsZero() || now.Sub(lastRipLog) >= ripProgressLogInterval {
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

	sess.Progress(overallRipPercent(index+1, total, 0), fmt.Sprintf("Phase %d/%d - Ripped title %d", index+1, total, title.ID))
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

// recordRipStats persists drive identity and throughput for a fresh rip into
// the envelope for the item-completion metrics record. Best-effort: a failure
// here must not fail a rip that already succeeded.
func (h *Handler) recordRipStats(logger *slog.Logger, sess *stage.Session, rippedDir string, titles int, seconds float64) {
	var bytes int64
	for path := range listMKVFiles(rippedDir) {
		if fi, err := os.Stat(path); err == nil {
			bytes += fi.Size()
		}
	}
	device := h.cfg.MakeMKV.OpticalDrive
	vendor, model := driveInfo(device)
	stats := &ripspec.RipStats{
		Device:      device,
		DriveVendor: vendor,
		DriveModel:  model,
		Bytes:       bytes,
		Seconds:     seconds,
		Titles:      titles,
	}
	if err := sess.MergeSave(func(env *ripspec.Envelope) error {
		env.Attributes.Rip = stats
		return nil
	}); err != nil {
		logger.Warn("rip stats not persisted",
			"event_type", "rip_stats_error",
			"error_hint", err.Error(),
			"impact", "metrics record will lack drive and rip throughput data",
		)
		return
	}
	logger.Debug("rip stats",
		"drive_vendor", vendor,
		"drive_model", model,
		"device", device,
		"bytes", bytes,
		"seconds", seconds,
	)
}

// driveInfo reads the drive's vendor and model strings from sysfs so rip
// throughput can be compared across physical drives; device paths like
// /dev/sr0 are assignment-order dependent and identify nothing.
func driveInfo(device string) (vendor, model string) {
	if resolved, err := filepath.EvalSymlinks(device); err == nil {
		device = resolved
	}
	if !strings.HasPrefix(device, "/dev/") {
		return "", ""
	}
	read := func(field string) string {
		data, err := os.ReadFile(filepath.Join("/sys/class/block", filepath.Base(device), "device", field))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
	return read("vendor"), read("model")
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
		DiscNumber:   sess.Env.Metadata.DiscNumber,
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
		// An entry without metadata can never be restored or pruned; drop it
		// rather than leave dead weight in the cache.
		logger.Warn("rip cache metadata write failed",
			"event_type", "cache_metadata_error",
			"error_hint", err.Error(),
			"impact", "cache entry removed; no cache for next rip of this disc",
		)
		if removeErr := h.cache.Remove(item.DiscFingerprint); removeErr != nil {
			logger.Warn("rip cache entry removal failed",
				"event_type", "cache_remove_error",
				"error_hint", removeErr.Error(),
				"impact", "unusable cache entry left on disk",
			)
		}
		return
	}
	if err := h.cache.Prune(); err != nil {
		logger.Warn("rip cache prune failed",
			"event_type", "cache_prune_error",
			"error_hint", err.Error(),
			"impact", "rip cache may exceed its configured size limit",
		)
	}
}

func (h *Handler) notifyRipComplete(ctx context.Context, logger *slog.Logger, sess *stage.Session, rippedCount int) {
	item := sess.Item
	displayTitle := item.DisplayTitle()
	if sess.Env.Metadata.DiscNumber > 0 {
		displayTitle += fmt.Sprintf(" - Disc %d", sess.Env.Metadata.DiscNumber)
	}
	titleWord := "titles"
	if rippedCount == 1 {
		titleWord = "title"
	}
	msg := fmt.Sprintf("Ripped %d %s.\n%s", rippedCount, titleWord, driveAvailableMsg)
	_ = notify.SendLogged(ctx, h.notifier, logger, notify.EventRipComplete,
		"Rip complete: "+displayTitle,
		msg,
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
		selection, ok, candidates, rejects, steps := PrimaryTitleDecisionSummary(env.Titles)
		if ok {
			for _, step := range steps {
				result := "narrowed"
				if step.After == step.Before {
					result = "not_applied"
				}
				stepAttrs := []any{
					"decision_type", logs.DecisionTitleSelectionFunnel,
					"decision_result", result,
					"decision_reason", step.Rule,
					"candidates_before", step.Before,
					"candidates_after", step.After,
				}
				if len(step.Eliminated) > 0 {
					stepAttrs = append(stepAttrs, "eliminated_title_ids", fmt.Sprint(step.Eliminated))
				}
				if step.Detail != "" {
					stepAttrs = append(stepAttrs, "evidence", step.Detail)
				}
				logger.Info("primary title funnel", stepAttrs...)
			}
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
			"decision_result", "selected",
			"decision_reason", "episode-referenced titles only",
			"titles", len(targets),
			"episodes", len(env.Episodes),
		)
		return targets, nil

	default:
		// Identification fails the item when TMDB cannot name the disc, so a
		// media type other than movie/tv cannot reach ripping. Reaching here
		// means the envelope was built by a path that skipped that gate.
		return nil, fmt.Errorf("cannot select rip targets for media type %q", env.Metadata.MediaType)
	}
}

// stagingSpaceMargin oversizes the MakeMKV scan estimate to absorb estimate
// drift and other writes on the staging volume during the rip.
const stagingSpaceMargin = 1.1

// checkStagingSpace fails the rip immediately when the staging volume cannot
// hold the selected titles, instead of surfacing a confusing copy error deep
// into the rip. The check is advisory-only in the other direction: unknown
// title sizes or a statfs failure never block a rip that might succeed.
func (h *Handler) checkStagingSpace(logger *slog.Logger, targets []ripspec.Title) error {
	// Sum only the titles MakeMKV sized. A title with no estimate used to
	// abandon the whole check, so one sizeless title left an entire rip
	// unguarded; the known subset is a lower bound on what the rip needs, and
	// failing when even that does not fit still cannot produce a false alarm.
	var estimated int64
	var unsized int
	for _, t := range targets {
		if t.SizeBytes <= 0 {
			unsized++
			continue
		}
		estimated += t.SizeBytes
	}
	if estimated == 0 {
		logger.Debug("staging space preflight skipped",
			"event_type", "staging_space_preflight",
			"reason", "no title sizes known",
			"titles", len(targets),
		)
		return nil
	}

	var fs unix.Statfs_t
	if err := unix.Statfs(h.cfg.Paths.StagingDir, &fs); err != nil {
		logger.Debug("staging space preflight skipped",
			"event_type", "staging_space_preflight",
			"reason", "statfs failed",
			"error", err.Error(),
		)
		return nil
	}
	free := int64(fs.Bavail) * int64(fs.Bsize)
	required := int64(float64(estimated) * stagingSpaceMargin)
	if free < required {
		sized := len(targets) - unsized
		scope := "rip"
		if unsized > 0 {
			scope = fmt.Sprintf("%d of %d titles in the rip", sized, len(targets))
		}
		return fmt.Errorf(
			"insufficient staging space: %s needs about %.1f GiB (%.1f GiB estimated plus margin) but %s has %.1f GiB free; free up space and retry",
			scope, gib(required), gib(estimated), h.cfg.Paths.StagingDir, gib(free))
	}
	logger.Debug("staging space preflight passed",
		"event_type", "staging_space_preflight",
		"estimated_bytes", estimated,
		"required_bytes", required,
		"free_bytes", free,
		"unsized_titles", unsized,
	)
	return nil
}

func gib(bytes int64) float64 { return float64(bytes) / (1 << 30) }

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
			if err := sess.MergeAddReviewReason(reason); err != nil {
				return err
			}
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
		if err := assignMovieAssets(env, dir); err != nil {
			return err
		}
	}

	// Validate all ripped artifacts with ffprobe. Both the fresh-rip and
	// rip-cache-restore paths funnel through this function.
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
		if err := sess.MergeAddReviewReason(reason); err != nil {
			return err
		}
		logger.Warn("partial rip validation",
			"event_type", "rip_validation_partial",
			"error_hint", "some episodes failed ffprobe validation",
			"impact", fmt.Sprintf("%d of %d episodes excluded", validationErrors, len(visited)),
		)
	}

	return nil
}

// persistRipResults merges the ripper-owned envelope fields. Ripping overlaps
// encoding, so replacing the whole envelope here would discard encoded assets
// completed while the remaining titles were still being ripped.
func persistRipResults(sess *stage.Session) error {
	titles := append([]ripspec.Title(nil), sess.Env.Titles...)
	ripped := append([]ripspec.Asset(nil), sess.Env.Assets.Ripped...)
	type episodeReview struct {
		key    string
		reason string
	}
	var reviews []episodeReview
	for _, ep := range sess.Env.Episodes {
		if ep.NeedsReview {
			reviews = append(reviews, episodeReview{key: ep.Key, reason: ep.ReviewReason})
		}
	}

	return sess.MergeSave(func(env *ripspec.Envelope) error {
		if len(env.Titles) == 0 && len(titles) > 0 {
			env.Titles = append([]ripspec.Title(nil), titles...)
		}
		for _, asset := range ripped {
			env.Assets.AddAsset(ripspec.AssetKindRipped, asset)
		}
		for _, review := range reviews {
			if ep := env.EpisodeByKey(review.key); ep != nil {
				ep.NeedsReview = true
				ep.ReviewReason = review.reason
			}
		}
		return nil
	})
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
		sess.Progress(percent, message, stage.WithProgressBytes(p.BytesCopied, p.TotalBytes))
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
