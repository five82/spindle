package workflow

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

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
		{Stage: queue.StageIdentification, Semaphore: SemDisc},
		{Stage: queue.StageRipping, Semaphore: SemDisc},
		{Stage: queue.StageEncoding, Semaphore: SemEncode},
		{Stage: queue.StageOrganizing, Semaphore: SemNone},
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

func TestConfigureStagesDerivesStageOrderDiscFirst(t *testing.T) {
	stages := []PipelineStage{
		{Stage: queue.StageIdentification, Semaphore: SemDisc},
		{Stage: queue.StageEncoding, Semaphore: SemEncode},
		{Stage: queue.StageRipping, Semaphore: SemDisc},
		{Stage: queue.StageOrganizing, Semaphore: SemNone},
	}

	m := newTestManager(stages)
	order := m.pipeline.stageOrder

	if len(order) != 4 {
		t.Fatalf("stageOrder length = %d, want 4", len(order))
	}

	// Disc stages should come first (identification, ripping), then the rest.
	if order[0] != queue.StageIdentification {
		t.Errorf("stageOrder[0] = %q, want %q", order[0], queue.StageIdentification)
	}
	if order[1] != queue.StageRipping {
		t.Errorf("stageOrder[1] = %q, want %q", order[1], queue.StageRipping)
	}
	if order[2] != queue.StageEncoding {
		t.Errorf("stageOrder[2] = %q, want %q", order[2], queue.StageEncoding)
	}
	if order[3] != queue.StageOrganizing {
		t.Errorf("stageOrder[3] = %q, want %q", order[3], queue.StageOrganizing)
	}
}

func TestNextStageReturnsCorrectProgression(t *testing.T) {
	stages := []PipelineStage{
		{Stage: queue.StageIdentification, Semaphore: SemDisc},
		{Stage: queue.StageRipping, Semaphore: SemDisc},
		{Stage: queue.StageEncoding, Semaphore: SemEncode},
		{Stage: queue.StageOrganizing, Semaphore: SemNone},
	}

	m := newTestManager(stages)

	tests := []struct {
		current queue.Stage
		want    queue.Stage
	}{
		{queue.StageIdentification, queue.StageRipping},
		{queue.StageRipping, queue.StageEncoding},
		{queue.StageEncoding, queue.StageOrganizing},
	}

	for _, tt := range tests {
		got := m.nextStage(tt.current)
		if got != tt.want {
			t.Errorf("nextStage(%q) = %q, want %q", tt.current, got, tt.want)
		}
	}
}

func TestNextStageReturnsCompletedForLastStage(t *testing.T) {
	stages := []PipelineStage{
		{Stage: queue.StageIdentification, Semaphore: SemDisc},
		{Stage: queue.StageRipping, Semaphore: SemDisc},
		{Stage: queue.StageOrganizing, Semaphore: SemNone},
	}

	m := newTestManager(stages)

	got := m.nextStage(queue.StageOrganizing)
	if got != queue.StageCompleted {
		t.Errorf("nextStage(%q) = %q, want %q", queue.StageOrganizing, got, queue.StageCompleted)
	}
}

func TestNextStageReturnsCompletedForUnknownStage(t *testing.T) {
	stages := []PipelineStage{
		{Stage: queue.StageIdentification, Semaphore: SemDisc},
	}

	m := newTestManager(stages)

	got := m.nextStage(queue.Stage("nonexistent"))
	if got != queue.StageCompleted {
		t.Errorf("nextStage(nonexistent) = %q, want %q", got, queue.StageCompleted)
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
	manager.ConfigureStages([]PipelineStage{{Stage: queue.StageOrganizing, Handler: stubHandler{}, Semaphore: SemNone}})

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
	manager.ConfigureStages([]PipelineStage{{Stage: queue.StageOrganizing, Handler: stubHandler{}, Semaphore: SemNone}})

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
