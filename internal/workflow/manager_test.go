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
	"spindle/internal/stage"
	"spindle/internal/workflow"
)

type stubStage struct {
	name        string
	prepareHook func(*queue.Item)
	executeHook func(*queue.Item)
	prepareErr  error
	executeErr  error
	health      stage.Health
}

func newStubStage(name string) *stubStage {
	return &stubStage{name: name, health: stage.Healthy(name)}
}

func (s *stubStage) Prepare(_ context.Context, item *queue.Item) error {
	if s.prepareHook != nil {
		s.prepareHook(item)
	}
	return s.prepareErr
}

func (s *stubStage) Execute(_ context.Context, item *queue.Item) error {
	if s.executeHook != nil {
		s.executeHook(item)
	}
	return s.executeErr
}

func (s *stubStage) HealthCheck(context.Context) stage.Health {
	return s.health
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
	t.Cleanup(func() { store.Close() })

	identifier := newStubStage("identifier")
	ripper := newStubStage("ripper")
	encoder := newStubStage("encoder")
	organizer := newStubStage("organizer")

	notifier := &managerNotifier{}
	mgr := workflow.NewManagerWithNotifier(cfg, store, zap.NewNop(), notifier)
	mgr.ConfigureStages(workflow.StageSet{
		Identifier: identifier,
		Ripper:     ripper,
		Encoder:    encoder,
		Organizer:  organizer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { mgr.Stop() })

	item, err := store.NewDisc(ctx, "Disc", "fp-success")
	if err != nil {
		t.Fatalf("NewDisc failed: %v", err)
	}

	deadline := time.After(60 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for completion")
		default:
		}
		updated, err := store.GetByID(ctx, item.ID)
		if err != nil {
			t.Fatalf("GetByID failed: %v", err)
		}
		if updated.Status == queue.StatusCompleted {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	if len(notifier.queueStarts) != 1 {
		t.Fatalf("expected one queue start notification, got %d", len(notifier.queueStarts))
	}
	deadline = time.After(10 * time.Second)
	for len(notifier.queueCompletes) == 0 {
		select {
		case <-deadline:
			t.Fatal("expected queue completion notification")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestManagerStatusIncludesStageHealth(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	handler := newStubStage("identifier")
	handler.health = stage.Unhealthy(handler.name, "dependency missing")

	mgr := workflow.NewManager(cfg, store, zap.NewNop())
	mgr.ConfigureStages(workflow.StageSet{Identifier: handler})

	status := mgr.Status(context.Background())
	health, ok := status.StageHealth[handler.name]
	if !ok {
		t.Fatalf("expected stage health entry for %s", handler.name)
	}
	if health.Ready {
		t.Fatalf("expected not ready health, got %+v", health)
	}
	if health.Detail != handler.health.Detail {
		t.Fatalf("expected detail %q, got %q", handler.health.Detail, health.Detail)
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
	failing := newStubStage("ripper")
	failing.executeErr = stageErr

	mgr := workflow.NewManager(cfg, store, zap.NewNop())
	mgr.ConfigureStages(workflow.StageSet{Ripper: failing})

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

	deadline := time.After(30 * time.Second)
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
		time.Sleep(25 * time.Millisecond)
	}
}

func TestManagerFailureDefaultsToFailed(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	failing := newStubStage("ripper")
	failing.executeErr = fmt.Errorf("boom")

	mgr := workflow.NewManager(cfg, store, zap.NewNop())
	mgr.ConfigureStages(workflow.StageSet{Ripper: failing})

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

	deadline := time.After(30 * time.Second)
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
		if updated.Status == queue.StatusFailed {
			if updated.ProgressStage != "Failed" {
				t.Fatalf("expected progress stage 'Failed', got %s", updated.ProgressStage)
			}
			if updated.ErrorMessage == "" {
				t.Fatal("expected error message to be populated")
			}
			break
		}
		time.Sleep(25 * time.Millisecond)
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
