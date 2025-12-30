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

type encodeJob struct {
	Episode ripspec.Episode
	Source  string
	Output  string
}

type encodePlanner interface {
	Plan(ctx context.Context, item *queue.Item, env ripspec.Envelope, encodedDir string, logger *slog.Logger) ([]encodeJob, presetDecision, error)
}

type defaultEncodePlanner struct {
	selectPreset func(ctx context.Context, item *queue.Item, sampleSource string, logger *slog.Logger) presetDecision
}

func newEncodePlanner(selectPreset func(ctx context.Context, item *queue.Item, sampleSource string, logger *slog.Logger) presetDecision) encodePlanner {
	return &defaultEncodePlanner{selectPreset: selectPreset}
}

func (p *defaultEncodePlanner) Plan(ctx context.Context, item *queue.Item, env ripspec.Envelope, encodedDir string, logger *slog.Logger) ([]encodeJob, presetDecision, error) {
	jobs, err := buildEncodeJobs(env, encodedDir)
	if err != nil {
		return nil, presetDecision{}, services.Wrap(
			services.ErrValidation,
			"encoding",
			"plan encode jobs",
			"Unable to map ripped episodes to encoding jobs",
			err,
		)
	}
	if logger != nil {
		choices := []string{"single_file"}
		decisionReason := "single_file"
		if len(jobs) > 0 {
			choices = append(choices, "episodes")
			decisionReason = "episode_jobs_available"
		}
		attrs := []logging.Attr{
			logging.String(logging.FieldDecisionType, "encoding_job_plan"),
			logging.String("decision_result", ternary(len(jobs) > 0, "episodes", "single_file")),
			logging.String("decision_reason", decisionReason),
			logging.String("decision_options", strings.Join(choices, ", ")),
			logging.Int("job_count", len(jobs)),
		}
		attrs = appendEncodeJobLines(attrs, jobs)
		logger.Info("encoding job plan", logging.Args(attrs...)...)
	}

	sampleSource := strings.TrimSpace(item.RippedFile)
	sampleSourceSource := "ripped_file"
	if len(jobs) > 0 {
		sampleSource = strings.TrimSpace(jobs[0].Source)
		sampleSourceSource = "first_episode_source"
	}
	var decision presetDecision
	if p != nil && p.selectPreset != nil {
		decision = p.selectPreset(ctx, item, sampleSource, logger)
	}
	if profile := strings.TrimSpace(decision.Profile); profile != "" {
		item.DraptoPresetProfile = profile
	} else {
		item.DraptoPresetProfile = "default"
	}
	if logger != nil {
		logger.Info(
			"encoding preset profile selected",
			logging.String(logging.FieldDecisionType, "encoding_preset_profile"),
			logging.String("decision_result", strings.TrimSpace(item.DraptoPresetProfile)),
			logging.String("decision_reason", ternary(decision.Applied, "preset_decider", "default")),
			logging.String("decision_options", "default, preset_decider"),
			logging.String("sample_source", sampleSource),
			logging.String("sample_source_reason", sampleSourceSource),
		)
	}

	return jobs, decision, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func buildEncodeJobs(env ripspec.Envelope, encodedDir string) ([]encodeJob, error) {
	if len(env.Episodes) == 0 {
		return nil, nil
	}
	jobs := make([]encodeJob, 0, len(env.Episodes))
	for _, episode := range env.Episodes {
		asset, ok := env.Assets.FindAsset("ripped", episode.Key)
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
