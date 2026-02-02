package organizer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"log/slog"

	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/services/jellyfin"
)

// finishReview moves encoded files to the review directory and marks the item complete.
func (o *Organizer) finishReview(ctx context.Context, item *queue.Item, stageStart time.Time, reason string, sources []string, detailErr error) error {
	if item == nil {
		return services.Wrap(services.ErrValidation, "organizing", "move to review", "Queue item unavailable for review routing", nil)
	}
	logger := logging.WithContext(ctx, o.logger)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Manual review required"
	}
	item.NeedsReview = true
	item.ReviewReason = reason

	if len(sources) == 0 && strings.TrimSpace(item.EncodedFile) != "" {
		sources = []string{item.EncodedFile}
	}

	var moved []string
	for _, source := range sources {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		target, err := o.movePathToReview(ctx, item, source)
		if err != nil {
			return err
		}
		moved = append(moved, target)
	}
	if len(moved) == 0 {
		return services.Wrap(services.ErrValidation, "organizing", "move to review", "No encoded files available to move to review directory", nil)
	}

	item.FinalFile = moved[len(moved)-1]
	item.EncodedFile = item.FinalFile
	item.Status = queue.StatusCompleted
	item.ProgressStage = "Manual review"
	item.ProgressPercent = 100
	item.ActiveEpisodeKey = ""
	if len(moved) == 1 {
		item.ProgressMessage = fmt.Sprintf("Moved to review directory: %s", filepath.Base(item.FinalFile))
	} else {
		item.ProgressMessage = fmt.Sprintf("Moved %d files to review directory", len(moved))
	}
	if strings.TrimSpace(item.ErrorMessage) == "" {
		if detailErr != nil {
			item.ErrorMessage = fmt.Sprintf("%s: %v", reason, detailErr)
		} else {
			item.ErrorMessage = reason
		}
	}

	if o.notifier != nil {
		label := filepath.Base(item.FinalFile)
		payload := notifications.Payload{
			"filename": label,
			"reason":   strings.TrimSpace(item.ReviewReason),
		}
		if len(moved) > 1 {
			payload["count"] = len(moved)
		}
		if err := o.notifier.Publish(ctx, notifications.EventUnidentifiedMedia, payload); err != nil {
			logger.Debug("review notification failed", logging.Error(err))
		}
	}

	for _, reviewPath := range moved {
		if err := o.validateOrganizedArtifact(ctx, reviewPath, stageStart); err != nil {
			return err
		}
	}
	o.cleanupStaging(ctx, item)
	return nil
}

// movePathToReview moves a single file to the review directory.
func (o *Organizer) movePathToReview(ctx context.Context, item *queue.Item, sourcePath string) (string, error) {
	logger := logging.WithContext(ctx, o.logger)
	logger.Debug(
		"moving encoded file to review",
		logging.String("encoded_file", strings.TrimSpace(sourcePath)),
		logging.String("disc_title", strings.TrimSpace(item.DiscTitle)),
	)
	reviewDir := strings.TrimSpace(o.cfg.Paths.ReviewDir)
	if reviewDir == "" {
		return "", services.Wrap(
			services.ErrConfiguration,
			"organizing",
			"resolve review dir",
			"Review directory not configured; set review_dir in your spindle config.toml",
			nil,
		)
	}
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		return "", services.Wrap(services.ErrConfiguration, "organizing", "ensure review dir", "Failed to create review directory", err)
	}
	ext := filepath.Ext(sourcePath)
	if ext == "" {
		ext = ".mkv"
	}
	prefix := reviewFilenamePrefix(item)
	target, err := o.nextReviewPath(reviewDir, prefix, ext)
	if err != nil {
		return "", services.Wrap(services.ErrTransient, "organizing", "allocate review filename", "Unable to allocate review filename", err)
	}
	if err := moveOrCopyFile(logger, sourcePath, target); err != nil {
		return "", err
	}
	return target, nil
}

// moveOrCopyFile attempts to rename a file, falling back to copy+delete for cross-device moves.
func moveOrCopyFile(logger *slog.Logger, source, target string) error {
	renameErr := os.Rename(source, target)
	if renameErr == nil {
		return nil
	}

	// Handle file exists - allocate a new name
	if errors.Is(renameErr, os.ErrExist) {
		return services.Wrap(services.ErrTransient, "organizing", "move review file", "Target file already exists", renameErr)
	}

	// Handle cross-device moves
	var linkErr *os.LinkError
	if errors.As(renameErr, &linkErr) && errors.Is(linkErr.Err, syscall.EXDEV) {
		if copyErr := copyFile(source, target); copyErr != nil {
			return services.Wrap(services.ErrTransient, "organizing", "copy review file", "Failed to copy file into review directory", copyErr)
		}
		if err := os.Remove(source); err != nil {
			logger.Warn("failed to remove source file after copy; duplicate files remain",
				logging.Error(err),
				logging.String(logging.FieldEventType, "review_source_cleanup_failed"),
				logging.String(logging.FieldErrorHint, "manually delete the staging file if needed"),
				logging.String(logging.FieldImpact, "duplicate file exists in staging; manual cleanup needed"),
			)
		}
		return nil
	}

	return services.Wrap(services.ErrTransient, "organizing", "move review file", "Failed to move file into review directory", renameErr)
}

// nextReviewPath finds the next available filename in the review directory.
func (o *Organizer) nextReviewPath(dir, prefix, ext string) (string, error) {
	const maxAttempts = 10000
	if strings.TrimSpace(prefix) == "" {
		prefix = "unidentified"
	}
	if ext == "" {
		ext = ".mkv"
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		name := fmt.Sprintf("%s-%d%s", prefix, attempt, ext)
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return candidate, nil
			}
			return "", err
		}
	}
	return "", fmt.Errorf("exhausted review filename slots in %s", dir)
}

// reviewFilenamePrefix generates a prefix for review filenames based on the review reason.
func reviewFilenamePrefix(item *queue.Item) string {
	reason := strings.TrimSpace(item.ReviewReason)
	if reason == "" {
		reason = "unidentified"
	}
	result := sanitizeSlug(reason, 0)
	if result == "" {
		result = "unidentified"
	}
	if fpSlug := sanitizeSlug(item.DiscFingerprint, 8); fpSlug != "" {
		return result + "-" + fpSlug
	}
	return result
}

// handleLibraryUnavailable logs the unavailable library and routes to review.
func (o *Organizer) handleLibraryUnavailable(ctx context.Context, item *queue.Item, stageStart time.Time, env *ripspec.Envelope, err error) error {
	logger := logging.WithContext(ctx, o.logger)
	logReviewDecision(logger, "review", "library_unavailable")
	logLibraryUnavailable(logger, err)
	return o.finishReview(ctx, item, stageStart, "Library unavailable", collectEncodedSources(item, env), err)
}

// organizeToLibrary performs the core library organization for a single file.
func (o *Organizer) organizeToLibrary(ctx context.Context, item *queue.Item, meta jellyfin.MediaMetadata, stageStart time.Time, env *ripspec.Envelope) error {
	logger := logging.WithContext(ctx, o.logger)

	o.updateProgress(ctx, item, "Organizing library structure", 20)
	logger.Debug("organizing encoded file into library", logging.String("encoded_file", item.EncodedFile))

	targetPath, err := o.jellyfin.Organize(ctx, item.EncodedFile, meta)
	if err != nil {
		if isLibraryUnavailable(err) {
			return o.handleLibraryUnavailable(ctx, item, stageStart, env, err)
		}
		return services.Wrap(services.ErrExternalTool, "organizing", "move to library", "Failed to move media into library", err)
	}

	item.FinalFile = targetPath
	logger.Debug("library move completed", logging.String("final_file", targetPath))

	// Check if subtitles were muxed into MKV (skip sidecar move if so)
	subtitlesMuxed := false
	if env != nil && len(env.Assets.Subtitled) > 0 {
		// For single-file (movie), check the "primary" episode key
		if asset, ok := env.Assets.FindAsset("subtitled", "primary"); ok && asset.SubtitlesMuxed {
			subtitlesMuxed = true
		}
	}

	var subtitlesMoved int
	if subtitlesMuxed {
		logger.Info("subtitle sidecar move decision",
			logging.String(logging.FieldDecisionType, "subtitle_sidecar_move"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "subtitles_muxed_into_mkv"),
		)
	} else {
		var err error
		subtitlesMoved, err = o.moveGeneratedSubtitles(ctx, item, targetPath)
		if err != nil {
			logger.Warn("subtitle sidecar move failed; subtitles may be missing in library",
				logging.Error(err),
				logging.String(logging.FieldEventType, "subtitle_move_failed"),
				logging.String(logging.FieldErrorHint, "check library_dir permissions and subtitle file names"),
				logging.String(logging.FieldImpact, "subtitles will not appear in Jellyfin for this item"),
			)
		}

		// Check if subtitles were expected but missing (only when not muxed)
		if o.cfg != nil && o.cfg.Subtitles.Enabled && env != nil && len(env.Assets.Subtitled) > 0 && subtitlesMoved == 0 {
			logger.Warn("expected subtitles not found",
				logging.Int("expected_count", len(env.Assets.Subtitled)),
				logging.Int("moved_count", subtitlesMoved),
				logging.String(logging.FieldEventType, "subtitles_missing"),
				logging.String(logging.FieldErrorHint, "check subtitle generation logs"),
			)
			item.NeedsReview = true
			if item.ReviewReason == "" {
				item.ReviewReason = "expected subtitles missing"
			}
		}
	}

	if err := o.validateOrganizedArtifact(ctx, targetPath, stageStart); err != nil {
		return err
	}

	// Jellyfin refresh
	o.updateProgress(ctx, item, "Refreshing Jellyfin library", 80)
	refreshAllowed, refreshReason := shouldRefreshJellyfin(o.cfg)
	if o.jellyfin == nil {
		refreshAllowed = false
		refreshReason = "service_unavailable"
	}
	logJellyfinRefreshDecision(logger, refreshAllowed, refreshReason, "item")

	jellyfinRefreshed := false
	if refreshAllowed {
		if o.tryJellyfinRefresh(ctx, logger, meta) {
			logger.Debug("jellyfin library refresh requested", logging.String("title", strings.TrimSpace(meta.Title())))
			jellyfinRefreshed = true
		}
	}

	// Finalize
	o.updateProgress(ctx, item, "Organization completed", 100)
	item.ProgressMessage = fmt.Sprintf("Available in library: %s", filepath.Base(targetPath))

	// Calculate resource metrics
	var finalFileSize int64
	if info, err := os.Stat(targetPath); err == nil {
		finalFileSize = info.Size()
	}

	// Log stage summary
	logger.Info(
		"organizing stage summary",
		logging.String(logging.FieldEventType, "stage_complete"),
		logging.String("final_file", targetPath),
		logging.Duration("stage_duration", time.Since(stageStart)),
		logging.Int64("final_file_size_bytes", finalFileSize),
		logging.String("media_title", strings.TrimSpace(meta.Title())),
		logging.Bool("is_movie", meta.IsMovie()),
	)

	if o.notifier != nil {
		title := notificationTitle(meta, item.DiscTitle, targetPath)
		o.publishCompletionNotifications(ctx, logger, title, targetPath, jellyfinRefreshed, 0, 0)
	}

	o.cleanupStaging(ctx, item)
	return nil
}
