package workflow_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/queue"
	"spindle/internal/services"
	"spindle/internal/workflow"
)

type stubHandler struct {
	healthReady  bool
	healthDetail string
}

type failingStage struct {
	err error
}

func newStubHandler() *stubHandler {
	return &stubHandler{healthReady: true}
}

func (h *stubHandler) Name() string                { return "stub" }
func (h *stubHandler) TriggerStatus() queue.Status { return queue.StatusIdentified }
func (h *stubHandler) ProcessingStatus() queue.Status {
	return queue.StatusRipping
}
func (h *stubHandler) NextStatus() queue.Status { return queue.StatusCompleted }
func (h *stubHandler) Prepare(ctx context.Context, item *queue.Item) error {
	item.ProgressStage = "preparing"
	return nil
}
func (h *stubHandler) Execute(ctx context.Context, item *queue.Item) error {
	item.ProgressStage = "done"
	item.ProgressPercent = 100
	item.ProgressMessage = "Stage complete"
	return nil
}
func (h *stubHandler) Rollback(ctx context.Context, item *queue.Item, stageErr error) error {
	return nil
}

func (h *stubHandler) HealthCheck(ctx context.Context) workflow.StageHealth {
	if h.healthReady {
		health := workflow.HealthyStage(h.Name())
		health.Detail = h.healthDetail
		return health
	}
	detail := h.healthDetail
	if detail == "" {
		detail = "not ready"
	}
	return workflow.UnhealthyStage(h.Name(), detail)
}

func (f *failingStage) Name() string { return "failing" }

func (f *failingStage) TriggerStatus() queue.Status { return queue.StatusIdentified }

func (f *failingStage) ProcessingStatus() queue.Status { return queue.StatusRipping }

func (f *failingStage) NextStatus() queue.Status { return queue.StatusCompleted }

func (f *failingStage) Prepare(ctx context.Context, item *queue.Item) error {
	item.ProgressStage = "starting"
	return nil
}

func (f *failingStage) Execute(ctx context.Context, item *queue.Item) error {
	return f.err
}

func (f *failingStage) Rollback(ctx context.Context, item *queue.Item, stageErr error) error {
	return nil
}

func (f *failingStage) HealthCheck(ctx context.Context) workflow.StageHealth {
	return workflow.HealthyStage(f.Name())
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	cfg := config.Default()
	cfg.TMDBAPIKey = "test"
	cfg.StagingDir = filepath.Join(base, "staging")
	cfg.LibraryDir = filepath.Join(base, "library")
	cfg.LogDir = filepath.Join(base, "logs")
	cfg.ReviewDir = filepath.Join(base, "review")
	cfg.QueuePollInterval = 0
	cfg.WorkflowWorkerCount = 1
	return &cfg
}

func TestManagerProcessesItems(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})

	logger := zap.NewNop()
	notifier := &managerNotifier{}
	mgr := workflow.NewManagerWithNotifier(cfg, store, logger, notifier)
	mgr.Register(newStubHandler())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() {
		mgr.Stop()
	})

	item, err := store.NewDisc(ctx, "Disc", "fp-123")
	if err != nil {
		t.Fatalf("NewDisc failed: %v", err)
	}
	item.Status = queue.StatusIdentified
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for item processing")
		default:
		}

		updated, err := store.GetByID(ctx, item.ID)
		if err != nil {
			t.Fatalf("GetByID failed: %v", err)
		}
		if updated.Status == queue.StatusCompleted {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if len(notifier.queueStarts) != 1 {
		t.Fatalf("expected one queue start notification, got %d", len(notifier.queueStarts))
	}
	deadline = time.After(time.Second)
	for len(notifier.queueCompletes) == 0 {
		select {
		case <-deadline:
			t.Fatal("expected queue completion notification")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

type managerNotifier struct {
	queueStarts    []int
	queueCompletes []struct{ processed, failed int }
}

func (m *managerNotifier) NotifyDiscDetected(context.Context, string, string) error { return nil }
func (m *managerNotifier) NotifyIdentificationComplete(context.Context, string, string) error {
	return nil
}
func (m *managerNotifier) NotifyRipStarted(context.Context, string) error          { return nil }
func (m *managerNotifier) NotifyRipCompleted(context.Context, string) error        { return nil }
func (m *managerNotifier) NotifyEncodingCompleted(context.Context, string) error   { return nil }
func (m *managerNotifier) NotifyProcessingCompleted(context.Context, string) error { return nil }
func (m *managerNotifier) NotifyOrganizationCompleted(context.Context, string, string) error {
	return nil
}

func (m *managerNotifier) NotifyQueueStarted(ctx context.Context, count int) error {
	m.queueStarts = append(m.queueStarts, count)
	return nil
}

func (m *managerNotifier) NotifyQueueCompleted(ctx context.Context, processed, failed int, _ time.Duration) error {
	m.queueCompletes = append(m.queueCompletes, struct{ processed, failed int }{processed: processed, failed: failed})
	return nil
}

func (m *managerNotifier) NotifyError(context.Context, error, string) error      { return nil }
func (m *managerNotifier) NotifyUnidentifiedMedia(context.Context, string) error { return nil }
func (m *managerNotifier) TestNotification(context.Context) error                { return nil }

func TestManagerStatusIncludesStageHealth(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})

	logger := zap.NewNop()
	stage := newStubHandler()
	stage.healthReady = false
	stage.healthDetail = "dependency missing"
	mgr := workflow.NewManager(cfg, store, logger)
	mgr.Register(stage)

	status := mgr.Status(context.Background())
	health, ok := status.StageHealth[stage.Name()]
	if !ok {
		t.Fatalf("expected stage health entry for %s", stage.Name())
	}
	if health.Ready {
		t.Fatalf("expected not ready health, got %+v", health)
	}
	if health.Detail != stage.healthDetail {
		t.Fatalf("expected detail %q, got %q", stage.healthDetail, health.Detail)
	}
}

func TestManagerFailureTriggersReviewWithHint(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	hint := "Ensure TMDB API key is set"
	stageErr := services.WithHint(
		services.Wrap(services.ErrorValidation, "failing", "execute", "validation failed", nil),
		hint,
	)
	stage := &failingStage{err: stageErr}
	mgr := workflow.NewManager(cfg, store, zap.NewNop())
	mgr.Register(stage)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { mgr.Stop() })

	item, err := store.NewDisc(ctx, "Disc", "fp-review")
	if err != nil {
		t.Fatalf("NewDisc failed: %v", err)
	}
	item.Status = queue.StatusIdentified
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for review status")
		default:
		}
		updated, err := store.GetByID(ctx, item.ID)
		if err != nil {
			t.Fatalf("GetByID failed: %v", err)
		}
		if updated.Status == queue.StatusReview {
			if updated.ProgressStage != "Needs review" {
				t.Fatalf("expected progress stage 'Needs review', got %s", updated.ProgressStage)
			}
			if !strings.Contains(updated.ErrorMessage, "[validation]") {
				t.Fatalf("expected validation code in error message, got %s", updated.ErrorMessage)
			}
			if updated.ProgressMessage != hint {
				t.Fatalf("expected hint in progress message, got %s", updated.ProgressMessage)
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestManagerFailureDefaultsToFailed(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	stage := &failingStage{err: fmt.Errorf("boom")}
	mgr := workflow.NewManager(cfg, store, zap.NewNop())
	mgr.Register(stage)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { mgr.Stop() })

	item, err := store.NewDisc(ctx, "Disc", "fp-failed")
	if err != nil {
		t.Fatalf("NewDisc failed: %v", err)
	}
	item.Status = queue.StatusIdentified
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	deadline := time.After(5 * time.Second)
	var last queue.Status
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for failed status")
		default:
		}
		updated, err := store.GetByID(ctx, item.ID)
		if err != nil {
			t.Fatalf("GetByID failed: %v", err)
		}
		if updated.Status != last {
			t.Logf("item status transitioned to %s", updated.Status)
			last = updated.Status
		}
		if updated.Status == queue.StatusFailed {
			if updated.ProgressStage != "Failed" {
				t.Fatalf("expected progress stage 'Failed', got %s", updated.ProgressStage)
			}
			if updated.ErrorMessage == "" {
				t.Fatal("expected error message to be populated")
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}
