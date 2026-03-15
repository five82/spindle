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
	logger.Info("organization stage started", "event_type", "stage_start")

	env, err := stage.ParseRipSpec(item.RipSpecData)
	if err != nil {
		return err
	}

	meta := queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)

	// Check if item needs review routing instead of library placement.
	if item.NeedsReview == 1 {
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
	if _, ok := env.Assets.FindAsset("subtitled", keys[0]); !ok {
		sourceStage = "encoded"
	}

	// Copy each asset to library.
	for i, key := range keys {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		asset, ok := env.Assets.FindAsset(sourceStage, key)
		if !ok || !asset.IsCompleted() {
			continue
		}

		// Determine destination filename.
		destName := destFilename(&meta, key, filepath.Ext(asset.Path))
		destPath := filepath.Join(libraryPath, destName)

		// Check if exists and not overwriting.
		if !h.cfg.Library.OverwriteExisting {
			if _, err := os.Stat(destPath); err == nil {
				logger.Info("file exists, skipping",
					"decision_type", "organize_skip",
					"decision_result", "skipped",
					"decision_reason", "file already exists",
					"path", destPath,
				)
				continue
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

		// Record final asset.
		env.Assets.AddAsset("final", ripspec.Asset{
			EpisodeKey: key,
			Path:       destPath,
			Status:     "completed",
		})

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
		_ = h.notifier.Send(ctx, notify.EventOrganizeComplete,
			"Organization Complete",
			fmt.Sprintf("Organized %s to library", item.DiscTitle),
		)
	}

	logger.Info("organization stage completed", "event_type", "stage_complete")
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
		"decision_type", "organize_route",
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
	if len(keys) > 0 {
		if _, ok := env.Assets.FindAsset("subtitled", keys[0]); !ok {
			sourceStage = "encoded"
		}
	}

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
	}

	logger.Info("review routing completed", "event_type", "stage_complete", "review_path", reviewPath)
	return nil
}

// copySidecarSubtitle copies a .en.srt sidecar subtitle file alongside the
// destination video if one exists next to the source video.
func copySidecarSubtitle(logger *slog.Logger, srcVideo, destVideo string) {
	srcSrt := strings.TrimSuffix(srcVideo, filepath.Ext(srcVideo)) + ".en.srt"
	if _, err := os.Stat(srcSrt); err != nil {
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
