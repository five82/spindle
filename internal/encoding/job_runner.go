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

type refineCommentaryFunc func(ctx context.Context, item *queue.Item, sourcePath, stagingRoot, label string, episodeIndex, episodeCount int, logger *slog.Logger) (string, error)

type encodeJobRunner struct {
	store   *queue.Store
	runner  *draptoRunner
	refiner refineCommentaryFunc
}

func newEncodeJobRunner(store *queue.Store, runner *draptoRunner, refiner refineCommentaryFunc) *encodeJobRunner {
	return &encodeJobRunner{store: store, runner: runner, refiner: refiner}
}

type encodeResults struct {
	EncodedPaths  []string
	SourcePaths   []string
	WorkingCopies []string
}

func (r *encodeJobRunner) Run(ctx context.Context, item *queue.Item, env ripspec.Envelope, jobs []encodeJob, decision presetDecision, stagingRoot, encodedDir string, logger *slog.Logger) (encodeResults, error) {
	encodedPaths := make([]string, 0, maxInt(1, len(jobs)))
	sourcePaths := make([]string, 0, maxInt(1, len(jobs)))
	workingCopies := make([]string, 0, maxInt(1, len(jobs)))

	if len(jobs) > 0 {
		paths, sources, copies, err := r.encodeEpisodes(ctx, item, &env, jobs, decision, stagingRoot, encodedDir, logger)
		if err != nil {
			return encodeResults{}, err
		}
		encodedPaths = paths
		sourcePaths = sources
		workingCopies = copies
	} else {
		path, source, copyPath, err := r.encodeSingleFile(ctx, item, decision, stagingRoot, encodedDir, logger)
		if err != nil {
			return encodeResults{}, err
		}
		encodedPaths = append(encodedPaths, path)
		sourcePaths = append(sourcePaths, source)
		if strings.TrimSpace(copyPath) != "" {
			workingCopies = append(workingCopies, copyPath)
		}
	}

	if len(encodedPaths) == 0 {
		return encodeResults{}, services.Wrap(
			services.ErrValidation,
			"encoding",
			"locate encoded outputs",
			"No encoded artifacts were produced",
			nil,
		)
	}

	return encodeResults{
		EncodedPaths:  encodedPaths,
		SourcePaths:   sourcePaths,
		WorkingCopies: workingCopies,
	}, nil
}

func (r *encodeJobRunner) encodeEpisodes(ctx context.Context, item *queue.Item, env *ripspec.Envelope, jobs []encodeJob, decision presetDecision, stagingRoot, encodedDir string, logger *slog.Logger) ([]string, []string, []string, error) {
	encodedPaths := make([]string, 0, len(jobs))
	sourcePaths := make([]string, 0, len(jobs))
	workingCopies := make([]string, 0, len(jobs))

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
		if r.refiner != nil {
			refined, err := r.refiner(ctx, item, sourcePath, stagingRoot, label, idx+1, len(jobs), logger)
			if err != nil {
				logger.Warn("commentary detection failed; encoding with existing audio streams", logging.Error(err))
			} else if strings.TrimSpace(refined) != "" {
				sourcePath = refined
				if !strings.EqualFold(strings.TrimSpace(sourcePath), strings.TrimSpace(job.Source)) {
					workingCopies = append(workingCopies, sourcePath)
				}
			}
		}

		path := ""
		if r.runner != nil {
			var err error
			path, err = r.runner.Encode(ctx, item, sourcePath, encodedDir, label, job.Episode.Key, idx+1, len(jobs), decision.Profile, logger)
			if err != nil {
				return nil, nil, nil, err
			}
		}

		finalPath, err := ensureEncodedOutput(path, job.Output, sourcePath)
		if err != nil {
			return nil, nil, nil, err
		}

		env.Assets.AddAsset("encoded", ripspec.Asset{EpisodeKey: job.Episode.Key, TitleID: job.Episode.TitleID, Path: finalPath})
		encodedPaths = append(encodedPaths, finalPath)
		sourcePaths = append(sourcePaths, sourcePath)

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

	return encodedPaths, sourcePaths, workingCopies, nil
}

func (r *encodeJobRunner) encodeSingleFile(ctx context.Context, item *queue.Item, decision presetDecision, stagingRoot, encodedDir string, logger *slog.Logger) (string, string, string, error) {
	label := strings.TrimSpace(item.DiscTitle)
	if label == "" {
		label = "Disc"
	}
	item.ActiveEpisodeKey = ""

	sourcePath := item.RippedFile
	workingCopy := ""
	if r.refiner != nil {
		refined, err := r.refiner(ctx, item, sourcePath, stagingRoot, label, 0, 0, logger)
		if err != nil {
			logger.Warn("commentary detection failed; encoding with existing audio streams", logging.Error(err))
		} else if strings.TrimSpace(refined) != "" {
			sourcePath = refined
			if !strings.EqualFold(strings.TrimSpace(sourcePath), strings.TrimSpace(item.RippedFile)) {
				workingCopy = sourcePath
			}
		}
	}

	path := ""
	if r.runner != nil {
		var err error
		path, err = r.runner.Encode(ctx, item, sourcePath, encodedDir, label, "", 0, 0, decision.Profile, logger)
		if err != nil {
			return "", "", "", err
		}
	}

	finalTarget := filepath.Join(encodedDir, deriveEncodedFilename(item.RippedFile))
	finalPath, err := ensureEncodedOutput(path, finalTarget, sourcePath)
	if err != nil {
		return "", "", "", err
	}

	return finalPath, sourcePath, workingCopy, nil
}
