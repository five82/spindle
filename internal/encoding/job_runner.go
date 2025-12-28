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
	encodedPaths := make([]string, 0, maxInt(1, len(jobs)))

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

	for idx, job := range jobs {
		label := fmt.Sprintf("S%02dE%02d", job.Episode.Season, job.Episode.Episode)
		item.ActiveEpisodeKey = strings.ToLower(strings.TrimSpace(job.Episode.Key))
		if item.ActiveEpisodeKey != "" {
			item.ProgressMessage = fmt.Sprintf("Starting encode %s (%d/%d)", label, idx+1, len(jobs))
			item.ProgressPercent = 0
			if r.store != nil {
				if err := r.store.UpdateProgress(ctx, item); err != nil {
					logger.Warn("failed to persist encoding job start", logging.Error(err))
				}
			}
		}

		sourcePath := job.Source
		path := ""
		if r.runner != nil {
			var err error
			path, err = r.runner.Encode(ctx, item, sourcePath, encodedDir, label, job.Episode.Key, idx+1, len(jobs), decision.Profile, logger)
			if err != nil {
				return nil, err
			}
		}

		finalPath, err := ensureEncodedOutput(path, job.Output, sourcePath)
		if err != nil {
			return nil, err
		}

		env.Assets.AddAsset("encoded", ripspec.Asset{EpisodeKey: job.Episode.Key, TitleID: job.Episode.TitleID, Path: finalPath})
		encodedPaths = append(encodedPaths, finalPath)

		// Persist rip spec after each episode so API consumers can surface
		// per-episode progress while the encoding stage is still running.
		if encoded, err := env.Encode(); err == nil {
			copy := *item
			copy.RipSpecData = encoded
			if r.store != nil {
				if err := r.store.Update(ctx, &copy); err != nil {
					logger.Warn("failed to persist rip spec after episode encode", logging.Error(err))
				} else {
					*item = copy
				}
			}
		} else {
			logger.Warn("failed to encode rip spec after episode encode", logging.Error(err))
		}
	}

	return encodedPaths, nil
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
