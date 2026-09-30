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

// A finished run must not leave activities reporting live work: the executor
// ends every open activity, journaling activity_ended before the terminal
// stage event, while activities that already finished keep their state.
func TestExecutorEndsOpenActivitiesAtTerminalOutcome(t *testing.T) {
	store := openExecutorTestStore(t)
	item, err := store.NewDisc("disc", "fp")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.EnsureTasks(item, []queue.TaskSpec{{Type: queue.StageIdentification}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	task := tasks[0]
	if err = store.StartTask(task); err != nil {
		t.Fatal(err)
	}
	_, err = ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
		Store: store, Stage: queue.StageIdentification, Task: task,
		Handler: executorStubHandler{run: func(_ context.Context, sess *Session) error {
			sess.Activity(queue.Activity{ID: "summary", Operation: "matching", State: "done", Message: "matched"})
			sess.Activity(queue.Activity{Operation: "persist", Message: "Finalizing identification"})
			return nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, a := range stored[0].Activities {
		states[a.Operation] = a.State
	}
	if states["persist"] != "ended" || states["matching"] != "done" {
		t.Fatalf("activity states = %v", states)
	}
	events, _, err := store.Events(item.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	n := len(events)
	if n < 2 || events[n-2].Type != "activity_ended" || events[n-2].Substage != "persist" || events[n-1].Type != "stage_complete" {
		t.Fatalf("events = %+v", events)
	}
}
