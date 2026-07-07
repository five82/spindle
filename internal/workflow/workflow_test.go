package workflow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/stage"
)

// Test wait ceilings. CI runners stall for whole seconds under load, so
// outcome waits are generous -- every wait exits early on success, so
// local runs never pay the ceiling. branchDeadlockAfter is how long a
// branch handler waits for its sibling before declaring the branches
// deadlocked; it must stay well under testWait so a true deadlock fails
// the test with its specific message instead of the generic timeout.
const (
	testWait            = 30 * time.Second
	branchDeadlockAfter = 10 * time.Second
)

type stubHandler struct {
	run func(context.Context, *stage.Session) error
}

func (h stubHandler) Run(ctx context.Context, sess *stage.Session) error {
	if h.run != nil {
		return h.run(ctx, sess)
	}
	return nil
}

func newTestManager(stages []PipelineStage) *Manager {
	m := New(nil, nil, nil, slog.Default())
	m.ConfigureStages(stages)
	return m
}

func TestConfigureStagesBuildsStageMap(t *testing.T) {
	stages := []PipelineStage{
		{Stage: queue.StageIdentification},
		{Stage: queue.StageRipping},
		{Stage: queue.StageEncoding},
		{Stage: queue.StageOrganizing},
	}

	m := newTestManager(stages)
	p := m.pipeline

	if len(p.stageMap) != 4 {
		t.Fatalf("stageMap length = %d, want 4", len(p.stageMap))
	}

	for i, s := range stages {
		got, ok := p.stageMap[s.Stage]
		if !ok {
			t.Errorf("stageMap missing stage %q", s.Stage)
			continue
		}
		if got != i {
			t.Errorf("stageMap[%q] = %d, want %d", s.Stage, got, i)
		}
	}
}

func TestCompletedItemHasAllTasksDone(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	item, _ := store.NewDisc("A", "fp1")
	if err := store.MoveToStage(item, queue.StageOrganizing); err != nil {
		t.Fatalf("move item: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(store, nil, nil, logger)
	manager.ConfigureStages([]PipelineStage{{Stage: queue.StageOrganizing, Handler: stubHandler{}}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.Now().Add(testWait)
	for time.Now().Before(deadline) {
		got, err := store.GetByID(item.ID)
		if err != nil {
			t.Fatalf("get item: %v", err)
		}
		if got.Stage == queue.StageCompleted {
			tasks, err := store.TasksForItem(item.ID)
			if err != nil {
				t.Fatalf("tasks for item: %v", err)
			}
			if len(tasks) == 0 {
				t.Fatal("expected compiled task rows for completed item")
			}
			for _, task := range tasks {
				if task.State != queue.TaskDone {
					t.Fatalf("task %s state = %q, want done", task.Type, task.State)
				}
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("item did not complete")
}

func TestUserStoppedItemIsNotRecordedAsStageSuccess(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	item, _ := store.NewDisc("A", "fp1")
	if err := store.MoveToStage(item, queue.StageOrganizing); err != nil {
		t.Fatalf("move item: %v", err)
	}
	if err := store.StartStage(item); err != nil {
		t.Fatalf("start stage: %v", err)
	}

	statusTracker := httpapi.NewStatusTracker(nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(store, nil, statusTracker, logger)
	manager.ConfigureStages([]PipelineStage{{Stage: queue.StageOrganizing, Handler: stubHandler{
		run: func(context.Context, *stage.Session) error {
			_, err := store.StopItems(item.ID)
			return err
		},
	}}})

	manager.processItem(context.Background(), nil, item, manager.pipeline.stages[0], nil)

	lastErr, _ := statusTracker.Snapshot()
	if lastErr != "" {
		t.Fatalf("status tracker lastErr=%q, want no stage outcome recorded", lastErr)
	}
	got, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if !got.UserStopped() || got.Stage != queue.StageFailed {
		t.Fatalf("item stage=%q user_stopped=%v, want stopped failure", got.Stage, got.UserStopped())
	}
}

func TestFinalPersistenceFailureSignalsWorkflowStop(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	item, _ := store.NewDisc("A", "fp1")
	_ = store.MoveToStage(item, queue.StageOrganizing)
	_ = store.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(store, nil, nil, logger)
	manager.ConfigureStages([]PipelineStage{{Stage: queue.StageOrganizing, Handler: stubHandler{}}})

	manager.processItem(context.Background(), nil, item, manager.pipeline.stages[0], nil)

	select {
	case <-manager.persistenceFailures:
	case <-time.After(testWait):
		t.Fatal("expected persistence failure signal")
	}
}

func TestQueueCycleNotificationsRequireBacklogAndPair(t *testing.T) {
	var events []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		events = append(events, r.Header.Get("Title"))
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(store, notify.New(srv.URL, 5, logger), nil, logger)

	item1, _ := store.NewDisc("A", "fp1")
	item2, _ := store.NewDisc("B", "fp2")

	manager.maybeStartQueueCycle(context.Background(), logger)
	if len(events) != 1 || events[0] != "Queue started" {
		t.Fatalf("events after start = %v, want [Queue started]", events)
	}

	manager.maybeStartQueueCycle(context.Background(), logger)
	if len(events) != 1 {
		t.Fatalf("queue_started duplicated: %v", events)
	}

	_ = store.MoveToStage(item1, queue.StageCompleted)
	_ = store.MoveToStage(item2, queue.StageCompleted)
	manager.maybeCompleteQueueCycle(context.Background(), logger)
	if len(events) != 2 || events[1] != "Queue completed" {
		t.Fatalf("events after complete = %v, want [Queue started Queue completed]", events)
	}
}

func TestQueueStartNotificationRetriesAfterFailure(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(store, notify.New(srv.URL, 5, logger), nil, logger)

	_, _ = store.NewDisc("A", "fp1")
	_, _ = store.NewDisc("B", "fp2")

	manager.maybeStartQueueCycle(context.Background(), logger)
	if manager.queueCycleActive {
		t.Fatal("queue cycle should remain inactive after failed queue_started notification")
	}

	manager.maybeStartQueueCycle(context.Background(), logger)
	if !manager.queueCycleActive {
		t.Fatal("queue cycle should become active after successful retry")
	}
	if attempts != 2 {
		t.Fatalf("queue_started attempts = %d, want 2", attempts)
	}
}

func TestQueueCompletionNotificationRetriesAfterFailure(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(store, notify.New(srv.URL, 5, logger), nil, logger)
	manager.queueCycleActive = true

	manager.maybeCompleteQueueCycle(context.Background(), logger)
	if !manager.queueCycleActive {
		t.Fatal("queue cycle should remain active after failed queue_completed notification")
	}

	manager.maybeCompleteQueueCycle(context.Background(), logger)
	if manager.queueCycleActive {
		t.Fatal("queue cycle should clear after successful queue_completed notification")
	}
	if attempts != 2 {
		t.Fatalf("queue_completed attempts = %d, want 2", attempts)
	}
}

func TestQueueCompletionSuppressedWithoutStartedCycle(t *testing.T) {
	var events []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		events = append(events, r.Header.Get("Title"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(store, notify.New(srv.URL, 5, logger), nil, logger)

	item, _ := store.NewDisc("A", "fp1")
	_ = store.MoveToStage(item, queue.StageCompleted)

	manager.maybeCompleteQueueCycle(context.Background(), logger)
	if len(events) != 0 {
		t.Fatalf("unexpected queue notification(s): %v", events)
	}
}

func TestSchedulerRunsChainedStagesAndRecordsTasks(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	item, _ := store.NewDisc("A", "fp1")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(store, nil, nil, logger)
	manager.ConfigureStages([]PipelineStage{
		{Stage: queue.StageIdentification, Handler: stubHandler{}, Claims: map[string]int{"drive": 1}},
		{Stage: queue.StageRipping, Handler: stubHandler{}, Claims: map[string]int{"drive": 1}},
		{Stage: queue.StageOrganizing, Handler: stubHandler{}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.Now().Add(testWait)
	for time.Now().Before(deadline) {
		got, err := store.GetByID(item.ID)
		if err != nil {
			t.Fatalf("get item: %v", err)
		}
		if got.Stage == queue.StageCompleted {
			tasks, err := store.TasksForItem(item.ID)
			if err != nil {
				t.Fatalf("tasks: %v", err)
			}
			if len(tasks) != 3 {
				t.Fatalf("task count = %d, want 3", len(tasks))
			}
			for _, task := range tasks {
				if task.State != queue.TaskDone {
					t.Fatalf("task %s state = %q, want done", task.Type, task.State)
				}
				if task.Attempts != 1 {
					t.Fatalf("task %s attempts = %d, want 1", task.Type, task.Attempts)
				}
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("item did not complete under scheduler")
}

func TestSchedulerBudgetSerializesSameClaim(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	_, _ = store.NewDisc("A", "fp1")
	_, _ = store.NewDisc("B", "fp2")

	var mu sync.Mutex
	running, maxRunning, total := 0, 0, 0

	handler := stubHandler{run: func(context.Context, *stage.Session) error {
		mu.Lock()
		running++
		if running > maxRunning {
			maxRunning = running
		}
		total++
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		running--
		mu.Unlock()
		return nil
	}}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(store, nil, nil, logger)
	manager.ConfigureStages([]PipelineStage{
		{Stage: queue.StageIdentification, Handler: handler, Claims: map[string]int{"drive": 1}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.Now().Add(testWait)
	for time.Now().Before(deadline) {
		mu.Lock()
		t2 := total
		mu.Unlock()
		if t2 == 2 {
			mu.Lock()
			defer mu.Unlock()
			if maxRunning != 1 {
				t.Fatalf("max concurrent same-claim stages = %d, want 1", maxRunning)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("both items did not run")
}

func TestSchedulerFailureMarksTaskFailedAndStopsItem(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	item, _ := store.NewDisc("A", "fp1")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(store, nil, nil, logger)
	manager.ConfigureStages([]PipelineStage{
		{Stage: queue.StageIdentification, Handler: stubHandler{run: func(context.Context, *stage.Session) error {
			return errTestBoom
		}}, Claims: map[string]int{"drive": 1}},
		{Stage: queue.StageRipping, Handler: stubHandler{}, Claims: map[string]int{"drive": 1}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// The item's stage flips to failed (executor) momentarily before the
	// scheduler records the task state, so poll for the COMPLETE terminal
	// state rather than asserting at first sight of the failed stage.
	deadline := time.Now().Add(testWait)
	for time.Now().Before(deadline) {
		got, err := store.GetByID(item.ID)
		if err != nil {
			t.Fatalf("get item: %v", err)
		}
		tasks, err := store.TasksForItem(item.ID)
		if err != nil {
			t.Fatalf("tasks: %v", err)
		}
		if got.Stage == queue.StageFailed &&
			len(tasks) == 2 && tasks[0].State == queue.TaskFailed && tasks[1].State == queue.TaskPending {
			ready, err := store.ReadyTasks()
			if err != nil {
				t.Fatalf("ready: %v", err)
			}
			if len(ready) != 0 {
				t.Fatalf("ready tasks for failed item = %v, want none", ready)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("item did not settle into failed state with failed task and gated dependent")
}

var errTestBoom = errors.New("boom")

func TestSchedulerCancelsWorkerOnUserStop(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	item, _ := store.NewDisc("A", "fp1")

	handlerRunning := make(chan struct{})
	handlerCanceled := make(chan struct{})
	handler := stubHandler{run: func(ctx context.Context, _ *stage.Session) error {
		close(handlerRunning)
		<-ctx.Done()
		close(handlerCanceled)
		return ctx.Err()
	}}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(store, nil, nil, logger)
	manager.ConfigureStages([]PipelineStage{
		{Stage: queue.StageIdentification, Handler: handler, Claims: map[string]int{"drive": 1}},
		{Stage: queue.StageRipping, Handler: stubHandler{}, Claims: map[string]int{"drive": 1}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	select {
	case <-handlerRunning:
	case <-time.After(testWait):
		t.Fatal("handler never started")
	}

	// User stops the item while its stage worker is running. The scheduler
	// must cancel the worker (within one loop tick) instead of leaving a
	// zombie that later stomps queue state or has staging wiped under it.
	if _, err := store.StopItems(item.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}

	select {
	case <-handlerCanceled:
	case <-time.After(testWait):
		t.Fatal("worker was not cancelled after user stop")
	}

	deadline := time.Now().Add(testWait)
	for time.Now().Before(deadline) {
		got, err := store.GetByID(item.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		tasks, err := store.TasksForItem(item.ID)
		if err != nil {
			t.Fatalf("tasks: %v", err)
		}
		if got.Stage == queue.StageFailed && got.UserStopped() &&
			len(tasks) == 2 && tasks[0].State == queue.TaskPending && got.InProgress == 0 {
			if got.FailedAtStage != queue.StageIdentification {
				t.Fatalf("failed_at_stage = %q, want identification", got.FailedAtStage)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("stopped item did not settle into failed state with pending task")
}

func TestSchedulerRunsParallelBranchesOfOneItemConcurrently(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	item, _ := store.NewDisc("A", "fp1")

	// ripping -> (encoding || subtitling) -> organizing, mirroring the 4b
	// template shape. Both branch handlers block until BOTH have started,
	// proving same-item concurrency.
	bothStarted := make(chan struct{})
	var startedMu sync.Mutex
	started := 0
	branchHandler := stubHandler{run: func(ctx context.Context, _ *stage.Session) error {
		startedMu.Lock()
		started++
		if started == 2 {
			close(bothStarted)
		}
		startedMu.Unlock()
		select {
		case <-bothStarted:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(branchDeadlockAfter):
			return errTestBoom // deadlock: branches did not overlap
		}
	}}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(store, nil, nil, logger)
	manager.ConfigureStages([]PipelineStage{
		{Stage: queue.StageRipping, Handler: stubHandler{}, Claims: map[string]int{"drive": 1}},
		{Stage: queue.StageEncoding, Handler: branchHandler, Claims: map[string]int{"encode": 1}, DependsOn: []queue.Stage{queue.StageRipping}},
		{Stage: queue.StageSubtitling, Handler: branchHandler, Claims: map[string]int{"gpu": 1}, DependsOn: []queue.Stage{queue.StageRipping}},
		{Stage: queue.StageOrganizing, Handler: stubHandler{}, DependsOn: []queue.Stage{queue.StageSubtitling, queue.StageEncoding}},
	})
	// Item starts at identification; move to the template's first stage.
	if err := store.MoveToStage(item, queue.StageRipping); err != nil {
		t.Fatalf("move: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.Now().Add(testWait)
	for time.Now().Before(deadline) {
		got, err := store.GetByID(item.ID)
		if err != nil {
			t.Fatalf("get item: %v", err)
		}
		if got.Stage == queue.StageFailed {
			t.Fatal("branches deadlocked instead of overlapping")
		}
		if got.Stage == queue.StageCompleted {
			tasks, err := store.TasksForItem(item.ID)
			if err != nil {
				t.Fatalf("tasks: %v", err)
			}
			for _, task := range tasks {
				if task.State != queue.TaskDone {
					t.Fatalf("task %s state = %q, want done", task.Type, task.State)
				}
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("item did not complete")
}

// TestFinalizeItemLagsStageLabelDuringOverlap verifies the replacement
// invariant for the deleted refreshDisplayStage: finalizeItem leaves the
// item's coarse stage label alone while a sibling worker (here, encoding) is
// still live, even though the ripping task underneath it has already
// finished. Observers must read task state, not the item stage, during
// overlap.
func TestFinalizeItemLagsStageLabelDuringOverlap(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	item, _ := store.NewDisc("A", "fp1")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(store, nil, nil, logger)
	manager.ConfigureStages([]PipelineStage{
		{Stage: queue.StageRipping, Handler: stubHandler{}, Claims: map[string]int{"drive": 1}},
		{Stage: queue.StageEncoding, Handler: stubHandler{}, Claims: map[string]int{"encode": 1}, DependsOn: []queue.Stage{queue.StageRipping}},
	})
	if err := store.MoveToStage(item, queue.StageRipping); err != nil {
		t.Fatalf("move: %v", err)
	}
	if err := store.EnsureTasks(item, manager.pipeline.specs); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := store.StartStage(item); err != nil {
		t.Fatalf("start stage: %v", err)
	}

	// Mark ripping done while an encoding worker is registered as live
	// (the 4d overlap: encoder running, rips just finished).
	tasks, _ := store.TasksForItem(item.ID)
	if err := store.StartTask(tasks[0]); err != nil {
		t.Fatalf("start task: %v", err)
	}
	if err := store.FinishTask(tasks[0], queue.TaskDone, ""); err != nil {
		t.Fatalf("finish task: %v", err)
	}
	if !manager.trackWorker(item.ID, tasks[1].ID, func() {}) {
		t.Fatal("track worker")
	}
	defer manager.untrackWorker(item.ID, tasks[1].ID)

	manager.finalizeItem(item.ID)

	got, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Stage != queue.StageRipping {
		t.Fatalf("stage label = %q, want ripping (label lags while sibling worker is live)", got.Stage)
	}
	if got.InProgress != 1 {
		t.Fatalf("in_progress = %d, want 1 (must stay set while a worker is live)", got.InProgress)
	}

	tasks, err = store.TasksForItem(item.ID)
	if err != nil {
		t.Fatalf("tasks for item: %v", err)
	}
	for _, task := range tasks {
		if task.Type == queue.StageRipping && task.State != queue.TaskDone {
			t.Fatalf("ripping task state = %q, want done (task truth is ahead of the lagging label)", task.State)
		}
	}
}

func TestClaimsFuncEnablesCrossTierPairing(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Item A is 1080p, item B is 4K, item C is 1080p. A and B must encode
	// concurrently (different tier slots); C must wait for A (same tier).
	itemA, _ := store.NewDisc("A-1080p", "fpA")
	itemB, _ := store.NewDisc("B-4k", "fpB")
	itemC, _ := store.NewDisc("C-1080p", "fpC")
	tiers := map[int64]string{itemA.ID: "encode_1080p", itemB.ID: "encode_4k", itemC.ID: "encode_1080p"}

	var mu sync.Mutex
	running := map[int64]bool{}
	maxPair := 0
	sameTierOverlap := false
	release := make(chan struct{})
	handler := stubHandler{run: func(ctx context.Context, sess *stage.Session) error {
		mu.Lock()
		running[sess.Item.ID] = true
		if running[itemA.ID] && running[itemC.ID] {
			sameTierOverlap = true
		}
		count := 0
		for _, on := range running {
			if on {
				count++
			}
		}
		if count > maxPair {
			maxPair = count
		}
		mu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		mu.Lock()
		running[sess.Item.ID] = false
		mu.Unlock()
		return nil
	}}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := New(store, nil, nil, logger)
	manager.ConfigureStages([]PipelineStage{
		{
			Stage:   queue.StageIdentification,
			Handler: handler,
			Claims:  map[string]int{"encode_1080p": 1, "encode_4k": 1},
			ClaimsFunc: func(item *queue.Item) map[string]int {
				return map[string]int{tiers[item.ID]: 1}
			},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Wait until A and B run concurrently (cross-tier pair).
	deadline := time.Now().Add(testWait)
	for time.Now().Before(deadline) {
		mu.Lock()
		paired := running[itemA.ID] && running[itemB.ID]
		mu.Unlock()
		if paired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	paired := running[itemA.ID] && running[itemB.ID]
	cWaiting := !running[itemC.ID]
	mu.Unlock()
	if !paired {
		t.Fatal("1080p + 4K items did not pair")
	}
	if !cWaiting {
		t.Fatal("second 1080p item ran while the 1080p slot was held")
	}

	close(release)
	deadline = time.Now().Add(testWait)
	for time.Now().Before(deadline) {
		items, _ := store.List(queue.StageCompleted)
		if len(items) == 3 {
			mu.Lock()
			defer mu.Unlock()
			if sameTierOverlap {
				t.Fatal("same-tier items overlapped")
			}
			if maxPair != 2 {
				t.Fatalf("max concurrent = %d, want 2 (one per tier)", maxPair)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("items did not complete")
}
