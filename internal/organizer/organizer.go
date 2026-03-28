package organizer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/fileutil"
	"github.com/five82/spindle/internal/jellyfin"
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

	// Check if item needs review routing instead of library placement.
	if item.NeedsReview == 1 {
		logger.Info("item routed to review",
			"decision_type", logs.DecisionOrganizeRoute,
			"decision_result", "review",
			"decision_reason", "needs_review flag set",
		)
		return h.routeToReview(ctx, logger, item, &env, &meta)
	}

	// Resolve library path.
	libraryPath, err := meta.GetLibraryPath(
		h.cfg.Paths.LibraryDir,
		h.cfg.Library.MoviesDir,
		h.cfg.Library.TVDir,
	)
	if err != nil {
		return fmt.Errorf("resolve library path: %w", err)
	}

	// Create library directory.
	if err := os.MkdirAll(libraryPath, 0o755); err != nil {
		return fmt.Errorf("create library dir: %w", err)
	}

	// Determine source stage: prefer "subtitled", fall back to "encoded".
	sourceStage := "subtitled"
	keys := env.AssetKeys()
	hasSubtitled := true
	if _, ok := env.Assets.FindAsset("subtitled", keys[0]); !ok {
		sourceStage = "encoded"
		hasSubtitled = false
	}
	logger.Info("organization source stage selected",
		"decision_type", logs.DecisionSourceStageSelection,
		"decision_result", sourceStage,
		"decision_reason", fmt.Sprintf("subtitled_available=%v", hasSubtitled),
	)

	// Copy each asset to library.
	for i, key := range keys {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		asset, ok := env.Assets.FindAsset(sourceStage, key)
		if !ok || !asset.IsCompleted() {
			logger.Warn("missing or incomplete asset",
				"event_type", "organize_missing_asset",
				"error_hint", fmt.Sprintf("no completed %s asset for %s", sourceStage, key),
				"impact", "episode will not be organized",
			)
			continue
		}

		// Determine destination filename.
		destName := destFilename(&meta, key, filepath.Ext(asset.Path))
		destPath := filepath.Join(libraryPath, destName)

		// Validate edition appears in filename for movies with editions.
		if meta.IsMovie() && meta.Edition != "" {
			if err := validateEditionFilename(destName, meta.Edition); err != nil {
				logger.Error("edition validation failed",
					"event_type", "edition_validation_failed",
					"error_hint", "edition not found in generated filename",
					"impact", "output file may have wrong name",
					"error", err.Error(),
				)
				return err
			}
			logger.Info("edition validation passed",
				"event_type", "edition_validation_passed",
			)
		}

		// Check if exists and not overwriting.
		if !h.cfg.Library.OverwriteExisting {
			if info, err := os.Stat(destPath); err == nil {
				// Check for partial file from previous interrupted copy.
				srcInfo, srcErr := os.Stat(asset.Path)
				if srcErr == nil && info.Size() < srcInfo.Size() {
					logger.Info("removing partial file from previous attempt",
						"decision_type", logs.DecisionPartialCleanup,
						"decision_result", "removed",
						"decision_reason", fmt.Sprintf("target %d bytes < source %d bytes", info.Size(), srcInfo.Size()),
						"path", destPath,
					)
					if err := os.Remove(destPath); err != nil {
						return fmt.Errorf("remove partial file %s: %w", destPath, err)
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

		logger.Info(fmt.Sprintf("Phase %d/%d - Copying to library (%s)", i+1, len(keys), key),
			"event_type", "organize_copy",
		)

		item.ProgressMessage = fmt.Sprintf("Phase %d/%d - Copying to library (%s)", i+1, len(keys), key)
		_ = h.store.UpdateProgress(item)

		// Copy with verification.
		if err := fileutil.CopyFileVerified(asset.Path, destPath); err != nil {
			// On cancellation, clean up partial file.
			if ctx.Err() != nil {
				_ = os.Remove(destPath)
				return ctx.Err()
			}
			return fmt.Errorf("copy %s to library: %w", key, err)
		}

		logger.Info("asset copied to library",
			"event_type", "asset_copied",
			"episode_key", key,
			"dest_path", destPath,
		)

		// Record final asset.
		env.Assets.AddAsset("final", ripspec.Asset{
			EpisodeKey: key,
			Path:       destPath,
			Status:     "completed",
		})
		item.FinalFile = destPath

		// Copy subtitle sidecar if exists (non-muxed).
		copySidecarSubtitle(logger, asset.Path, destPath)
	}

	// Persist envelope with final asset paths.
	if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
		return err
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
// Movies: "{GetFilename()}{ext}". TV: per-episode filename built from
// metadata with sanitized display name.
func destFilename(meta *queue.Metadata, key, ext string) string {
	if meta.IsMovie() {
		return textutil.SanitizeDisplayName(meta.GetFilename()) + ext
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

// routeToReview copies assets to the review directory for manual inspection.
// Directory structure: review_dir/{reason}_{fingerprint_prefix}/
func (h *Handler) routeToReview(ctx context.Context, logger *slog.Logger, item *queue.Item, env *ripspec.Envelope, meta *queue.Metadata) error {
	logger.Info("routing to review",
		"decision_type", logs.DecisionOrganizeRoute,
		"decision_result", "review",
		"decision_reason", item.ReviewReason,
	)

	// Build review subdirectory name.
	reason := textutil.SanitizePathSegment(item.ReviewReason)
	fpPrefix := item.DiscFingerprint
	if len(fpPrefix) > 8 {
		fpPrefix = fpPrefix[:8]
	}
	if fpPrefix == "" {
		fpPrefix = fmt.Sprintf("id%d", item.ID)
	}
	dirName := reason + "_" + fpPrefix

	reviewPath, err := textutil.SafeJoin(h.cfg.Paths.ReviewDir, dirName)
	if err != nil {
		return fmt.Errorf("resolve review path: %w", err)
	}

	if err := os.MkdirAll(reviewPath, 0o755); err != nil {
		return fmt.Errorf("create review dir: %w", err)
	}

	// Determine source stage.
	sourceStage := "subtitled"
	keys := env.AssetKeys()
	hasSubtitled := true
	if len(keys) > 0 {
		if _, ok := env.Assets.FindAsset("subtitled", keys[0]); !ok {
			sourceStage = "encoded"
			hasSubtitled = false
		}
	}
	logger.Info("organization source stage selected",
		"decision_type", logs.DecisionSourceStageSelection,
		"decision_result", sourceStage,
		"decision_reason", fmt.Sprintf("subtitled_available=%v", hasSubtitled),
	)

	for i, key := range keys {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		asset, ok := env.Assets.FindAsset(sourceStage, key)
		if !ok || !asset.IsCompleted() {
			continue
		}

		destName := destFilename(meta, key, filepath.Ext(asset.Path))
		destPath := filepath.Join(reviewPath, destName)

		logger.Info(fmt.Sprintf("Phase %d/%d - Copying to review (%s)", i+1, len(keys), key),
			"event_type", "review_copy",
		)

		if err := fileutil.CopyFileVerified(asset.Path, destPath); err != nil {
			if ctx.Err() != nil {
				_ = os.Remove(destPath)
				return ctx.Err()
			}
			return fmt.Errorf("copy %s to review: %w", key, err)
		}

		copySidecarSubtitle(logger, asset.Path, destPath)
		item.FinalFile = destPath
	}

	h.cleanupStaging(ctx, item)

	logger.Info("review routing completed", "event_type", "stage_complete", "stage", "organizing", "review_path", reviewPath)
	return nil
}

// validateEditionFilename verifies that the edition suffix appears in the
// final filename when edition metadata is present.
func validateEditionFilename(filename, edition string) error {
	expected := " - " + textutil.SanitizeDisplayName(edition)
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	if !strings.HasSuffix(base, expected) {
		return fmt.Errorf("edition validation failed: filename %q missing expected suffix %q", filename, expected)
	}
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
