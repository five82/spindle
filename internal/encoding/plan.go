package encoding

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"log/slog"

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

	sampleSource := strings.TrimSpace(item.RippedFile)
	if len(jobs) > 0 {
		sampleSource = strings.TrimSpace(jobs[0].Source)
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
