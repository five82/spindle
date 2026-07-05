package queue

import (
	"testing"
)

var testSpecs = []TaskSpec{
	{Type: StageIdentification},
	{Type: StageRipping, DependsOn: []Stage{StageIdentification}},
	{Type: StageEncoding, DependsOn: []Stage{StageRipping}},
	{Type: StageOrganizing, DependsOn: []Stage{StageEncoding}},
}

func taskStatesByType(t *testing.T, store *Store, itemID int64) map[Stage]string {
	t.Helper()
	tasks, err := store.TasksForItem(itemID)
	if err != nil {
		t.Fatalf("tasks for item: %v", err)
	}
	states := make(map[Stage]string, len(tasks))
	for _, task := range tasks {
		states[task.Type] = task.State
	}
	return states
}

func TestEnsureTasksCompilesFreshItemAllPending(t *testing.T) {
	store := openTestStore(t)
	item, _ := store.NewDisc("A", "fp1")

	if err := store.EnsureTasks(item, testSpecs); err != nil {
		t.Fatalf("ensure tasks: %v", err)
	}

	states := taskStatesByType(t, store, item.ID)
	if len(states) != len(testSpecs) {
		t.Fatalf("task count = %d, want %d", len(states), len(testSpecs))
	}
	for _, spec := range testSpecs {
		if states[spec.Type] != TaskPending {
			t.Fatalf("task %s state = %q, want pending", spec.Type, states[spec.Type])
		}
	}
}

func TestEnsureTasksMarksPassedStagesDone(t *testing.T) {
	store := openTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
	if err := store.MoveToStage(item, StageEncoding); err != nil {
		t.Fatalf("move: %v", err)
	}

	if err := store.EnsureTasks(item, testSpecs); err != nil {
		t.Fatalf("ensure tasks: %v", err)
	}

	states := taskStatesByType(t, store, item.ID)
	if states[StageIdentification] != TaskDone || states[StageRipping] != TaskDone {
		t.Fatalf("passed stages not done: %v", states)
	}
	if states[StageEncoding] != TaskPending || states[StageOrganizing] != TaskPending {
		t.Fatalf("remaining stages not pending: %v", states)
	}
}

func TestEnsureTasksIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	item, _ := store.NewDisc("A", "fp1")

	if err := store.EnsureTasks(item, testSpecs); err != nil {
		t.Fatalf("ensure tasks: %v", err)
	}
	if err := store.EnsureTasks(item, testSpecs); err != nil {
		t.Fatalf("ensure tasks again: %v", err)
	}
	tasks, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatalf("tasks: %v", err)
	}
	if len(tasks) != len(testSpecs) {
		t.Fatalf("task count = %d, want %d (no duplicates)", len(tasks), len(testSpecs))
	}
}

func TestReadyTasksGatesOnDepsAndItemState(t *testing.T) {
	store := openTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
	if err := store.EnsureTasks(item, testSpecs); err != nil {
		t.Fatalf("ensure tasks: %v", err)
	}

	ready, err := store.ReadyTasks()
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if len(ready) != 1 || ready[0].Type != StageIdentification {
		t.Fatalf("ready = %v, want only identification", ready)
	}

	// Completing the first task makes the second ready.
	if err := store.StartTask(ready[0]); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := store.FinishTask(ready[0], TaskDone, ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	ready, err = store.ReadyTasks()
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if len(ready) != 1 || ready[0].Type != StageRipping {
		t.Fatalf("ready after done = %v, want only ripping", ready)
	}

	// A running task is not ready, and its dependents are gated by deps.
	if err := store.StartTask(ready[0]); err != nil {
		t.Fatalf("start task: %v", err)
	}
	ready2, err := store.ReadyTasks()
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if len(ready2) != 0 {
		t.Fatalf("ready while task running = %v, want none", ready2)
	}
	if err := store.FinishTask(ready[0], TaskPending, ""); err != nil {
		t.Fatalf("revert task: %v", err)
	}

	// A failed item exposes no ready tasks.
	if err := store.FailStage(item, StageRipping, "boom"); err != nil {
		t.Fatalf("fail stage: %v", err)
	}
	ready, err = store.ReadyTasks()
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if len(ready) != 0 {
		t.Fatalf("ready for failed item = %v, want none", ready)
	}
}

func TestRetryFailedRecompilesTasksFromFailedStage(t *testing.T) {
	store := openTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
	if err := store.EnsureTasks(item, testSpecs); err != nil {
		t.Fatalf("ensure tasks: %v", err)
	}
	if err := store.StartStage(item); err != nil {
		t.Fatalf("start stage: %v", err)
	}
	if err := store.FailStage(item, StageEncoding, "boom"); err != nil {
		t.Fatalf("fail: %v", err)
	}

	n, err := store.RetryFailed(item.ID)
	if err != nil || n != 1 {
		t.Fatalf("retry = (%d, %v), want (1, nil)", n, err)
	}

	// Tasks were dropped by retry; recompile derives position from the
	// retried stage.
	tasks, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatalf("tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks after retry = %d, want 0 (recompiled lazily)", len(tasks))
	}
	refreshed, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := store.EnsureTasks(refreshed, testSpecs); err != nil {
		t.Fatalf("ensure tasks: %v", err)
	}
	states := taskStatesByType(t, store, item.ID)
	if states[StageRipping] != TaskDone || states[StageEncoding] != TaskPending {
		t.Fatalf("recompiled states = %v, want ripping done, encoding pending", states)
	}
}

func TestResetRunningTasks(t *testing.T) {
	store := openTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
	if err := store.EnsureTasks(item, testSpecs); err != nil {
		t.Fatalf("ensure tasks: %v", err)
	}
	ready, _ := store.ReadyTasks()
	if err := store.StartTask(ready[0]); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := store.ResetRunningTasks(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	states := taskStatesByType(t, store, item.ID)
	if states[StageIdentification] != TaskPending {
		t.Fatalf("state after reset = %q, want pending", states[StageIdentification])
	}
}

func TestStopItemsRecordsStoppedStageForRetry(t *testing.T) {
	store := openTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
	if err := store.MoveToStage(item, StageEncoding); err != nil {
		t.Fatalf("move: %v", err)
	}
	if err := store.StartStage(item); err != nil {
		t.Fatalf("start stage: %v", err)
	}

	if _, err := store.StopItems(item.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	got, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.FailedAtStage != string(StageEncoding) {
		t.Fatalf("failed_at_stage = %q, want encoding", got.FailedAtStage)
	}

	// Retry must resume from the stopped stage, not identification:
	// re-running earlier stages wipes staging under resumable outputs.
	if _, err := store.RetryFailed(item.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	got, err = store.GetByID(item.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Stage != StageEncoding {
		t.Fatalf("stage after retry = %q, want encoding", got.Stage)
	}
}

func TestEnsureTasksCompilesDAGAndReadyTasksExposesParallelBranches(t *testing.T) {
	store := openTestStore(t)
	item, _ := store.NewDisc("A", "fp1")

	// Diamond: ripping -> (encoding || subtitling) -> organizing.
	dag := []TaskSpec{
		{Type: StageRipping},
		{Type: StageEncoding, DependsOn: []Stage{StageRipping}},
		{Type: StageSubtitling, DependsOn: []Stage{StageRipping}},
		{Type: StageOrganizing, DependsOn: []Stage{StageEncoding, StageSubtitling}},
	}
	if err := store.MoveToStage(item, StageRipping); err != nil {
		t.Fatalf("move: %v", err)
	}
	if err := store.EnsureTasks(item, dag); err != nil {
		t.Fatalf("ensure tasks: %v", err)
	}

	ready, err := store.ReadyTasks()
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if len(ready) != 1 || ready[0].Type != StageRipping {
		t.Fatalf("ready = %v, want only ripping", ready)
	}
	if err := store.StartTask(ready[0]); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := store.FinishTask(ready[0], TaskDone, ""); err != nil {
		t.Fatalf("finish: %v", err)
	}

	// Both branches become ready simultaneously.
	ready, err = store.ReadyTasks()
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if len(ready) != 2 {
		t.Fatalf("parallel branches ready = %d, want 2 (%v)", len(ready), ready)
	}

	// The join is gated until BOTH branches are done.
	for _, task := range ready {
		if err := store.StartTask(task); err != nil {
			t.Fatalf("start: %v", err)
		}
	}
	if err := store.FinishTask(ready[0], TaskDone, ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	mid, err := store.ReadyTasks()
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if len(mid) != 0 {
		t.Fatalf("join ready before both branches done: %v", mid)
	}
	if err := store.FinishTask(ready[1], TaskDone, ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	final, err := store.ReadyTasks()
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if len(final) != 1 || final[0].Type != StageOrganizing {
		t.Fatalf("join not ready after both branches: %v", final)
	}
}

func TestEnsureTasksRejectsForwardDependency(t *testing.T) {
	store := openTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
	bad := []TaskSpec{
		{Type: StageRipping, DependsOn: []Stage{StageEncoding}},
		{Type: StageEncoding},
	}
	if err := store.EnsureTasks(item, bad); err == nil {
		t.Fatal("expected error for forward dependency")
	}
}
