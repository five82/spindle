package stage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/queue"
)

func TestExecutorRespectsStopRacingWithHandler(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	item, err := store.NewDisc("Example", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	stop := func() {
		t.Helper()
		if _, err := store.StopItems(item.ID); err != nil {
			t.Fatal(err)
		}
	}
	handler := executorStubHandler{run: func(context.Context, *Session) error { stop(); return nil }}
	res, err := ExecuteWorkflowStage(context.Background(), item, WorkflowOptions{Store: store, Handler: handler, Stage: queue.StageIdentification})
	if err != nil || !res.UserStopped || res.Failed {
		t.Fatalf("successful handler after stop: %+v %v", res, err)
	}
	item2, err := store.NewDisc("Other", "fingerprint2")
	if err != nil {
		t.Fatal(err)
	}
	handler = executorStubHandler{run: func(context.Context, *Session) error {
		if _, err := store.StopItems(item2.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.Refresh(item2); err != nil {
			t.Fatal(err)
		}
		return errors.New("work canceled by stop")
	}}
	res, err = ExecuteWorkflowStage(context.Background(), item2, WorkflowOptions{Store: store, Handler: handler, OneShot: true, Stage: queue.StageIdentification})
	if err != nil || !res.UserStopped || res.Failed {
		t.Fatalf("one-shot stop: %+v %v", res, err)
	}
}
