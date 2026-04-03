package organizer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/fileutil"
	"github.com/five82/spindle/internal/jellyfin"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/textutil"
)

// Handler implements stage.Handler for organization.
type Handler struct {
	cfg      *config.Config
	store    *queue.Store
	jfClient *jellyfin.Client
	notifier *notify.Notifier
}

// New creates an organization handler.
func New(cfg *config.Config, store *queue.Store, jfClient *jellyfin.Client, notifier *notify.Notifier) *Handler {
	return &Handler{cfg: cfg, store: store, jfClient: jfClient, notifier: notifier}
}

// Run executes the organization stage.
func (h *Handler) Run(ctx context.Context, item *queue.Item) error {
	logger := stage.LoggerFromContext(ctx)
	logger.Info("organization stage started", "event_type", "stage_start", "stage", "organizing")

	env, err := stage.ParseRipSpec(item.RipSpecData)
	if err != nil {
		return err
	}

	meta := queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)
	keys := env.AssetKeys()
	sourceStage, hasSubtitled := resolveSourceStage(&env, keys)
	logger.Info("organization source stage selected",
		"decision_type", logs.DecisionSourceStageSelection,
		"decision_result", sourceStage,
		"decision_reason", fmt.Sprintf("subtitled_available=%v", hasSubtitled),
	)

	if item.NeedsReview == 1 {
		if env.Metadata.MediaType != "tv" || !ripspec.HasResolvedEpisodes(env.Episodes) {
			logger.Info("item routed to review",
				"decision_type", logs.DecisionOrganizeRoute,
				"decision_result", "review",
				"decision_reason", "needs_review flag set with no clean resolved tv episodes",
			)
			return h.routeToReview(ctx, logger, item, &env, &meta, sourceStage, keys)
		}

		libraryKeys, reviewKeys := partitionTVOrganizationKeys(&env)
		if len(libraryKeys) == 0 {
			logger.Info("item routed to review",
				"decision_type", logs.DecisionOrganizeRoute,
				"decision_result", "review",
				"decision_reason", "all resolved episodes flagged for review",
			)
			return h.routeToReview(ctx, logger, item, &env, &meta, sourceStage, reviewKeys)
		}
		logger.Info("item partially organized",
			"decision_type", logs.DecisionOrganizeRoute,
			"decision_result", "partial_library_review",
			"decision_reason", fmt.Sprintf("clean_episodes=%d review_episodes=%d", len(libraryKeys), len(reviewKeys)),
		)

		libraryPath, err := meta.GetLibraryPath(
			h.cfg.Paths.LibraryDir,
			h.cfg.Library.MoviesDir,
			h.cfg.Library.TVDir,
		)
		if err != nil {
			return fmt.Errorf("resolve library path: %w", err)
		}
		if err := os.MkdirAll(libraryPath, 0o755); err != nil {
			return fmt.Errorf("create library dir: %w", err)
		}
		if _, _, err := h.copyAssetsToDir(ctx, logger, item, &env, &meta, sourceStage, libraryPath, libraryKeys, "library"); err != nil {
			return err
		}
		if len(reviewKeys) > 0 {
			if _, _, err := h.copyAssetsToDir(ctx, logger, item, &env, &meta, sourceStage, reviewPathForItem(h.cfg.Paths.ReviewDir, item), reviewKeys, "review"); err != nil {
				return err
			}
		}
		if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
			return err
		}
		item.ProgressPercent = 100
		item.ProgressMessage = fmt.Sprintf("Available in library (%d episodes, %d to review)", len(libraryKeys), len(reviewKeys))
		_ = h.store.UpdateProgress(item)
	} else {
		libraryPath, err := meta.GetLibraryPath(
			h.cfg.Paths.LibraryDir,
			h.cfg.Library.MoviesDir,
			h.cfg.Library.TVDir,
		)
		if err != nil {
			return fmt.Errorf("resolve library path: %w", err)
		}
		if err := os.MkdirAll(libraryPath, 0o755); err != nil {
			return fmt.Errorf("create library dir: %w", err)
		}
		if _, _, err := h.copyAssetsToDir(ctx, logger, item, &env, &meta, sourceStage, libraryPath, keys, "library"); err != nil {
			return err
		}
		if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
			return err
		}
	}

	// Trigger Jellyfin refresh.
	if h.jfClient != nil {
		if err := h.jfClient.Refresh(ctx); err != nil {
			logger.Warn("jellyfin refresh failed",
				"event_type", "jellyfin_refresh_error",
				"error_hint", err.Error(),
				"impact", "library may not show new content immediately",
			)
			// Degraded, not fatal.
		}
	}

	// Notification.
	if h.notifier != nil {
		msg := fmt.Sprintf("Organized %s to library", item.DiscTitle)
		if item.NeedsReview == 1 && env.Metadata.MediaType == "tv" {
			msg = fmt.Sprintf("Organized clean episodes for %s; some episodes routed to review", item.DiscTitle)
		}
		msg += queue.FormatAlsoProcessing(h.store, item.ID)
		_ = h.notifier.Send(ctx, notify.EventOrganizeComplete,
			"Organization Complete",
			msg,
		)
	}

	h.cleanupStaging(ctx, item)

	logger.Info("organization stage completed", "event_type", "stage_complete", "stage", "organizing")
	return nil
}

// destFilename builds the destination filename for a given asset key.
func destFilename(meta *queue.Metadata, key, ext string) string {
	if meta.IsMovie() {
		return meta.GetFilename() + ext
	}

	// For TV, build a per-episode filename from the key.
	// Parse season/episode from the key (format: "s01e03").
	season, episode := parseEpisodeKey(key)
	if season > 0 && episode > 0 {
		// Build per-episode metadata to get the correct filename.
		epMeta := queue.Metadata{
			Title:        meta.Title,
			ShowTitle:    meta.ShowTitle,
			MediaType:    "tv",
			SeasonNumber: meta.SeasonNumber,
			Episodes: []queue.MetadataEpisode{
				{Season: season, Episode: episode},
			},
			DisplayTitle: meta.DisplayTitle,
		}
		return textutil.SanitizeDisplayName(epMeta.GetFilename()) + ext
	}

	// Fallback: use the key directly as part of the filename.
	show := textutil.SanitizeDisplayName(meta.ShowTitle)
	if show == "" || show == "manual-import" {
		show = textutil.SanitizeDisplayName(meta.Title)
	}
	return textutil.SanitizeDisplayName(show+" - "+key) + ext
}

// parseEpisodeKey extracts season and episode numbers from a key like "s01e03".
// Returns (0, 0) if the key does not match the expected format.
func parseEpisodeKey(key string) (season, episode int) {
	_, err := fmt.Sscanf(strings.ToLower(key), "s%02de%02d", &season, &episode)
	if err != nil {
		return 0, 0
	}
	return season, episode
}

func resolveSourceStage(env *ripspec.Envelope, keys []string) (string, bool) {
	sourceStage := "subtitled"
	hasSubtitled := true
	if len(keys) == 0 {
		return sourceStage, hasSubtitled
	}
	if _, ok := env.Assets.FindAsset("subtitled", keys[0]); !ok {
		return "encoded", false
	}
	return sourceStage, hasSubtitled
}

func partitionTVOrganizationKeys(env *ripspec.Envelope) (libraryKeys, reviewKeys []string) {
	for _, ep := range env.Episodes {
		if ep.Key == "" {
			continue
		}
		if ep.Episode > 0 && !ep.NeedsReview {
			libraryKeys = append(libraryKeys, ep.Key)
		} else {
			reviewKeys = append(reviewKeys, ep.Key)
		}
	}
	return libraryKeys, reviewKeys
}

func reviewPathForItem(reviewDir string, item *queue.Item) string {
	reason := textutil.SanitizePathSegment(item.ReviewReason)
	if reason == "" {
		reason = "manual-review"
	}
	fpPrefix := item.DiscFingerprint
	if len(fpPrefix) > 8 {
		fpPrefix = fpPrefix[:8]
	}
	if fpPrefix == "" {
		fpPrefix = fmt.Sprintf("id%d", item.ID)
	}
	dirName := reason + "_" + fpPrefix
	path, err := textutil.SafeJoin(reviewDir, dirName)
	if err != nil {
		return filepath.Join(reviewDir, dirName)
	}
	return path
}

func (h *Handler) copyAssetsToDir(ctx context.Context, logger *slog.Logger, item *queue.Item, env *ripspec.Envelope, meta *queue.Metadata, sourceStage, destDir string, keys []string, target string) (string, int, error) {
	if len(keys) == 0 {
		return "", 0, nil
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("create %s dir: %w", target, err)
	}

	totalBytes := totalCompletedStageBytes(env, sourceStage, keys)
	var completedBytes int64
	copied := 0
	lastPath := ""
	for i, key := range keys {
		if ctx.Err() != nil {
			return "", copied, ctx.Err()
		}

		asset, ok := env.Assets.FindAsset(sourceStage, key)
		if !ok || !asset.IsCompleted() {
			logger.Warn("missing or incomplete asset",
				"event_type", "organize_missing_asset",
				"error_hint", fmt.Sprintf("no completed %s asset for %s", sourceStage, key),
				"impact", fmt.Sprintf("episode will not be copied to %s", target),
			)
			continue
		}

		destName := destFilename(meta, key, filepath.Ext(asset.Path))
		destPath := filepath.Join(destDir, destName)
		if target == "library" && !h.cfg.Library.OverwriteExisting {
			if info, err := os.Stat(destPath); err == nil {
				srcInfo, srcErr := os.Stat(asset.Path)
				if srcErr == nil && info.Size() < srcInfo.Size() {
					logger.Info("removing partial file from previous attempt",
						"decision_type", logs.DecisionPartialCleanup,
						"decision_result", "removed",
						"decision_reason", fmt.Sprintf("target %d bytes < source %d bytes", info.Size(), srcInfo.Size()),
						"path", destPath,
					)
					if err := os.Remove(destPath); err != nil {
						return "", copied, fmt.Errorf("remove partial file %s: %w", destPath, err)
					}
				} else {
					logger.Info("file exists, skipping",
						"decision_type", logs.DecisionOrganizeSkip,
						"decision_result", "skipped",
						"decision_reason", "file already exists",
						"path", destPath,
					)
					continue
				}
			}
		}

		eventType := "organize_copy"
		if target == "review" {
			eventType = "review_copy"
		}
		logger.Info(fmt.Sprintf("Phase %d/%d - Copying to %s (%s)", i+1, len(keys), target, key),
			"event_type", eventType,
		)
		item.ProgressMessage = fmt.Sprintf("Phase %d/%d - Copying to %s (%s)", i+1, len(keys), target, key)
		item.ProgressBytesCopied = completedBytes
		item.ProgressTotalBytes = totalBytes
		item.ProgressPercent = overallBytePercent(completedBytes, totalBytes)
		_ = h.store.UpdateProgress(item)

		if err := fileutil.CopyFileVerifiedWithProgress(asset.Path, destPath, func(p fileutil.CopyProgress) {
			item.ProgressBytesCopied = completedBytes + p.BytesCopied
			item.ProgressTotalBytes = totalBytes
			item.ProgressPercent = overallBytePercent(item.ProgressBytesCopied, totalBytes)
			_ = h.store.UpdateProgress(item)
		}); err != nil {
			if ctx.Err() != nil {
				_ = os.Remove(destPath)
				return "", copied, ctx.Err()
			}
			return "", copied, fmt.Errorf("copy %s to %s: %w", key, target, err)
		}

		logger.Info("asset copied",
			"event_type", "asset_copied",
			"episode_key", key,
			"dest_path", destPath,
			"organize_target", target,
		)
		copySidecarSubtitle(logger, asset.Path, destPath)
		env.Assets.AddAsset("final", ripspec.Asset{EpisodeKey: key, Path: destPath, Status: "completed"})
		if err := queue.PersistRipSpec(ctx, h.store, item, env); err != nil {
			return "", copied, err
		}
		item.FinalFile = destPath
		lastPath = destPath
		copied++
		if info, statErr := os.Stat(asset.Path); statErr == nil {
			completedBytes += info.Size()
		}
		item.ProgressBytesCopied = completedBytes
		item.ProgressTotalBytes = totalBytes
		item.ProgressPercent = overallBytePercent(completedBytes, totalBytes)
	}
	return lastPath, copied, nil
}

// routeToReview copies assets to the review directory for manual inspection.
// Directory structure: review_dir/{reason}_{fingerprint_prefix}/
func (h *Handler) routeToReview(ctx context.Context, logger *slog.Logger, item *queue.Item, env *ripspec.Envelope, meta *queue.Metadata, sourceStage string, keys []string) error {
	logger.Info("routing to review",
		"decision_type", logs.DecisionOrganizeRoute,
		"decision_result", "review",
		"decision_reason", item.ReviewReason,
	)

	reviewPath := reviewPathForItem(h.cfg.Paths.ReviewDir, item)
	if _, _, err := h.copyAssetsToDir(ctx, logger, item, env, meta, sourceStage, reviewPath, keys, "review"); err != nil {
		return err
	}
	if err := queue.PersistRipSpec(ctx, h.store, item, env); err != nil {
		return err
	}

	h.cleanupStaging(ctx, item)

	logger.Info("review routing completed", "event_type", "stage_complete", "stage", "organizing", "review_path", reviewPath)
	return nil
}

// cleanupStaging removes the staging directory for a completed item.
// Failures are logged as warnings (non-fatal) — disk space reclamation is
// best-effort.
func (h *Handler) cleanupStaging(ctx context.Context, item *queue.Item) {
	logger := stage.LoggerFromContext(ctx)
	root, err := item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		logger.Warn("cannot resolve staging root for cleanup",
			"event_type", "staging_cleanup_failed",
			"error_hint", err.Error(),
			"impact", "disk space not reclaimed; manual cleanup needed",
		)
		return
	}
	if err := os.RemoveAll(root); err != nil {
		logger.Warn("failed to clean staging directory; leftover files remain",
			"staging_root", root,
			"event_type", "staging_cleanup_failed",
			"error_hint", "check staging_dir permissions",
			"impact", "disk space not reclaimed; manual cleanup needed",
		)
		return
	}
	logger.Info("cleaned staging directory",
		"event_type", "staging_cleanup",
		"staging_root", root,
	)
}

func totalCompletedStageBytes(env *ripspec.Envelope, stage string, keys []string) int64 {
	if env == nil {
		return 0
	}
	var total int64
	for _, key := range keys {
		asset, ok := env.Assets.FindAsset(stage, key)
		if !ok || !asset.IsCompleted() {
			continue
		}
		if info, err := os.Stat(asset.Path); err == nil {
			total += info.Size()
		}
	}
	return total
}

func overallBytePercent(copiedBytes, totalBytes int64) float64 {
	if totalBytes <= 0 {
		return 0
	}
	if copiedBytes < 0 {
		copiedBytes = 0
	}
	if copiedBytes > totalBytes {
		copiedBytes = totalBytes
	}
	return float64(copiedBytes) / float64(totalBytes) * 100
}

// copySidecarSubtitle copies a .en.srt sidecar subtitle file alongside the
// destination video if one exists next to the source video.
func copySidecarSubtitle(logger *slog.Logger, srcVideo, destVideo string) {
	srcSrt := strings.TrimSuffix(srcVideo, filepath.Ext(srcVideo)) + ".en.srt"
	if _, err := os.Stat(srcSrt); err != nil {
		logger.Info("sidecar subtitle not found, skipping",
			"decision_type", logs.DecisionSidecarSubtitleCopy,
			"decision_result", "skipped",
			"decision_reason", "source SRT does not exist",
		)
		return
	}

	destSrt := strings.TrimSuffix(destVideo, filepath.Ext(destVideo)) + ".en.srt"
	if err := fileutil.CopyFile(srcSrt, destSrt); err != nil {
		logger.Warn("failed to copy sidecar subtitle",
			"event_type", "sidecar_copy_error",
			"error_hint", err.Error(),
			"impact", "subtitle file not available in library",
		)
	}
}
