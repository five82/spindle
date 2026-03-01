package encoding

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"log/slog"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/textutil"
)

type encodeJobRunner struct {
	store  *queue.Store
	runner *draptoRunner
}

func newEncodeJobRunner(store *queue.Store, runner *draptoRunner) *encodeJobRunner {
	return &encodeJobRunner{store: store, runner: runner}
}

func (r *encodeJobRunner) Run(ctx context.Context, item *queue.Item, env *ripspec.Envelope, jobs []encodeJob, encodedDir string, logger *slog.Logger) ([]string, error) {
	encodedPaths := make([]string, 0, max(1, len(jobs)))
	if logger != nil {
		runnerAvailable := r != nil && r.runner != nil
		attrs := append(
			logging.DecisionAttrsWithOptions("encoding_runner",
				textutil.Ternary(runnerAvailable, "drapto", "placeholder"),
				textutil.Ternary(runnerAvailable, "drapto_client_configured", "drapto_client_unavailable"),
				"drapto, placeholder"),
			logging.Int("job_count", len(jobs)),
		)
		logger.Info("encoding runner decision", logging.Args(attrs...)...)
	}

	if len(jobs) > 0 {
		paths, err := r.encodeEpisodes(ctx, item, env, jobs, encodedDir, logger)
		if err != nil {
			return nil, err
		}
		encodedPaths = paths
	} else {
		path, err := r.encodeSingleFile(ctx, item, encodedDir, logger)
		if err != nil {
			return nil, err
		}
		encodedPaths = append(encodedPaths, path)
	}

	if len(encodedPaths) == 0 {
		return nil, services.Wrap(
			services.ErrValidation,
			"encoding",
			"locate encoded outputs",
			"No encoded artifacts were produced",
			nil,
		)
	}

	return encodedPaths, nil
}

func (r *encodeJobRunner) encodeEpisodes(ctx context.Context, item *queue.Item, env *ripspec.Envelope, jobs []encodeJob, encodedDir string, logger *slog.Logger) ([]string, error) {
	encodedPaths := make([]string, 0, len(jobs))
	var lastErr error
	skipped := 0

	for idx, job := range jobs {
		episodeKey := strings.ToLower(strings.TrimSpace(job.Episode.Key))
		label := fmt.Sprintf("S%02dE%02d", job.Episode.Season, job.Episode.Episode)

		// Skip already-completed episodes (enables resume after partial failure)
		if asset, ok := env.Assets.FindAsset(ripspec.AssetKindEncoded, episodeKey); ok && asset.IsCompleted() {
			attrs := append(logging.DecisionAttrs("episode_encoding", "skipped", "already_encoded"),
				logging.String("episode_key", episodeKey),
				logging.String("encoded_path", asset.Path),
			)
			logger.Info("episode encoding decision", logging.Args(attrs...)...)
			encodedPaths = append(encodedPaths, asset.Path)
			skipped++
			continue
		}

		item.ActiveEpisodeKey = episodeKey
		remaining := len(jobs) - skipped
		current := idx + 1 - skipped
		item.ProgressMessage = fmt.Sprintf("Starting encode %s (%d/%d)", label, current, remaining)
		item.ProgressPercent = 0
		if r.store != nil {
			if err := r.store.UpdateProgress(ctx, item); err != nil {
				logger.Warn("failed to persist encoding job start; queue status may lag",
					logging.Error(err),
					logging.String(logging.FieldEventType, "queue_progress_persist_failed"),
					logging.String(logging.FieldErrorHint, "check queue database access"),
					logging.String(logging.FieldImpact, "queue UI may show stale progress"),
				)
			}
		}

		sourcePath := job.Source
		path := ""
		if r.runner != nil {
			var err error
			path, err = r.runner.Encode(ctx, item, sourcePath, encodedDir, label, job.Episode.Key, idx+1, len(jobs), logger)
			if err != nil {
				r.recordEpisodeFailure(ctx, item, env, &job, episodeKey, err, "episode_encode_failed", "episode encoding failed", logger)
				lastErr = err
				continue
			}
		}

		finalPath, err := ensureEncodedOutput(path, job.Output, sourcePath)
		if err != nil {
			r.recordEpisodeFailure(ctx, item, env, &job, episodeKey, err, "episode_output_failed", "episode output finalization failed", logger)
			lastErr = err
			continue
		}

		env.Assets.AddAsset(ripspec.AssetKindEncoded, ripspec.Asset{
			EpisodeKey: job.Episode.Key,
			TitleID:    job.Episode.TitleID,
			Path:       finalPath,
			Status:     ripspec.AssetStatusCompleted,
		})
		encodedPaths = append(encodedPaths, finalPath)

		// Persist rip spec after each episode so API consumers can surface
		// per-episode progress while the encoding stage is still running.
		r.persistRipSpec(ctx, item, env, logger)
	}

	// Only fail if no episodes were encoded successfully
	if len(encodedPaths) == 0 && lastErr != nil {
		return nil, lastErr
	}

	return encodedPaths, nil
}

func (r *encodeJobRunner) recordEpisodeFailure(ctx context.Context, item *queue.Item, env *ripspec.Envelope, job *encodeJob, episodeKey string, err error, eventType, message string, logger *slog.Logger) {
	logger.Error(message,
		logging.String("episode_key", episodeKey),
		logging.Error(err),
		logging.String(logging.FieldEventType, eventType),
	)
	env.Assets.AddAsset(ripspec.AssetKindEncoded, ripspec.Asset{
		EpisodeKey: job.Episode.Key,
		TitleID:    job.Episode.TitleID,
		Path:       "",
		Status:     ripspec.AssetStatusFailed,
		ErrorMsg:   err.Error(),
	})
	r.persistRipSpec(ctx, item, env, logger)
}

func (r *encodeJobRunner) persistRipSpec(ctx context.Context, item *queue.Item, env *ripspec.Envelope, logger *slog.Logger) {
	if err := queue.PersistRipSpec(ctx, r.store, item, env); err != nil {
		logger.Warn("failed to persist rip spec after episode encode; metadata may be stale",
			logging.Error(err),
			logging.String(logging.FieldEventType, "rip_spec_persist_failed"),
			logging.String(logging.FieldErrorHint, "rerun identification or check queue database access"),
			logging.String(logging.FieldImpact, "episode encoding metadata may not reflect latest state"),
		)
	}
}

func (r *encodeJobRunner) encodeSingleFile(ctx context.Context, item *queue.Item, encodedDir string, logger *slog.Logger) (string, error) {
	label := strings.TrimSpace(item.DiscTitle)
	if label == "" {
		label = "Disc"
	}
	item.ActiveEpisodeKey = ""

	sourcePath := item.RippedFile
	path := ""
	if r.runner != nil {
		var err error
		path, err = r.runner.Encode(ctx, item, sourcePath, encodedDir, label, "", 0, 0, logger)
		if err != nil {
			return "", err
		}
	}

	finalTarget := filepath.Join(encodedDir, deriveEncodedFilename(item.RippedFile))
	finalPath, err := ensureEncodedOutput(path, finalTarget, sourcePath)
	if err != nil {
		return "", err
	}

	return finalPath, nil
}
