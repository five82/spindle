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

func TestCompletedItemKeepsTerminalProgress(t *testing.T) {
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

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := store.GetByID(item.ID)
		if err != nil {
			t.Fatalf("get item: %v", err)
		}
		if got.Stage == queue.StageCompleted {
			if got.ProgressStage != string(queue.StageCompleted) {
				t.Fatalf("progress stage = %q, want %q", got.ProgressStage, queue.StageCompleted)
			}
			if got.ProgressPercent != 100 {
				t.Fatalf("progress percent = %v, want 100", got.ProgressPercent)
			}
			if got.ProgressMessage != "Completed" {
				t.Fatalf("progress message = %q, want Completed", got.ProgressMessage)
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
	if err := store.StartStage(item, queue.StageOrganizing); err != nil {
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

	manager.processItem(context.Background(), item, manager.pipeline.stages[0])

	lastErr, lastItem, _ := statusTracker.Snapshot()
	if lastErr != "" || lastItem != nil {
		t.Fatalf("status tracker lastErr=%q lastItem=%v, want no stage outcome", lastErr, lastItem)
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

	manager.processItem(context.Background(), item, manager.pipeline.stages[0])

	select {
	case <-manager.persistenceFailures:
	case <-time.After(time.Second):
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

	deadline := time.Now().Add(3 * time.Second)
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

	deadline := time.Now().Add(3 * time.Second)
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
	deadline := time.Now().Add(3 * time.Second)
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
	case <-time.After(3 * time.Second):
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
	case <-time.After(8 * time.Second):
		t.Fatal("worker was not cancelled after user stop")
	}

	deadline := time.Now().Add(3 * time.Second)
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
			if got.FailedAtStage != string(queue.StageIdentification) {
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
		case <-time.After(3 * time.Second):
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

	deadline := time.Now().Add(8 * time.Second)
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

func TestRefreshDisplayStageUpdatesLabelDuringOverlap(t *testing.T) {
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
	if err := store.StartStage(item, queue.StageRipping); err != nil {
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
	if got.Stage != queue.StageEncoding {
		t.Fatalf("stage label = %q, want encoding (refreshed during overlap)", got.Stage)
	}
	if got.InProgress != 1 {
		t.Fatalf("in_progress = %d, want 1 (label-only refresh must not clear it)", got.InProgress)
	}
}
