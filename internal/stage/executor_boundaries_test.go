package stage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/queue"
)

func TestExecuteWorkflowStageReportsFailurePersistenceError(t *testing.T) {
	store := openExecutorTestStore(t)
	item, err := store.NewDisc("A", "fp")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{Store: store, Handler: executorStubHandler{run: func(context.Context, *Session) error { return errors.New("rip failed") }}, Stage: queue.StageRipping})
	var persist *PersistenceError
	if result.Failed || !errors.As(err, &persist) || persist.Op != "persist stage start" || !strings.Contains(persist.Error(), "persist stage start") || errors.Unwrap(persist) == nil {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestExecuteWorkflowStageOneShotCancellation(t *testing.T) {
	store := openExecutorTestStore(t)
	item, err := store.NewDisc("A", "fp")
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{Store: store, Handler: executorStubHandler{run: func(context.Context, *Session) error { return context.Canceled }}, OneShot: true})
	if !result.Canceled || !errors.Is(err, context.Canceled) || result.Failed {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if _, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{Handler: executorStubHandler{}}); err == nil || !strings.Contains(err.Error(), "nil queue store") {
		t.Fatalf("missing store: %v", err)
	}
}
