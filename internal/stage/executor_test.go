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
	"github.com/five82/spindle/internal/services"
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

func TestMarkStartedInitializesActiveProgress(t *testing.T) {
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

	if err := MarkStarted(store, item, queue.StageEncoding); err != nil {
		t.Fatalf("MarkStarted: %v", err)
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

func TestExecuteStartedAdvancesAndSetsCompletedProgress(t *testing.T) {
	store := openExecutorTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
	item.Stage = queue.StageOrganizing
	if err := store.Update(item); err != nil {
		t.Fatalf("update item: %v", err)
	}
	if err := MarkStarted(store, item, queue.StageOrganizing); err != nil {
		t.Fatalf("MarkStarted: %v", err)
	}

	_, err := ExecuteStarted(context.Background(), item, ExecuteOptions{
		Store:     store,
		Handler:   executorStubHandler{},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stage:     queue.StageOrganizing,
		NextStage: queue.StageCompleted,
		Advance:   true,
	})
	if err != nil {
		t.Fatalf("ExecuteStarted: %v", err)
	}
	got, _ := store.GetByID(item.ID)
	if got.Stage != queue.StageCompleted || got.InProgress != 0 || got.ProgressStage != string(queue.StageCompleted) || got.ProgressPercent != 100 || got.ProgressMessage != "Completed" {
		t.Fatalf("completed state = stage:%q in_progress:%d progress:%q %.1f %q", got.Stage, got.InProgress, got.ProgressStage, got.ProgressPercent, got.ProgressMessage)
	}
}

func TestExecuteStartedMarksFailureWhenConfigured(t *testing.T) {
	store := openExecutorTestStore(t)
	item, _ := store.NewDisc("A", "fp1")
	if err := MarkStarted(store, item, queue.StageIdentification); err != nil {
		t.Fatalf("MarkStarted: %v", err)
	}
	stageErr := errors.New("boom")

	res, err := ExecuteStarted(context.Background(), item, ExecuteOptions{
		Store:      store,
		Handler:    executorStubHandler{run: func(context.Context, *Session) error { return stageErr }},
		Stage:      queue.StageIdentification,
		MarkFailed: true,
	})
	if !errors.Is(err, stageErr) || !res.Failed {
		t.Fatalf("result err=%v failed=%v, want stage error and failed", err, res.Failed)
	}
	got, _ := store.GetByID(item.ID)
	if got.Stage != queue.StageFailed || got.InProgress != 0 || got.FailedAtStage != string(queue.StageIdentification) || got.ErrorMessage != "boom" {
		t.Fatalf("failed state = stage:%q in_progress:%d failed_at:%q err:%q", got.Stage, got.InProgress, got.FailedAtStage, got.ErrorMessage)
	}
}

func TestExecuteStartedTreatsDegradedAsSuccessWhenConfigured(t *testing.T) {
	item := &queue.Item{Stage: queue.StageIdentification, RipSpecData: mustEncodeExecutorEnvelope(t)}
	res, err := ExecuteStarted(context.Background(), item, ExecuteOptions{
		Handler:          executorStubHandler{run: func(context.Context, *Session) error { return &services.ErrDegraded{Msg: "soft"} }},
		Stage:            queue.StageIdentification,
		NextStage:        queue.StageRipping,
		Advance:          true,
		DegradedSucceeds: true,
	})
	if err != nil || !res.Degraded || res.DegradedMsg != "soft" {
		t.Fatalf("result err=%v degraded=%v msg=%q", err, res.Degraded, res.DegradedMsg)
	}
	if item.Stage != queue.StageRipping || item.InProgress != 0 {
		t.Fatalf("item state = stage:%q in_progress:%d", item.Stage, item.InProgress)
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
