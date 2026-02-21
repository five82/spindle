package encoding

import (
	"context"
	"testing"

	"log/slog"

	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/testsupport"
)

func TestEncodePlannerBuildsJobs(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	planner := newEncodePlanner()
	item := &queue.Item{RippedFile: "movie.mkv"}
	env := ripspec.Envelope{}
	_, err := planner.Plan(context.Background(), item, env, cfg.Paths.StagingDir, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
