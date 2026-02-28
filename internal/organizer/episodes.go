package organizer

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services"
)

// organizeJob represents a single episode to organize.
type organizeJob struct {
	Episode  ripspec.Episode
	Source   string
	Metadata queue.Metadata
}

// buildOrganizeJobs creates organize jobs from the ripspec envelope.
func buildOrganizeJobs(env ripspec.Envelope, base queue.Metadata) ([]organizeJob, error) {
	if len(env.Episodes) == 0 {
		return nil, nil
	}
	show := strings.TrimSpace(base.ShowTitle)
	if show == "" {
		show = strings.TrimSpace(base.Title())
	}
	if show == "" {
		show = "Manual Import"
	}
	jobs := make([]organizeJob, 0, len(env.Episodes))
	for _, ep := range env.Episodes {
		asset, ok := env.Assets.FindAsset("encoded", ep.Key)
		if !ok || strings.TrimSpace(asset.Path) == "" {
			return nil, fmt.Errorf("missing encoded asset for %s", ep.Key)
		}
		display := fmt.Sprintf("%s Season %02d", show, ep.Season)
		meta := queue.NewTVMetadata(show, ep.Season, []int{ep.Episode}, display)
		jobs = append(jobs, organizeJob{Episode: ep, Source: asset.Path, Metadata: meta})
	}
	return jobs, nil
}

const maxLoggedOrganizeJobs = 6

// appendOrganizeJobLines adds organize job details to logging attributes.
func appendOrganizeJobLines(attrs []logging.Attr, jobs []organizeJob) []logging.Attr {
	if len(jobs) == 0 {
		return attrs
	}
	limit := min(len(jobs), maxLoggedOrganizeJobs)
	if len(jobs) > maxLoggedOrganizeJobs {
		attrs = append(attrs, logging.Int("job_hidden_count", len(jobs)-limit))
	}
	for idx := range limit {
		attrs = append(attrs, logging.String(fmt.Sprintf("job_%d", idx+1), formatOrganizeJobValue(jobs[idx])))
	}
	return attrs
}

func formatOrganizeJobValue(job organizeJob) string {
	key := strings.TrimSpace(job.Episode.Key)
	if key == "" {
		key = fmt.Sprintf("S%02dE%02d", job.Episode.Season, job.Episode.Episode)
	}
	source := filepath.Base(job.Source)
	if source == "" {
		source = "unknown"
	}
	return fmt.Sprintf("%s | %s", strings.ToUpper(key), source)
}

// organizeEpisodes organizes multiple TV episodes into the library.
func (o *Organizer) organizeEpisodes(ctx context.Context, item *queue.Item, env *ripspec.Envelope, jobs []organizeJob, logger *slog.Logger, stageStarted time.Time) error {
	// Partition: resolved episodes go to library, unresolved go to review.
	var resolvedJobs []organizeJob
	var unresolvedSources []string
	for _, job := range jobs {
		if job.Episode.Episode > 0 {
			resolvedJobs = append(resolvedJobs, job)
		} else {
			unresolvedSources = append(unresolvedSources, job.Source)
		}
	}

	finalPaths := make([]string, 0, len(resolvedJobs))
	step := 80.0 / float64(max(len(resolvedJobs), 1))

	refreshAllowed, refreshReason := shouldRefreshJellyfin(o.cfg)
	if o.jellyfin == nil {
		refreshAllowed = false
		refreshReason = "service_unavailable"
	}
	logJellyfinRefreshDecision(logger, refreshAllowed, refreshReason, "batch_after_all_episodes")

	var (
		skipped        int
		failedEpisodes int
		lastErr        error
	)

	for idx, job := range resolvedJobs {
		finalPath, err := o.processEpisode(ctx, item, env, job, idx, len(resolvedJobs), skipped, step, logger, stageStarted)
		if err != nil {
			// Check for library unavailable (terminal condition)
			if isLibraryUnavailable(err) {
				return o.handleLibraryUnavailable(ctx, item, stageStarted, env, err)
			}
			// Record per-episode failure and continue
			failedEpisodes++
			lastErr = err
			continue
		}
		if finalPath == "" {
			// Episode was skipped (already organized)
			skipped++
			continue
		}
		finalPaths = append(finalPaths, finalPath)
	}

	// Only fail if no episodes were organized successfully
	if len(finalPaths) == 0 && lastErr != nil {
		return services.Wrap(
			services.ErrExternalTool,
			"organizing",
			"move to library",
			fmt.Sprintf("All %d episode(s) failed organization", len(resolvedJobs)-skipped),
			lastErr,
		)
	}

	// Route unresolved episodes to review directory.
	var reviewedCount int
	for _, src := range unresolvedSources {
		if _, err := o.movePathToReview(ctx, item, src); err != nil {
			logger.Warn("failed to move unresolved episode to review",
				logging.String("source", src),
				logging.Error(err),
				logging.String(logging.FieldEventType, "unresolved_episode_review_failed"),
				logging.String(logging.FieldImpact, "unresolved episode remains in staging"),
				logging.String(logging.FieldErrorHint, "check review_dir permissions"),
			)
		} else {
			reviewedCount++
		}
	}
	if reviewedCount > 0 {
		item.NeedsReview = true
		if item.ReviewReason == "" {
			item.ReviewReason = fmt.Sprintf(
				"%d unresolved episode(s) moved to review", reviewedCount)
		}
	}

	// Persist final rip spec state
	if env != nil {
		if encoded, err := env.Encode(); err == nil {
			item.RipSpecData = encoded
		} else {
			logger.Warn("failed to encode rip spec after organizing; metadata may be stale",
				logging.Error(err),
				logging.String(logging.FieldEventType, "rip_spec_encode_failed"),
				logging.String(logging.FieldErrorHint, "rerun identification if rip spec data looks wrong"),
				logging.String(logging.FieldImpact, "organize metadata may not reflect latest state"),
			)
		}
	}

	// Batch Jellyfin refresh once after all episodes are organized
	jellyfinRefreshed := false
	if refreshAllowed && len(finalPaths) > 0 {
		if err := o.jellyfin.Refresh(ctx, resolvedJobs[0].Metadata); err != nil {
			logger.Warn("jellyfin refresh failed; library scan may be stale",
				logging.Error(err),
				logging.String(logging.FieldEventType, "jellyfin_refresh_failed"),
				logging.String(logging.FieldErrorHint, "check jellyfin.url and jellyfin.api_key"),
				logging.String(logging.FieldImpact, "new media may not appear in Jellyfin until next scan"),
			)
		} else {
			jellyfinRefreshed = true
			logger.Debug("jellyfin library refresh requested (batch)",
				logging.Int("organized_episodes", len(finalPaths)),
			)
		}
	}

	if len(finalPaths) > 0 {
		item.FinalFile = finalPaths[len(finalPaths)-1]
	}
	item.ProgressStage = "Organizing"
	item.ProgressPercent = 100
	item.ActiveEpisodeKey = ""
	switch {
	case failedEpisodes > 0 && reviewedCount > 0:
		item.ProgressMessage = fmt.Sprintf("Available in library (%d episodes, %d failed, %d to review)", len(finalPaths), failedEpisodes, reviewedCount)
	case failedEpisodes > 0:
		item.ProgressMessage = fmt.Sprintf("Available in library (%d episodes, %d failed)", len(finalPaths), failedEpisodes)
	case reviewedCount > 0:
		item.ProgressMessage = fmt.Sprintf("Available in library (%d episodes, %d to review)", len(finalPaths), reviewedCount)
	default:
		item.ProgressMessage = fmt.Sprintf("Available in library (%d episodes)", len(finalPaths))
	}
	if failedEpisodes > 0 {
		item.NeedsReview = true
		if item.ReviewReason == "" {
			item.ReviewReason = fmt.Sprintf("%d episode(s) failed organization", failedEpisodes)
		}
	}

	// Log final organization summary
	expected, _, _, final := env.AssetCounts()
	logger.Info("organization stage summary",
		logging.String(logging.FieldEventType, "stage_complete"),
		logging.Duration("stage_duration", time.Since(stageStarted)),
		logging.Int("expected_episodes", expected),
		logging.Int("organized_episodes", len(finalPaths)),
		logging.Int("failed_episodes", failedEpisodes),
		logging.Int("skipped_episodes", skipped),
		logging.Int("reviewed_episodes", reviewedCount),
		logging.Int("final_asset_count", final),
	)

	o.publishCompletionNotifications(ctx, logger, strings.TrimSpace(item.DiscTitle), item.FinalFile, jellyfinRefreshed, len(finalPaths), failedEpisodes)
	o.cleanupStaging(ctx, item)
	return nil
}

// processEpisode handles organization of a single episode.
// Returns the final path if organized, empty string if skipped, or error if failed.
func (o *Organizer) processEpisode(ctx context.Context, item *queue.Item, env *ripspec.Envelope, job organizeJob, idx, totalJobs, skipped int, step float64, logger *slog.Logger, stageStarted time.Time) (string, error) {
	episodeKey := strings.ToLower(strings.TrimSpace(job.Episode.Key))
	label := fmt.Sprintf("S%02dE%02d", job.Episode.Season, job.Episode.Episode)

	// Skip already-organized episodes (enables resume after partial failure)
	if asset, ok := env.Assets.FindAsset("final", episodeKey); ok && asset.IsCompleted() {
		logger.Info("episode organization decision",
			logging.String(logging.FieldDecisionType, "episode_organization"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "already_organized"),
			logging.String("episode_key", episodeKey),
			logging.String("final_path", asset.Path),
		)
		return "", nil // empty string signals skip
	}

	item.ActiveEpisodeKey = episodeKey
	remaining := totalJobs - skipped
	current := idx + 1 - skipped
	o.updateProgress(ctx, item, fmt.Sprintf("Organizing %s (%d/%d)", label, current, remaining), step*float64(idx))

	targetPath, err := o.jellyfin.Organize(ctx, job.Source, job.Metadata)
	if err != nil {
		if isLibraryUnavailable(err) {
			return "", err // propagate library unavailable for special handling
		}
		// Record per-episode failure
		logger.Error("episode organization failed",
			logging.String("episode_key", episodeKey),
			logging.Error(err),
			logging.String(logging.FieldEventType, "episode_organize_failed"),
		)
		o.recordEpisodeAsset(ctx, item, env, job.Episode, "", ripspec.AssetStatusFailed, err.Error(), logger)
		return "", err
	}

	logger.Debug(
		"organized episode into library",
		logging.String("episode_label", label),
		logging.String("source_file", strings.TrimSpace(job.Source)),
		logging.String("final_file", targetPath),
	)
	o.recordEpisodeAsset(ctx, item, env, job.Episode, targetPath, ripspec.AssetStatusCompleted, "", logger)

	if err := o.validateOrganizedArtifact(ctx, targetPath, stageStarted, ""); err != nil {
		// Validation failure is critical - record and return error
		logger.Error("episode validation failed",
			logging.String("episode_key", episodeKey),
			logging.Error(err),
			logging.String(logging.FieldEventType, "episode_validation_failed"),
		)
		o.recordEpisodeAsset(ctx, item, env, job.Episode, targetPath, ripspec.AssetStatusFailed, err.Error(), logger)
		return "", err
	}

	// Move subtitles for this episode unless already muxed into MKV
	if o.cfg != nil && o.cfg.Subtitles.MuxIntoMKV {
		logger.Info("subtitle sidecar move decision",
			logging.String(logging.FieldDecisionType, "subtitle_sidecar_move"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "subtitles_muxed_into_mkv"),
			logging.String("episode_key", episodeKey),
		)
	} else {
		itemCopy := *item
		itemCopy.EncodedFile = job.Source
		if _, err := o.moveGeneratedSubtitles(ctx, &itemCopy, targetPath); err != nil {
			logger.Warn("subtitle sidecar move failed; subtitles may be missing in library",
				logging.Error(err),
				logging.String(logging.FieldEventType, "subtitle_move_failed"),
				logging.String(logging.FieldErrorHint, "check library_dir permissions and subtitle file names"),
				logging.String(logging.FieldImpact, "subtitles will not appear in Jellyfin for this episode"),
			)
		}
	}

	return targetPath, nil
}

// recordEpisodeAsset records an episode's final asset status and persists the ripspec.
func (o *Organizer) recordEpisodeAsset(ctx context.Context, item *queue.Item, env *ripspec.Envelope, episode ripspec.Episode, path, status, errorMsg string, logger *slog.Logger) {
	if env == nil {
		return
	}
	env.Assets.AddAsset("final", ripspec.Asset{
		EpisodeKey: episode.Key,
		TitleID:    episode.TitleID,
		Path:       path,
		Status:     status,
		ErrorMsg:   errorMsg,
	})
	o.persistRipSpec(ctx, item, env, logger)
}
