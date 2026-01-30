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
)

type encodeJobRunner struct {
	store  *queue.Store
	runner *draptoRunner
}

func newEncodeJobRunner(store *queue.Store, runner *draptoRunner) *encodeJobRunner {
	return &encodeJobRunner{store: store, runner: runner}
}

func (r *encodeJobRunner) Run(ctx context.Context, item *queue.Item, env ripspec.Envelope, jobs []encodeJob, decision presetDecision, stagingRoot, encodedDir string, logger *slog.Logger) ([]string, error) {
	encodedPaths := make([]string, 0, max(1, len(jobs)))
	if logger != nil {
		runnerAvailable := r != nil && r.runner != nil
		logger.Info(
			"encoding runner decision",
			logging.String(logging.FieldDecisionType, "encoding_runner"),
			logging.String("decision_result", ternary(runnerAvailable, "drapto", "placeholder")),
			logging.String("decision_reason", ternary(runnerAvailable, "drapto_client_configured", "drapto_client_unavailable")),
			logging.String("decision_options", "drapto, placeholder"),
			logging.Int("job_count", len(jobs)),
		)
	}

	if len(jobs) > 0 {
		paths, err := r.encodeEpisodes(ctx, item, &env, jobs, decision, stagingRoot, encodedDir, logger)
		if err != nil {
			return nil, err
		}
		encodedPaths = paths
	} else {
		path, err := r.encodeSingleFile(ctx, item, decision, stagingRoot, encodedDir, logger)
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

func (r *encodeJobRunner) encodeEpisodes(ctx context.Context, item *queue.Item, env *ripspec.Envelope, jobs []encodeJob, decision presetDecision, stagingRoot, encodedDir string, logger *slog.Logger) ([]string, error) {
	encodedPaths := make([]string, 0, len(jobs))
	var lastErr error
	skipped := 0

	for idx, job := range jobs {
		episodeKey := strings.ToLower(strings.TrimSpace(job.Episode.Key))
		label := fmt.Sprintf("S%02dE%02d", job.Episode.Season, job.Episode.Episode)

		// Skip already-completed episodes (enables resume after partial failure)
		if asset, ok := env.Assets.FindAsset("encoded", episodeKey); ok && asset.IsCompleted() {
			logger.Info("episode encoding decision",
				logging.String(logging.FieldDecisionType, "episode_encoding"),
				logging.String("decision_result", "skipped"),
				logging.String("decision_reason", "already_encoded"),
				logging.String("episode_key", episodeKey),
				logging.String("encoded_path", asset.Path),
			)
			encodedPaths = append(encodedPaths, asset.Path)
			skipped++
			continue
		}

		item.ActiveEpisodeKey = episodeKey
		if item.ActiveEpisodeKey != "" {
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
		}

		sourcePath := job.Source
		path := ""
		if r.runner != nil {
			var err error
			path, err = r.runner.Encode(ctx, item, sourcePath, encodedDir, label, job.Episode.Key, idx+1, len(jobs), decision.Profile, logger)
			if err != nil {
				// Record per-episode failure and continue to next episode
				logger.Error("episode encoding failed",
					logging.String("episode_key", episodeKey),
					logging.Error(err),
					logging.String(logging.FieldEventType, "episode_encode_failed"),
				)
				env.Assets.AddAsset("encoded", ripspec.Asset{
					EpisodeKey: job.Episode.Key,
					TitleID:    job.Episode.TitleID,
					Path:       "",
					Status:     ripspec.AssetStatusFailed,
					ErrorMsg:   err.Error(),
				})
				lastErr = err
				r.persistRipSpec(ctx, item, env, logger)
				continue
			}
		}

		finalPath, err := ensureEncodedOutput(path, job.Output, sourcePath)
		if err != nil {
			// Record per-episode failure and continue to next episode
			logger.Error("episode output finalization failed",
				logging.String("episode_key", episodeKey),
				logging.Error(err),
				logging.String(logging.FieldEventType, "episode_output_failed"),
			)
			env.Assets.AddAsset("encoded", ripspec.Asset{
				EpisodeKey: job.Episode.Key,
				TitleID:    job.Episode.TitleID,
				Path:       "",
				Status:     ripspec.AssetStatusFailed,
				ErrorMsg:   err.Error(),
			})
			lastErr = err
			r.persistRipSpec(ctx, item, env, logger)
			continue
		}

		env.Assets.AddAsset("encoded", ripspec.Asset{
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

func (r *encodeJobRunner) persistRipSpec(ctx context.Context, item *queue.Item, env *ripspec.Envelope, logger *slog.Logger) {
	encoded, err := env.Encode()
	if err != nil {
		logger.Warn("failed to encode rip spec after episode encode; metadata may be stale",
			logging.Error(err),
			logging.String(logging.FieldEventType, "rip_spec_encode_failed"),
			logging.String(logging.FieldErrorHint, "rerun identification if rip spec data looks wrong"),
			logging.String(logging.FieldImpact, "episode encoding metadata may not reflect latest state"),
		)
		return
	}
	itemCopy := *item
	itemCopy.RipSpecData = encoded
	if r.store != nil {
		if err := r.store.Update(ctx, &itemCopy); err != nil {
			logger.Warn("failed to persist rip spec after episode encode; metadata may be stale",
				logging.Error(err),
				logging.String(logging.FieldEventType, "rip_spec_persist_failed"),
				logging.String(logging.FieldErrorHint, "check queue database access"),
				logging.String(logging.FieldImpact, "episode encoding metadata may not reflect latest state"),
			)
		} else {
			*item = itemCopy
		}
	}
}

func (r *encodeJobRunner) encodeSingleFile(ctx context.Context, item *queue.Item, decision presetDecision, stagingRoot, encodedDir string, logger *slog.Logger) (string, error) {
	label := strings.TrimSpace(item.DiscTitle)
	if label == "" {
		label = "Disc"
	}
	item.ActiveEpisodeKey = ""

	sourcePath := item.RippedFile
	path := ""
	if r.runner != nil {
		var err error
		path, err = r.runner.Encode(ctx, item, sourcePath, encodedDir, label, "", 0, 0, decision.Profile, logger)
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
