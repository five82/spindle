package organizer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/deps"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/services/jellyfin"
	"spindle/internal/textutil"
)

// shouldRefreshJellyfin checks whether Jellyfin refresh is allowed and returns the reason.
func shouldRefreshJellyfin(cfg *config.Config) (bool, string) {
	if cfg == nil {
		return false, "config_unavailable"
	}
	if !cfg.Jellyfin.Enabled {
		return false, "disabled"
	}
	if strings.TrimSpace(cfg.Jellyfin.URL) == "" || strings.TrimSpace(cfg.Jellyfin.APIKey) == "" {
		return false, "missing_credentials"
	}
	return true, "configured"
}

// notificationTitle returns the best available title for notifications,
// falling back through metadata title, disc title, and finally the filename.
func notificationTitle(meta MetadataProvider, discTitle, finalPath string) string {
	if meta != nil {
		if title := strings.TrimSpace(meta.Title()); title != "" {
			return title
		}
	}
	if title := strings.TrimSpace(discTitle); title != "" {
		return title
	}
	return filepath.Base(finalPath)
}

// libraryUnavailableErrors lists syscall errors that indicate the library is unavailable.
var libraryUnavailableErrors = []error{
	syscall.ENODEV,
	syscall.ENOTCONN,
	syscall.EHOSTDOWN,
	syscall.EHOSTUNREACH,
	syscall.ETIMEDOUT,
	syscall.EIO,
	syscall.ESTALE,
}

// isLibraryUnavailable checks whether an error indicates the library filesystem is unavailable.
func isLibraryUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	for _, target := range libraryUnavailableErrors {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// sanitizeSlug converts input to a lowercase alphanumeric slug with hyphens.
// Spaces, underscores, periods, and hyphens become hyphens. Other characters are dropped.
// maxLen of 0 means unlimited length.
func sanitizeSlug(input string, maxLen int) string {
	input = strings.ToLower(strings.TrimSpace(input))
	var slug strings.Builder
	lastHyphen := false
	for _, r := range input {
		if maxLen > 0 && slug.Len() >= maxLen {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			slug.WriteRune(r)
			lastHyphen = false
		case r == ' ' || r == '-' || r == '_' || r == '.':
			if !lastHyphen {
				slug.WriteRune('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(slug.String(), "-")
}

// copyFile copies a file from src to dst, verifying both size and content hash.
func copyFile(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	srcSize := srcInfo.Size()

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	// Hash source while reading, hash destination while writing
	srcHasher := sha256.New()
	dstHasher := sha256.New()
	tee := io.TeeReader(in, srcHasher)
	multi := io.MultiWriter(out, dstHasher)

	written, err := io.Copy(multi, tee)
	if err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	if written != srcSize {
		_ = os.Remove(dst)
		return fmt.Errorf("copy size mismatch: source %d bytes, copied %d bytes", srcSize, written)
	}

	// Verify hashes match to detect corruption
	if !bytes.Equal(srcHasher.Sum(nil), dstHasher.Sum(nil)) {
		_ = os.Remove(dst)
		return fmt.Errorf("copy hash mismatch: file corrupted during copy")
	}

	return nil
}

// collectEncodedSources gathers all encoded file paths from the item and envelope.
func collectEncodedSources(item *queue.Item, env *ripspec.Envelope) []string {
	var sources []string
	if env != nil {
		for _, asset := range env.Assets.Encoded {
			if path := strings.TrimSpace(asset.Path); path != "" {
				sources = append(sources, path)
			}
		}
	}
	if len(sources) == 0 && item != nil {
		if path := strings.TrimSpace(item.EncodedFile); path != "" {
			sources = append(sources, path)
		}
	}
	return sources
}

// validateOrganizedArtifact validates that an organized file exists and is valid media.
// The edition parameter triggers edition filename validation when non-empty.
func (o *Organizer) validateOrganizedArtifact(ctx context.Context, path string, startedAt time.Time, edition string) error {
	logger := logging.WithContext(ctx, o.logger)
	clean := strings.TrimSpace(path)
	if clean == "" {
		logger.Error("organizer validation failed", logging.String("reason", "empty path"))
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate output",
			"Organization produced an empty target path",
			nil,
		)
	}
	info, err := os.Stat(clean)
	if err != nil {
		logger.Error("organizer validation failed", logging.String("reason", "stat failure"), logging.Error(err))
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate output",
			"Failed to stat organized file",
			err,
		)
	}
	if info.IsDir() {
		logger.Error("organizer validation failed", logging.String("reason", "path is directory"), logging.String("final_file", clean))
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate output",
			"Organized artifact points to a directory",
			nil,
		)
	}
	if info.Size() < minOrganizedFileSizeBytes {
		logger.Error(
			"organizer validation failed",
			logging.String("reason", "file too small"),
			logging.Int64("size_bytes", info.Size()),
		)
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate output",
			fmt.Sprintf("Organized file %q is unexpectedly small (%d bytes)", clean, info.Size()),
			nil,
		)
	}

	binary := "ffprobe"
	if o.cfg != nil {
		binary = deps.ResolveFFprobePath(o.cfg.FFprobeBinary())
	}
	probe, err := organizerProbe(ctx, binary, clean)
	if err != nil {
		logger.Error("organizer validation failed", logging.String("reason", "ffprobe"), logging.Error(err))
		return services.Wrap(
			services.ErrExternalTool,
			"organizing",
			"ffprobe validation",
			"Failed to inspect organized file with ffprobe",
			err,
		)
	}
	if probe.VideoStreamCount() == 0 {
		logger.Error("organizer validation failed", logging.String("reason", "no video stream"))
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate video stream",
			"Organized file does not contain a video stream",
			nil,
		)
	}
	if probe.AudioStreamCount() == 0 {
		logger.Error("organizer validation failed", logging.String("reason", "no audio stream"))
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate audio stream",
			"Organized file does not contain an audio stream",
			nil,
		)
	}
	duration := probe.DurationSeconds()
	if duration <= 0 {
		logger.Error("organizer validation failed", logging.String("reason", "invalid duration"))
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate duration",
			"Organized file duration could not be determined",
			nil,
		)
	}

	// Validate edition filename if edition metadata is present
	if err := ValidateEditionFilename(clean, edition, logger); err != nil {
		return err
	}

	logger.Debug(
		"organizer validation succeeded",
		logging.String("final_file", clean),
		logging.Duration("elapsed", time.Since(startedAt)),
		logging.Group("ffprobe",
			logging.Float64("duration_seconds", duration),
			logging.Int("video_streams", probe.VideoStreamCount()),
			logging.Int("audio_streams", probe.AudioStreamCount()),
		),
	)
	return nil
}

// cleanupStaging removes the staging directory for an item.
func (o *Organizer) cleanupStaging(ctx context.Context, item *queue.Item) {
	if item == nil || o.cfg == nil {
		return
	}
	base := strings.TrimSpace(o.cfg.Paths.StagingDir)
	if base == "" {
		return
	}
	root := strings.TrimSpace(item.StagingRoot(base))
	if root == "" {
		return
	}
	logger := logging.WithContext(ctx, o.logger)
	if err := os.RemoveAll(root); err != nil {
		logger.Warn("failed to clean staging directory; leftover files remain",
			logging.String("staging_root", root),
			logging.Error(err),
			logging.String(logging.FieldEventType, "staging_cleanup_failed"),
			logging.String(logging.FieldErrorHint, "check staging_dir permissions"),
			logging.String(logging.FieldImpact, "disk space not reclaimed; manual cleanup needed"),
		)
		return
	}
	logger.Debug("cleaned staging directory", logging.String("staging_root", root))
}

// updateProgress updates the item's progress in the queue store.
func (o *Organizer) updateProgress(ctx context.Context, item *queue.Item, message string, percent float64) {
	o.updateProgressWithBytes(ctx, item, message, percent, 0, 0)
}

// updateProgressWithBytes updates the item's progress including byte copy tracking.
func (o *Organizer) updateProgressWithBytes(ctx context.Context, item *queue.Item, message string, percent float64, bytesCopied, totalBytes int64) {
	logger := logging.WithContext(ctx, o.logger)
	copy := *item
	copy.ProgressMessage = message
	copy.ProgressPercent = percent
	copy.ProgressBytesCopied = bytesCopied
	copy.ProgressTotalBytes = totalBytes
	if err := o.store.UpdateProgress(ctx, &copy); err != nil {
		logger.Warn("failed to persist organizer progress; queue status may lag",
			logging.Error(err),
			logging.String(logging.FieldEventType, "queue_progress_persist_failed"),
			logging.String(logging.FieldErrorHint, "check queue database access"),
			logging.String(logging.FieldImpact, "queue UI may show stale progress"),
		)
		return
	}
	*item = copy
}

// logReviewDecision logs a review routing decision with consistent fields.
func logReviewDecision(logger *slog.Logger, result, reason string) {
	logger.Info(
		"organizer review decision",
		logging.String(logging.FieldDecisionType, "organizer_review_routing"),
		logging.String("decision_result", result),
		logging.String("decision_reason", reason),
		logging.String("decision_options", "organize, review"),
	)
}

// logJellyfinRefreshDecision logs a Jellyfin refresh decision with consistent fields.
func logJellyfinRefreshDecision(logger *slog.Logger, allowed bool, reason, scope string) {
	logger.Info(
		"jellyfin refresh decision",
		logging.String(logging.FieldDecisionType, "jellyfin_refresh"),
		logging.String("decision_result", textutil.Ternary(allowed, "refresh", "skip")),
		logging.String("decision_reason", reason),
		logging.String("decision_options", "refresh, skip"),
		logging.String("decision_scope", scope),
	)
}

// tryJellyfinRefresh attempts a Jellyfin library refresh and logs the result.
// Returns true if the refresh succeeded.
func (o *Organizer) tryJellyfinRefresh(ctx context.Context, logger *slog.Logger, meta jellyfin.MediaMetadata) bool {
	if err := o.jellyfin.Refresh(ctx, meta); err != nil {
		logger.Warn("jellyfin refresh failed; library scan may be stale",
			logging.Error(err),
			logging.String(logging.FieldEventType, "jellyfin_refresh_failed"),
			logging.String(logging.FieldErrorHint, "check jellyfin.url and jellyfin.api_key"),
			logging.String(logging.FieldImpact, "new media may not appear in Jellyfin until next scan"),
		)
		return false
	}
	return true
}

// logLibraryUnavailable logs a library unavailable warning with consistent fields.
func logLibraryUnavailable(logger *slog.Logger, err error) {
	logger.Warn("library unavailable; moving to review directory",
		logging.Error(err),
		logging.String(logging.FieldEventType, "library_unavailable"),
		logging.String(logging.FieldErrorHint, "check library_dir mount and Jellyfin configuration"),
		logging.String(logging.FieldImpact, "item routed to review directory for manual handling"),
	)
}

// persistRipSpec encodes and persists the ripspec envelope to the queue item.
func (o *Organizer) persistRipSpec(ctx context.Context, item *queue.Item, env *ripspec.Envelope, logger *slog.Logger) {
	if env == nil {
		return
	}
	if err := queue.PersistRipSpec(ctx, o.store, item, env); err != nil {
		logger.Warn("failed to persist rip spec after episode organize; metadata may be stale",
			logging.Error(err),
			logging.String(logging.FieldEventType, "rip_spec_persist_failed"),
			logging.String(logging.FieldErrorHint, "rerun identification or check queue database access"),
			logging.String(logging.FieldImpact, "episode metadata may not reflect latest state"),
		)
	}
}
