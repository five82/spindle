package workflow

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
)

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

	item1.Stage = queue.StageCompleted
	_ = store.Update(item1)
	item2.Stage = queue.StageCompleted
	_ = store.Update(item2)
	manager.maybeCompleteQueueCycle(context.Background(), logger)
	if len(events) != 2 || events[1] != "Queue completed" {
		t.Fatalf("events after complete = %v, want [Queue started Queue completed]", events)
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
	item.Stage = queue.StageCompleted
	_ = store.Update(item)

	manager.maybeCompleteQueueCycle(context.Background(), logger)
	if len(events) != 0 {
		t.Fatalf("unexpected queue notification(s): %v", events)
	}
}
