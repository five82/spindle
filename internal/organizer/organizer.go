package organizer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/fileutil"
	"github.com/five82/spindle/internal/jellyfin"
	"github.com/five82/spindle/internal/language"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/mediameta"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/textutil"
)

const reviewReasonDirMaxBytes = 96

const copyProgressLogInterval = 3 * time.Minute

// Handler implements stage.Handler for organization.
type Handler struct {
	cfg      *config.Config
	jfClient *jellyfin.Client
	notifier *notify.Notifier
}

// New creates an organization handler.
func New(cfg *config.Config, jfClient *jellyfin.Client, notifier *notify.Notifier) *Handler {
	return &Handler{cfg: cfg, jfClient: jfClient, notifier: notifier}
}

// Run executes the organization stage.
func (h *Handler) Run(ctx context.Context, sess *stage.Session) error {
	item := sess.Item
	logger := sess.Logger
	env := sess.Env

	meta := mediameta.FromJSON(item.MetadataJSON, item.DiscTitle)
	keys := env.AssetKeys()

	logger.Info("organization plan",
		"event_type", "organization_plan",
		"asset_keys", len(keys),
		"media_type", env.Metadata.MediaType,
		"needs_review", item.NeedsReview == 1,
	)

	libraryCount := 0
	reviewCount := 0

	if item.NeedsReview == 1 {
		if env.Metadata.MediaType != "tv" || !ripspec.HasResolvedEpisodes(env.Episodes) {
			logger.Info("item routed to review",
				"decision_type", logs.DecisionOrganizeRoute,
				"decision_result", "review",
				"decision_reason", "needs_review flag set with no clean resolved tv episodes",
			)
			if err := h.routeToReview(ctx, logger, sess, &meta, keys); err != nil {
				return err
			}
			reviewCount = len(keys)
			h.sendTerminalNotification(ctx, logger, sess, libraryCount, reviewCount)
			return nil
		}

		libraryKeys, reviewKeys := partitionTVOrganizationKeys(env)
		if len(libraryKeys) == 0 {
			logger.Info("item routed to review",
				"decision_type", logs.DecisionOrganizeRoute,
				"decision_result", "review",
				"decision_reason", "all resolved episodes flagged for review",
			)
			if err := h.routeToReview(ctx, logger, sess, &meta, reviewKeys); err != nil {
				return err
			}
			reviewCount = len(reviewKeys)
			h.sendTerminalNotification(ctx, logger, sess, libraryCount, reviewCount)
			return nil
		}
		logger.Info("item partially organized",
			"decision_type", logs.DecisionOrganizeRoute,
			"decision_result", "partial_library_review",
			"decision_reason", fmt.Sprintf("clean_episodes=%d review_episodes=%d", len(libraryKeys), len(reviewKeys)),
		)

		if _, err := h.placeInLibrary(ctx, logger, sess, &meta, libraryKeys); err != nil {
			return err
		}
		if len(reviewKeys) > 0 {
			if _, _, err := h.copyAssetsToDir(ctx, logger, sess, &meta, reviewPathForItem(h.cfg.Paths.ReviewDir, item), reviewKeys, "review"); err != nil {
				return err
			}
		}
		if err := sess.Save(); err != nil {
			return err
		}
		libraryCount = len(libraryKeys)
		reviewCount = len(reviewKeys)
		sess.Progress(100, fmt.Sprintf("Available in library (%d episodes, %d to review)", libraryCount, reviewCount))
	} else {
		copied, err := h.placeInLibrary(ctx, logger, sess, &meta, keys)
		if err != nil {
			return err
		}
		libraryCount = copied
		if err := sess.Save(); err != nil {
			return err
		}
	}

	return h.finalize(ctx, logger, sess, libraryCount, reviewCount)
}

// placeInLibrary copies the given asset keys into the resolved library
// destination (task: organize). It resolves the library path from metadata,
// ensures the directory exists, and runs the per-asset verified copy loop.
func (h *Handler) placeInLibrary(
	ctx context.Context,
	logger *slog.Logger,
	sess *stage.Session,
	meta *mediameta.Metadata,
	keys []string,
) (int, error) {
	libraryPath, err := meta.LibraryPath(
		h.cfg.Paths.LibraryDir,
		h.cfg.Library.MoviesDir,
		h.cfg.Library.TVDir,
	)
	if err != nil {
		return 0, fmt.Errorf("resolve library path: %w", err)
	}
	if err := os.MkdirAll(libraryPath, 0o755); err != nil {
		return 0, fmt.Errorf("create library dir: %w", err)
	}
	_, copied, err := h.copyAssetsToDir(ctx, logger, sess, meta, libraryPath, keys, "library")
	if err != nil {
		return 0, err
	}
	return copied, nil
}

// finalize performs the item-level completion work after all assets are
// placed (task: finalize): Jellyfin refresh, terminal notification, staging
// cleanup, and the stage completion log.
func (h *Handler) finalize(ctx context.Context, logger *slog.Logger, sess *stage.Session, libraryCount, reviewCount int) error {
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

	h.sendTerminalNotification(ctx, logger, sess, libraryCount, reviewCount)
	h.cleanupStaging(logger, sess.Item)
	return nil
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
	reason := reviewReasonDirSegment(item)
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

func reviewReasonDirSegment(item *queue.Item) string {
	var raw string
	if item != nil {
		raw = strings.TrimSpace(item.PrimaryReviewReason())
		if raw == "" {
			raw = strings.TrimSpace(item.ReviewReason)
		}
	}
	if raw == "" {
		return "manual-review"
	}
	reason := textutil.SanitizePathSegment(raw)
	reason = truncatePathSegmentBytes(reason, reviewReasonDirMaxBytes)
	if reason == "" {
		return "manual-review"
	}
	return reason
}

func truncatePathSegmentBytes(segment string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(segment) <= maxBytes {
		return segment
	}
	end := 0
	for i, r := range segment {
		next := i + utf8.RuneLen(r)
		if next > maxBytes {
			break
		}
		end = next
	}
	return strings.Trim(segment[:end], "-_")
}

func throttledProgressUpdater(sess *stage.Session, minInterval time.Duration) func() {
	var lastUpdate time.Time
	return func() {
		if sess == nil || sess.Task == nil {
			return
		}
		now := time.Now()
		if !lastUpdate.IsZero() && now.Sub(lastUpdate) < minInterval {
			return
		}
		lastUpdate = now
		sess.Progress(sess.Task.ProgressPercent, sess.Task.ProgressMessage,
			stage.WithProgressBytes(sess.Task.ProgressBytesCopied, sess.Task.ProgressTotalBytes))
	}
}

func moveOrCopyWithProgress(src, dst string, progress fileutil.ProgressFunc) error {
	if err := os.Rename(src, dst); err == nil {
		if progress != nil {
			if info, statErr := os.Stat(dst); statErr == nil {
				progress(fileutil.CopyProgress{BytesCopied: info.Size(), TotalBytes: info.Size()})
			}
		}
		return nil
	} else {
		var linkErr *os.LinkError
		if !errors.As(err, &linkErr) || !errors.Is(linkErr.Err, syscall.EXDEV) {
			return fmt.Errorf("move file: %w", err)
		}
	}
	if err := fileutil.CopyFileVerifiedWithProgress(src, dst, progress); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove source after copy: %w", err)
	}
	return nil
}

type organizationInput struct {
	key   string
	stage string
	asset ripspec.Asset
}

func organizationInputForKey(env *ripspec.Envelope, key string) (organizationInput, bool) {
	if env == nil {
		return organizationInput{}, false
	}
	if asset, ok := env.Assets.FindAsset(ripspec.AssetKindSubtitled, key); ok && asset.IsCompleted() {
		return organizationInput{key: key, stage: ripspec.AssetKindSubtitled, asset: asset}, true
	}
	if asset, ok := env.Assets.FindAsset(ripspec.AssetKindEncoded, key); ok && asset.IsCompleted() {
		return organizationInput{key: key, stage: ripspec.AssetKindEncoded, asset: asset}, true
	}
	return organizationInput{}, false
}

func (h *Handler) copyAssetsToDir(ctx context.Context, logger *slog.Logger, sess *stage.Session, meta *mediameta.Metadata, destDir string, keys []string, target string) (string, int, error) {
	env := sess.Env
	if len(keys) == 0 {
		return "", 0, nil
	}

	inputs := make([]organizationInput, 0, len(keys))
	for _, key := range keys {
		input, ok := organizationInputForKey(env, key)
		if !ok {
			err := fmt.Errorf("no completed subtitled or encoded asset for %s", key)
			logger.Error("missing or incomplete asset",
				"event_type", "organize_missing_asset",
				"error_hint", "pipeline output is incomplete; retry the failed item",
				"error", err,
			)
			return "", 0, err
		}
		reason := "completed subtitled asset available"
		if input.stage == ripspec.AssetKindEncoded {
			reason = "no completed subtitled asset; using encoded"
		}
		logger.Info("organization source stage selected",
			"decision_type", logs.DecisionSourceStageSelection,
			"decision_result", input.stage,
			"decision_reason", fmt.Sprintf("%s for episode_key=%s", reason, key),
		)
		inputs = append(inputs, input)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("create %s dir: %w", target, err)
	}

	totalBytes := totalOrganizationBytes(inputs)
	var completedBytes int64
	copied := 0
	lastPath := ""
	pushProgress := throttledProgressUpdater(sess, 250*time.Millisecond)
	for i, input := range inputs {
		if ctx.Err() != nil {
			return "", copied, ctx.Err()
		}
		key := input.key
		asset := input.asset

		var season, episode, episodeEnd int
		if ep := env.EpisodeByKey(key); ep != nil {
			season, episode, episodeEnd = ep.Season, ep.Episode, ep.EpisodeEnd
		}
		destName := mediameta.DestFilename(meta, key, filepath.Ext(asset.Path), season, episode, episodeEnd)
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
			"episode_key", key,
			"source_path", asset.Path,
			"dest_path", destPath,
		)
		sess.Progress(overallBytePercent(completedBytes, totalBytes), fmt.Sprintf("Phase %d/%d - Copying to %s (%s)", i+1, len(keys), target, key), stage.WithProgressBytes(completedBytes, totalBytes))

		transfer := fileutil.CopyFileVerifiedWithProgress
		if target == "review" {
			transfer = moveOrCopyWithProgress
		}
		copyStart := time.Now()
		var lastCopyLog time.Time
		if err := transfer(asset.Path, destPath, func(p fileutil.CopyProgress) {
			sess.Task.ProgressBytesCopied = completedBytes + p.BytesCopied
			sess.Task.ProgressTotalBytes = totalBytes
			sess.Task.ProgressPercent = overallBytePercent(sess.Task.ProgressBytesCopied, totalBytes)
			pushProgress()

			now := time.Now()
			if lastCopyLog.IsZero() || now.Sub(lastCopyLog) >= copyProgressLogInterval || p.BytesCopied >= p.TotalBytes {
				lastCopyLog = now
				logger.Info("copy progress",
					"event_type", "copy_progress",
					"episode_key", key,
					"organize_target", target,
					"bytes_copied", p.BytesCopied,
					"total_bytes", p.TotalBytes,
					"overall_bytes_copied", sess.Task.ProgressBytesCopied,
					"overall_total_bytes", totalBytes,
					"overall_percent", math.Round(sess.Task.ProgressPercent*10)/10,
				)
			}
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
			"duration_ms", time.Since(copyStart).Milliseconds(),
		)
		if asset.SubtitlesMuxed {
			if err := removeMuxedSubtitleSidecar(logger, env, key, destPath); err != nil {
				return "", copied, err
			}
		} else {
			copySidecarSubtitle(logger, asset.Path, destPath)
		}
		if err := sess.SaveAssetSuccess(ripspec.AssetKindFinal, ripspec.Asset{EpisodeKey: key, Path: destPath}); err != nil {
			return "", copied, err
		}
		lastPath = destPath
		copied++
		if info, statErr := os.Stat(asset.Path); statErr == nil {
			completedBytes += info.Size()
		}
		sess.Progress(overallBytePercent(completedBytes, totalBytes), sess.Task.ProgressMessage, stage.WithProgressBytes(completedBytes, totalBytes))
	}
	return lastPath, copied, nil
}

// routeToReview copies assets to the review directory for manual inspection.
// Directory structure: review_dir/{reason}_{fingerprint_prefix}/
func (h *Handler) routeToReview(ctx context.Context, logger *slog.Logger, sess *stage.Session, meta *mediameta.Metadata, keys []string) error {
	item := sess.Item
	reviewPath := reviewPathForItem(h.cfg.Paths.ReviewDir, item)
	logger.Info("routing to review",
		"decision_type", logs.DecisionOrganizeRoute,
		"decision_result", "review",
		"decision_reason", item.ReviewReason,
		"review_path", reviewPath,
	)

	if _, _, err := h.copyAssetsToDir(ctx, logger, sess, meta, reviewPath, keys, "review"); err != nil {
		return err
	}
	if err := sess.Save(); err != nil {
		return err
	}

	h.cleanupStaging(logger, item)
	return nil
}

// cleanupStaging removes the staging directory for a completed item.
// Failures are logged as warnings (non-fatal) — disk space reclamation is
// best-effort.
func (h *Handler) cleanupStaging(logger *slog.Logger, item *queue.Item) {
	if logger == nil {
		logger = slog.Default()
	}
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

func (h *Handler) sendTerminalNotification(ctx context.Context, logger *slog.Logger, sess *stage.Session, libraryCount, reviewCount int) {
	item := sess.Item
	displayTitle := item.DisplayTitle()
	if sess.Env != nil && sess.Env.Metadata.DiscNumber > 0 {
		displayTitle += fmt.Sprintf(" - Disc %d", sess.Env.Metadata.DiscNumber)
	}

	if reviewCount > 0 || item.NeedsReview == 1 {
		title := "Review needed: " + displayTitle
		var msg string
		switch {
		case libraryCount > 0 && reviewCount > 0:
			msg = fmt.Sprintf("Imported %s to the library; routed %s to review.", itemCount(libraryCount), itemCount(reviewCount))
		case libraryCount > 0:
			msg = fmt.Sprintf("Imported %s to the library, but review is still required.", itemCount(libraryCount))
		case reviewCount > 0:
			msg = fmt.Sprintf("Routed %s to review.", itemCount(reviewCount))
		default:
			msg = "Review is required before library import."
		}
		if reason := item.ReviewSummary(2); reason != "" {
			msg += "\nReason: " + reason
		}
		msg += subtitleSkipNote(sess.Env)
		_ = notify.SendLogged(ctx, h.notifier, logger, notify.EventReviewRequired, title, msg,
			"library_count", libraryCount,
			"review_count", reviewCount,
		)
		return
	}

	title := "Imported: " + displayTitle
	msg := fmt.Sprintf("Imported %s to the library.", itemCount(libraryCount))
	msg += subtitleSkipNote(sess.Env)
	_ = notify.SendLogged(ctx, h.notifier, logger, notify.EventPipelineComplete, title, msg,
		"library_count", libraryCount,
	)
}

// subtitleSkipNote lists episodes that completed without subtitles so the
// operator knows to generate them with the whisperx-subtitles agent skill.
func subtitleSkipNote(env *ripspec.Envelope) string {
	if env == nil {
		return ""
	}
	var keys []string
	for _, record := range env.Attributes.SubtitleGenerationResults {
		if strings.EqualFold(record.Source, "none") {
			keys = append(keys, record.EpisodeKey)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	// A movie's single "main" key is noise in a notification; only episode
	// lists are worth enumerating.
	if len(keys) == 1 && strings.EqualFold(keys[0], "main") {
		return "\nNo subtitles (generate with the whisperx-subtitles skill)"
	}
	return "\nNo subtitles: " + strings.Join(keys, ", ") + " (generate with the whisperx-subtitles skill)"
}

func itemCount(count int) string {
	word := "items"
	if count == 1 {
		word = "item"
	}
	return fmt.Sprintf("%d %s", count, word)
}

func totalOrganizationBytes(inputs []organizationInput) int64 {
	var total int64
	for _, input := range inputs {
		if info, err := os.Stat(input.asset.Path); err == nil {
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

// removeMuxedSubtitleSidecar removes a prior external copy of the subtitle
// now embedded in destVideo. Other-language sidecars are left untouched.
func removeMuxedSubtitleSidecar(logger *slog.Logger, env *ripspec.Envelope, key, destVideo string) error {
	lang := "en"
	for _, record := range env.Attributes.SubtitleGenerationResults {
		if strings.EqualFold(record.EpisodeKey, key) {
			if normalized := language.ToISO2(record.Language); normalized != "" {
				lang = normalized
			}
			break
		}
	}
	destBase := strings.TrimSuffix(destVideo, filepath.Ext(destVideo))
	sidecarPath := destBase + "." + lang + ".srt"
	if err := os.Remove(sidecarPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("remove obsolete subtitle sidecar %s: %w", sidecarPath, err)
	}
	logger.Info("obsolete subtitle sidecar removed",
		"decision_type", logs.DecisionSidecarSubtitleCopy,
		"decision_result", "removed",
		"decision_reason", "matching subtitle is embedded in replacement video",
		"path", sidecarPath,
	)
	return nil
}

// copySidecarSubtitle copies sidecar SRT files that share the source video's
// basename alongside the destination video.
func copySidecarSubtitle(logger *slog.Logger, srcVideo, destVideo string) {
	srcBase := strings.TrimSuffix(srcVideo, filepath.Ext(srcVideo))
	matches, err := filepath.Glob(srcBase + ".*.srt")
	if err != nil || len(matches) == 0 {
		logger.Info("sidecar subtitle not found, skipping",
			"decision_type", logs.DecisionSidecarSubtitleCopy,
			"decision_result", "skipped",
			"decision_reason", "source SRT does not exist",
		)
		return
	}

	destBase := strings.TrimSuffix(destVideo, filepath.Ext(destVideo))
	for _, srcSrt := range matches {
		suffix := strings.TrimPrefix(srcSrt, srcBase)
		destSrt := destBase + suffix
		if err := fileutil.CopyFile(srcSrt, destSrt); err != nil {
			logger.Warn("failed to copy sidecar subtitle",
				"event_type", "sidecar_copy_error",
				"error_hint", err.Error(),
				"impact", "subtitle file not available in library",
			)
		}
	}
}
