package stage

import (
	"context"
	"errors"
	"testing"

	"github.com/five82/spindle/internal/queue"
)

func TestExecutorJournalsEachRunOutcome(t *testing.T) {
	store := openExecutorTestStore(t)
	item, err := store.NewDisc("disc", "fp")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		run  func(context.Context, *Session) error
		want string
	}{
		{run: func(context.Context, *Session) error { return nil }, want: "stage_complete"},
		{run: func(context.Context, *Session) error { return errors.New("broken") }, want: "stage_failed"},
		{run: func(context.Context, *Session) error { return context.Canceled }, want: "stage_canceled"},
	} {
		_, _ = ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
			Store: store, Stage: queue.StageIdentification, OneShot: true,
			Handler: executorStubHandler{run: tc.run},
		})
	}
	events, _, err := store.Events(item.ID, 0, 20)
	if err != nil || len(events) != 6 {
		t.Fatalf("events=%+v error=%v", events, err)
	}
	for i, want := range []string{"stage_complete", "stage_failed", "stage_canceled"} {
		start, end := events[i*2], events[i*2+1]
		if start.Type != "stage_start" || end.Type != want || end.Stage != queue.StageIdentification || end.DurationSeconds <= 0 {
			t.Fatalf("run %d: start=%+v end=%+v", i, start, end)
		}
	}
}
