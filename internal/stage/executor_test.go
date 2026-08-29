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

func TestExecuteWorkflowStageMarksFailure(t *testing.T) {
	store := openExecutorTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
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
	if got.Stage != queue.StageFailed || got.FailedAtStage != queue.StageIdentification || got.ErrorMessage != "boom" {
		t.Fatalf("failed state = stage:%q failed_at:%q err:%q", got.Stage, got.FailedAtStage, got.ErrorMessage)
	}
}

func TestExecuteWorkflowStageClassifiesKilledSubprocessAsCancellation(t *testing.T) {
	store := openExecutorTestStore(t)
	item, _ := store.NewDisc("A", "fp1")

	// Subprocess stages surface a context-cancel kill as an exec error, not
	// context.Canceled. With the stage context cancelled, that must classify
	// as cancellation (task reverts to pending), never as a stage failure.
	ctx, cancel := context.WithCancel(context.Background())
	res, err := ExecuteWorkflowStage(ctx, item, WorkflowOptions{
		Store: store,
		Handler: executorStubHandler{run: func(hctx context.Context, _ *Session) error {
			cancel()
			<-hctx.Done()
			return errors.New("rip title 3: makemkv rip: signal: killed")
		}},
		Stage: queue.StageRipping,
	})
	if !res.Canceled || res.Failed {
		t.Fatalf("result canceled=%v failed=%v err=%v, want cancellation", res.Canceled, res.Failed, err)
	}
	got, _ := store.GetByID(item.ID)
	if got.Stage == queue.StageFailed {
		t.Fatalf("item state = stage:%q, want unchanged stage", got.Stage)
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

	res, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
		Store:   store,
		Handler: executorStubHandler{run: func(context.Context, *Session) error { return &ErrDegraded{Msg: "soft"} }},
		Stage:   queue.StageIdentification,
	})
	if err != nil || !res.Degraded || res.DegradedMsg != "soft" {
		t.Fatalf("result err=%v degraded=%v msg=%q", err, res.Degraded, res.DegradedMsg)
	}
	if item.Stage != queue.StageIdentification {
		t.Fatalf("scheduler-owned state = stage:%q", item.Stage)
	}
}

func TestExecuteWorkflowStageCancellationLeavesStage(t *testing.T) {
	store := openExecutorTestStore(t)
	item, _ := store.NewDisc("A", "fp1")

	res, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
		Store:   store,
		Handler: executorStubHandler{run: func(context.Context, *Session) error { return context.Canceled }},
		Stage:   queue.StageIdentification,
	})
	if !errors.Is(err, context.Canceled) || !res.Canceled {
		t.Fatalf("result err=%v canceled=%v, want context cancellation", err, res.Canceled)
	}
	got, _ := store.GetByID(item.ID)
	if got.Stage != queue.StageIdentification {
		t.Fatalf("canceled state = stage:%q", got.Stage)
	}
}

func TestExecuteWorkflowStageOneShotDoesNotAdvance(t *testing.T) {
	store := openExecutorTestStore(t)
	item, _ := store.NewDisc("A", "fp1")

	res, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
		Store:   store,
		Handler: executorStubHandler{},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stage:   queue.StageIdentification,
		OneShot: true,
	})
	if err != nil || res.Failed || res.Degraded || res.Canceled {
		t.Fatalf("result err=%v failed=%v degraded=%v canceled=%v", err, res.Failed, res.Degraded, res.Canceled)
	}
	got, _ := store.GetByID(item.ID)
	if got.Stage != queue.StageIdentification {
		t.Fatalf("one-shot state = stage:%q", got.Stage)
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
	if got.Stage != queue.StageIdentification || got.FailedAtStage != "" || got.ErrorMessage != "" {
		t.Fatalf("one-shot failure state = stage:%q failed_at:%q err:%q", got.Stage, got.FailedAtStage, got.ErrorMessage)
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
	if got.Stage != queue.StageIdentification {
		t.Fatalf("one-shot degraded state = stage:%q", got.Stage)
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
	_ = store.Close()

	res, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{
		Store:   store,
		Handler: executorStubHandler{},
		Stage:   queue.StageIdentification,
	})
	var persistErr *PersistenceError
	if !errors.As(err, &persistErr) || persistErr.Op != "refresh after stage completion" || res.Failed {
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
