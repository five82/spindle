package stage

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

type executorStubHandler struct {
	run func(context.Context, *Session) error
}

func (h executorStubHandler) Run(ctx context.Context, sess *Session) error {
	if h.run != nil {
		return h.run(ctx, sess)
	}
	return nil
}

func openExecutorTestStore(t *testing.T) *queue.Store {
	t.Helper()
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestStartStageInitializesActiveProgress(t *testing.T) {
	store := openExecutorTestStore(t)
	item, err := store.NewDisc("A", "fp1")
	if err != nil {
		t.Fatalf("new disc: %v", err)
	}
	item.ProgressPercent = 72
	item.ProgressMessage = "old"
	item.ActiveEpisodeKey = "s01e02"
	item.ProgressBytesCopied = 10
	item.ProgressTotalBytes = 20

	if err := store.StartStage(item, queue.StageEncoding); err != nil {
		t.Fatalf("StartStage: %v", err)
	}
	got, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.InProgress != 1 || got.ProgressStage != string(queue.StageEncoding) || got.ProgressPercent != 0 || got.ProgressMessage != "" {
		t.Fatalf("started state = in_progress:%d stage:%q percent:%v message:%q", got.InProgress, got.ProgressStage, got.ProgressPercent, got.ProgressMessage)
	}
	if got.ActiveEpisodeKey != "" || got.ProgressBytesCopied != 0 || got.ProgressTotalBytes != 0 {
		t.Fatalf("stale progress not cleared = episode:%q bytes:%d/%d", got.ActiveEpisodeKey, got.ProgressBytesCopied, got.ProgressTotalBytes)
	}
}

func TestExecuteWorkflowStageAdvancesAndSetsCompletedProgress(t *testing.T) {
	store := openExecutorTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
	if err := store.MoveToStage(item, queue.StageOrganizing); err != nil {
		t.Fatalf("move item: %v", err)
	}
	if err := store.StartStage(item, queue.StageOrganizing); err != nil {
		t.Fatalf("StartStage: %v", err)
	}

	_, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
		Store:     store,
		Handler:   executorStubHandler{},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stage:     queue.StageOrganizing,
		NextStage: queue.StageCompleted,
	})
	if err != nil {
		t.Fatalf("ExecuteWorkflowStage: %v", err)
	}
	got, _ := store.GetByID(item.ID)
	if got.Stage != queue.StageCompleted || got.InProgress != 0 || got.ProgressStage != string(queue.StageCompleted) || got.ProgressPercent != 100 || got.ProgressMessage != "Completed" {
		t.Fatalf("completed state = stage:%q in_progress:%d progress:%q %.1f %q", got.Stage, got.InProgress, got.ProgressStage, got.ProgressPercent, got.ProgressMessage)
	}
}

func TestExecuteWorkflowStageMarksFailure(t *testing.T) {
	store := openExecutorTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
	if err := store.StartStage(item, queue.StageIdentification); err != nil {
		t.Fatalf("StartStage: %v", err)
	}
	stageErr := errors.New("boom")

	res, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
		Store:   store,
		Handler: executorStubHandler{run: func(context.Context, *Session) error { return stageErr }},
		Stage:   queue.StageIdentification,
	})
	if !errors.Is(err, stageErr) || !res.Failed {
		t.Fatalf("result err=%v failed=%v, want stage error and failed", err, res.Failed)
	}
	got, _ := store.GetByID(item.ID)
	if got.Stage != queue.StageFailed || got.InProgress != 0 || got.FailedAtStage != string(queue.StageIdentification) || got.ErrorMessage != "boom" {
		t.Fatalf("failed state = stage:%q in_progress:%d failed_at:%q err:%q", got.Stage, got.InProgress, got.FailedAtStage, got.ErrorMessage)
	}
}

func TestExecuteWorkflowStageTreatsDegradedAsSuccess(t *testing.T) {
	store := openExecutorTestStore(t)
	item, err := store.NewDisc("A", "fp1")
	if err != nil {
		t.Fatalf("new disc: %v", err)
	}
	item.RipSpecData = mustEncodeExecutorEnvelope(t)
	if err := store.UpdateWorkState(item); err != nil {
		t.Fatalf("update work state: %v", err)
	}
	if err := store.StartStage(item, queue.StageIdentification); err != nil {
		t.Fatalf("StartStage: %v", err)
	}

	res, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
		Store:     store,
		Handler:   executorStubHandler{run: func(context.Context, *Session) error { return &ErrDegraded{Msg: "soft"} }},
		Stage:     queue.StageIdentification,
		NextStage: queue.StageRipping,
	})
	if err != nil || !res.Degraded || res.DegradedMsg != "soft" {
		t.Fatalf("result err=%v degraded=%v msg=%q", err, res.Degraded, res.DegradedMsg)
	}
	if item.Stage != queue.StageRipping || item.InProgress != 0 {
		t.Fatalf("item state = stage:%q in_progress:%d", item.Stage, item.InProgress)
	}
}

func TestExecuteWorkflowStageCancellationClearsInProgress(t *testing.T) {
	store := openExecutorTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
	if err := store.StartStage(item, queue.StageIdentification); err != nil {
		t.Fatalf("StartStage: %v", err)
	}

	res, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
		Store:   store,
		Handler: executorStubHandler{run: func(context.Context, *Session) error { return context.Canceled }},
		Stage:   queue.StageIdentification,
	})
	if !errors.Is(err, context.Canceled) || !res.Canceled {
		t.Fatalf("result err=%v canceled=%v, want context cancellation", err, res.Canceled)
	}
	got, _ := store.GetByID(item.ID)
	if got.Stage != queue.StageIdentification || got.InProgress != 0 {
		t.Fatalf("canceled state = stage:%q in_progress:%d", got.Stage, got.InProgress)
	}
}

func TestExecuteWorkflowStageOneShotClearsWithoutAdvancing(t *testing.T) {
	store := openExecutorTestStore(t)
	item, _ := store.NewDisc("A", "fp1")

	res, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
		Store:     store,
		Handler:   executorStubHandler{},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stage:     queue.StageIdentification,
		NextStage: queue.StageRipping,
		OneShot:   true,
	})
	if err != nil || res.Failed || res.Degraded || res.Canceled {
		t.Fatalf("result err=%v failed=%v degraded=%v canceled=%v", err, res.Failed, res.Degraded, res.Canceled)
	}
	got, _ := store.GetByID(item.ID)
	if got.Stage != queue.StageIdentification || got.InProgress != 0 || got.ProgressStage != string(queue.StageIdentification) {
		t.Fatalf("one-shot state = stage:%q in_progress:%d progress_stage:%q", got.Stage, got.InProgress, got.ProgressStage)
	}
}

func TestExecuteWorkflowStageOneShotFailureDoesNotFailItem(t *testing.T) {
	store := openExecutorTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
	stageErr := errors.New("boom")

	res, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
		Store:   store,
		Handler: executorStubHandler{run: func(context.Context, *Session) error { return stageErr }},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stage:   queue.StageIdentification,
		OneShot: true,
	})
	if !errors.Is(err, stageErr) || !res.Failed {
		t.Fatalf("result err=%v failed=%v, want wrapped stage error and failed", err, res.Failed)
	}
	got, _ := store.GetByID(item.ID)
	if got.Stage != queue.StageIdentification || got.InProgress != 0 || got.FailedAtStage != "" || got.ErrorMessage != "" {
		t.Fatalf("one-shot failure state = stage:%q in_progress:%d failed_at:%q err:%q", got.Stage, got.InProgress, got.FailedAtStage, got.ErrorMessage)
	}
}

func TestExecuteWorkflowStageOneShotTreatsDegradedAsError(t *testing.T) {
	store := openExecutorTestStore(t)
	item, _ := store.NewDisc("A", "fp1")

	res, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
		Store:   store,
		Handler: executorStubHandler{run: func(context.Context, *Session) error { return &ErrDegraded{Msg: "soft"} }},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stage:   queue.StageIdentification,
		OneShot: true,
	})
	var degraded *ErrDegraded
	if !errors.As(err, &degraded) || !res.Failed || res.Degraded {
		t.Fatalf("result err=%v failed=%v degraded=%v, want degraded error treated as failure", err, res.Failed, res.Degraded)
	}
	got, _ := store.GetByID(item.ID)
	if got.Stage != queue.StageIdentification || got.InProgress != 0 {
		t.Fatalf("one-shot degraded state = stage:%q in_progress:%d", got.Stage, got.InProgress)
	}
}

func TestExecuteWorkflowStageOneShotIgnoresCompletionPersistenceError(t *testing.T) {
	store := openExecutorTestStore(t)
	item, _ := store.NewDisc("A", "fp1")

	res, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
		Store: store,
		Handler: executorStubHandler{run: func(context.Context, *Session) error {
			return store.Close()
		}},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stage:   queue.StageIdentification,
		OneShot: true,
	})
	if err != nil || res.Failed || res.Canceled {
		t.Fatalf("result err=%v failed=%v canceled=%v, want ignored completion persistence error", err, res.Failed, res.Canceled)
	}
}

func TestExecuteWorkflowStageReturnsPersistenceError(t *testing.T) {
	store := openExecutorTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
	if err := store.StartStage(item, queue.StageIdentification); err != nil {
		t.Fatalf("StartStage: %v", err)
	}
	_ = store.Close()

	res, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
		Store:     store,
		Handler:   executorStubHandler{},
		Stage:     queue.StageIdentification,
		NextStage: queue.StageRipping,
	})
	var persistErr *PersistenceError
	if !errors.As(err, &persistErr) || persistErr.Op != "persist stage completion" || res.Failed {
		t.Fatalf("result err=%v persist=%v failed=%v, want completion persistence error", err, persistErr, res.Failed)
	}
}

func mustEncodeExecutorEnvelope(t *testing.T) string {
	t.Helper()
	env := ripspec.Envelope{Version: ripspec.CurrentVersion}
	data, err := env.Encode()
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	return data
}
