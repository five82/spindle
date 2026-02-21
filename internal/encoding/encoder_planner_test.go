package encoding

import (
	"context"
	"testing"

	"log/slog"

	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/testsupport"
)

type fakePlanner struct {
	jobs   []encodeJob
	err    error
	called bool
}

func (f *fakePlanner) Plan(ctx context.Context, item *queue.Item, env ripspec.Envelope, encodedDir string, logger *slog.Logger) ([]encodeJob, error) {
	f.called = true
	return f.jobs, f.err
}

func TestEncoderUsesPlannerOverride(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)
	enc := NewEncoderWithDependencies(cfg, store, slog.Default(), nil, nil)
	planner := &fakePlanner{jobs: nil}
	enc.planner = planner
	item := &queue.Item{RippedFile: "movie.mkv", RipSpecData: `{}`, Status: queue.StatusIdentified}
	if err := enc.Execute(context.Background(), item); err == nil {
		t.Fatalf("expected execution to fail without ripped file artifacts")
	}
	if !planner.called {
		t.Fatalf("expected planner to be called")
	}
}
