package encoding

import (
	"context"
	"testing"

	"log/slog"

	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/testsupport"
)

type selectPresetFunc func(ctx context.Context, item *queue.Item, sampleSource string, logger *slog.Logger) presetDecision

func TestEncodePlannerDefaultsProfile(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	planner := newEncodePlanner(selectPresetFunc(func(ctx context.Context, item *queue.Item, sampleSource string, logger *slog.Logger) presetDecision {
		return presetDecision{}
	}))
	item := &queue.Item{RippedFile: "movie.mkv"}
	env := ripspec.Envelope{}
	_, _, err := planner.Plan(context.Background(), item, env, cfg.Paths.StagingDir, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.DraptoPresetProfile != "default" {
		t.Fatalf("expected default profile, got %q", item.DraptoPresetProfile)
	}
}

func TestEncodePlannerTrimsProfile(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	planner := newEncodePlanner(selectPresetFunc(func(ctx context.Context, item *queue.Item, sampleSource string, logger *slog.Logger) presetDecision {
		return presetDecision{Profile: "  clean  "}
	}))
	item := &queue.Item{RippedFile: "movie.mkv"}
	env := ripspec.Envelope{}
	_, _, err := planner.Plan(context.Background(), item, env, cfg.Paths.StagingDir, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.DraptoPresetProfile != "clean" {
		t.Fatalf("expected clean profile, got %q", item.DraptoPresetProfile)
	}
}
