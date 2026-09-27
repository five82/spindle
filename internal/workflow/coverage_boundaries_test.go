package workflow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/stage"
)

func TestDispatchAndFinalizeWithClosedStore(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(store, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.ConfigureStages([]PipelineStage{{Stage: queue.StageIdentification, Handler: stubHandler{}}})
	m.finalizeItem(999) // missing item is a no-op
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	m.dispatch(context.Background(), &workers) // closed store: must not spawn workers
	m.finalizeItem(999)                        // closed store: must not advance an item
	workers.Wait()
}

func TestMetricsFailuresNeverFailPipeline(t *testing.T) {
	m := New(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.SetMetricsPath(t.TempDir()) // opening a directory for append fails
	m.writeMetricsRecord(&queue.Item{ID: 7, DiscTitle: "movie", RipSpecData: "not a rip spec"}, nil)
	m.warnMetrics(7, errors.New("storage unavailable"))
	if waits := m.takeWaits(7); waits != nil {
		t.Fatalf("unexpected wait accounting: %v", waits)
	}
}

func TestDispatchRespectsCancellationUnknownStageAndResourceBudget(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	item, err := store.NewDisc("Example", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	calls := 0
	m.ConfigureStages([]PipelineStage{{Stage: queue.StageIdentification, Handler: stubHandler{run: func(context.Context, *stage.Session) error { calls++; return nil }}, Claims: map[string]int{"drive": 1}}})
	var workers sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.dispatch(ctx, &workers)
	if calls != 0 {
		t.Fatal("canceled dispatch ran a handler")
	}
	delete(m.pipeline.stageMap, queue.StageIdentification)
	m.dispatch(context.Background(), &workers)
	if calls != 0 {
		t.Fatal("unregistered stage ran a handler")
	}
	m.pipeline.stageMap[queue.StageIdentification] = 0
	m.budgetUsed["drive"] = 1
	m.dispatch(context.Background(), &workers)
	if len(m.blocked) != 1 || calls != 0 {
		t.Fatalf("blocked task: %v, calls=%d", m.blocked, calls)
	}
	m.budgetUsed["drive"] = 0
	m.dispatch(context.Background(), &workers)
	workers.Wait()
	if calls != 1 || len(m.blocked) != 0 {
		t.Fatalf("granted task: %v, calls=%d", m.blocked, calls)
	}
	got, err := store.GetByID(item.ID)
	if err != nil || got.Stage != queue.StageCompleted {
		t.Fatalf("item not finalized: %+v %v", got, err)
	}
}

func TestStaleWorkerAndUserStopPreventConcurrentDispatch(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	item, err := store.NewDisc("Example", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	m := New(store, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.ConfigureStages([]PipelineStage{{Stage: queue.StageIdentification, Handler: stubHandler{}}})
	if m.hasStaleWorker(item.ID) {
		t.Fatal("item without workers is stale")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !m.trackWorker(item.ID, 999, queue.StageIdentification, cancel) {
		t.Fatal("register worker")
	}
	if !m.hasStaleWorker(item.ID) {
		t.Fatal("orphan worker not detected")
	}
	if _, err := store.StopItems(item.ID); err != nil {
		t.Fatal(err)
	}
	m.cancelStoppedWorkers()
	if ctx.Err() != context.Canceled {
		t.Fatal("stopped item did not cancel its worker")
	}
	m.finalizeItem(item.ID) // worker is still live; no stage advancement
	m.untrackWorker(item.ID, 999)
	m.finalizeItem(item.ID) // stopped item retains its failed stage
	got, err := store.GetByID(item.ID)
	if err != nil || got.Stage != queue.StageFailed {
		t.Fatalf("stopped stage: %+v %v", got, err)
	}
}

func TestWorkflowRejectsUnknownOrReorderedStageTemplates(t *testing.T) {
	for _, stages := range [][]PipelineStage{
		{{Stage: queue.Stage("unknown")}},
		{{Stage: queue.StageRipping}, {Stage: queue.StageIdentification}},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("invalid template accepted: %+v", stages)
				}
			}()
			New(nil, nil, nil, nil).ConfigureStages(stages)
		}()
	}
}

func TestWorkerRegistryAndDrainIdempotence(t *testing.T) {
	m := New(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.ConfigureStages([]PipelineStage{{Stage: queue.StageIdentification, Claims: map[string]int{"drive": 1}}, {Stage: queue.StageRipping}})
	if !m.stageHoldsDrive(queue.StageIdentification) || m.stageHoldsDrive(queue.StageRipping) || m.stageHoldsDrive(queue.Stage("missing")) {
		t.Fatal("drive resource claims misclassified")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !m.trackWorker(1, 1, queue.StageRipping, cancel) || m.trackWorker(1, 1, queue.StageRipping, cancel) {
		t.Fatal("duplicate worker registered")
	}
	m.Drain()
	m.Drain()
	m.drainPass()
	m.drainPass()
	if ctx.Err() != context.Canceled {
		t.Fatal("resumable worker was not canceled")
	}
	m.untrackWorker(1, 1)
	m.drainPass()
	select {
	case <-m.Drained():
	default:
		t.Fatal("drain not completed")
	}
}

func TestNotificationReasonTruncatesAtRuneBoundary(t *testing.T) {
	if got := notificationReason(nil); got != "unknown error" {
		t.Fatal(got)
	}
	if got := notificationReason(errors.New(" \t")); got != "unknown error" {
		t.Fatal(got)
	}
	if got := notificationReason(errors.New(" short ")); got != "short" {
		t.Fatal(got)
	}
	got := notificationReason(errors.New(strings.Repeat("\u00e9", 505)))
	if len([]rune(got)) != 500 || !strings.HasSuffix(got, "...") {
		t.Fatalf("truncated reason: %q", got)
	}
}
