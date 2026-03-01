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

type encodeJob struct {
	Episode ripspec.Episode
	Source  string
	Output  string
}

type encodePlanner interface {
	Plan(ctx context.Context, item *queue.Item, env ripspec.Envelope, encodedDir string, logger *slog.Logger) ([]encodeJob, error)
}

type defaultEncodePlanner struct{}

func newEncodePlanner() encodePlanner {
	return &defaultEncodePlanner{}
}

func (p *defaultEncodePlanner) Plan(ctx context.Context, item *queue.Item, env ripspec.Envelope, encodedDir string, logger *slog.Logger) ([]encodeJob, error) {
	jobs, err := buildEncodeJobs(env, encodedDir)
	if err != nil {
		return nil, services.Wrap(
			services.ErrValidation,
			"encoding",
			"plan encode jobs",
			"Unable to map ripped episodes to encoding jobs",
			err,
		)
	}
	if logger != nil {
		decisionReason := "single_file"
		if len(jobs) > 0 {
			decisionReason = "episode_jobs_available"
		}
		attrs := append(
			logging.DecisionAttrsWithOptions("encoding_job_plan",
				textutil.Ternary(len(jobs) > 0, "episodes", "single_file"),
				decisionReason,
				"single_file, episodes"),
			logging.Int("job_count", len(jobs)),
		)
		attrs = appendEncodeJobLines(attrs, jobs)
		logger.Info("encoding job plan", logging.Args(attrs...)...)
	}

	return jobs, nil
}

func buildEncodeJobs(env ripspec.Envelope, encodedDir string) ([]encodeJob, error) {
	if len(env.Episodes) == 0 {
		return nil, nil
	}
	jobs := make([]encodeJob, 0, len(env.Episodes))
	for _, episode := range env.Episodes {
		asset, ok := env.Assets.FindAsset(ripspec.AssetKindRipped, episode.Key)
		if !ok || strings.TrimSpace(asset.Path) == "" {
			return nil, fmt.Errorf("missing ripped asset for %s", episode.Key)
		}
		base := strings.TrimSpace(episode.OutputBasename)
		if base == "" {
			base = fmt.Sprintf("episode-%s", strings.ToLower(episode.Key))
		}
		output := filepath.Join(encodedDir, base+".mkv")
		jobs = append(jobs, encodeJob{Episode: episode, Source: asset.Path, Output: output})
	}
	return jobs, nil
}

const maxLoggedEncodeJobs = 6

func appendEncodeJobLines(attrs []logging.Attr, jobs []encodeJob) []logging.Attr {
	if len(jobs) == 0 {
		return attrs
	}
	limit := len(jobs)
	if limit > maxLoggedEncodeJobs {
		limit = maxLoggedEncodeJobs
		attrs = append(attrs, logging.Int("job_hidden_count", len(jobs)-limit))
	}
	for idx := 0; idx < limit; idx++ {
		attrs = append(attrs, logging.String(fmt.Sprintf("job_%d", idx+1), formatEncodeJobValue(jobs[idx])))
	}
	return attrs
}

func formatEncodeJobValue(job encodeJob) string {
	key := strings.TrimSpace(job.Episode.Key)
	if key == "" {
		key = fmt.Sprintf("S%02dE%02d", job.Episode.Season, job.Episode.Episode)
	}
	source := filepath.Base(job.Source)
	if source == "" {
		source = "unknown"
	}
	output := filepath.Base(job.Output)
	if output == "" {
		output = "unknown"
	}
	return fmt.Sprintf("%s | %s -> %s", strings.ToUpper(key), source, output)
}
