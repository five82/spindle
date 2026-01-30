package workflow_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/services"
	"spindle/internal/stage"
	"spindle/internal/testsupport"
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

func TestManagerProcessesItems(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	cfg.Workflow.QueuePollInterval = 0
	store := testsupport.MustOpenStore(t, cfg)

	identifier := newStubStage("identifier")
	ripper := newStubStage("ripper")
	encoder := newStubStage("encoder")
	organizer := newStubStage("organizer")

	notifier := &managerNotifier{}
	mgr := workflow.NewManagerWithNotifier(cfg, store, logging.NewNop(), notifier)
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

	deadline := time.After(10 * time.Second)
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
			if updated.ProgressStage != "Completed" {
				t.Fatalf("expected progress stage %q, got %q", "Completed", updated.ProgressStage)
			}
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	if notifier.startsCount() != 1 {
		t.Fatalf("expected one queue start notification, got %d", notifier.startsCount())
	}
	deadline = time.After(10 * time.Second)
	for notifier.completesCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("expected queue completion notification")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestManagerStatusIncludesStageHealth(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	cfg.Workflow.QueuePollInterval = 0
	store := testsupport.MustOpenStore(t, cfg)

	handler := newStubStage("identifier")
	handler.health = stage.Unhealthy(handler.name, "dependency missing")

	mgr := workflow.NewManager(cfg, store, logging.NewNop())
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

func TestManagerValidationErrorTriggersReview(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	cfg.Workflow.QueuePollInterval = 0
	store := testsupport.MustOpenStore(t, cfg)

	stageErr := services.Wrap(
		services.ErrValidation,
		"failing",
		"execute",
		"validation failed; ensure TMDB API key is set",
		nil,
	)
	failing := newStubStage("ripper")
	failing.executeErr = stageErr

	mgr := workflow.NewManager(cfg, store, logging.NewNop())
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
		if updated.Status == queue.StatusFailed {
			if updated.ProgressStage != "Failed" {
				t.Fatalf("expected progress stage 'Failed', got %s", updated.ProgressStage)
			}
			if !strings.Contains(strings.ToLower(updated.ErrorMessage), "validation") {
				t.Fatalf("expected validation hint in error message, got %s", updated.ErrorMessage)
			}
			if updated.ProgressMessage != updated.ErrorMessage {
				t.Fatalf("expected progress message to match error message, got %q", updated.ProgressMessage)
			}
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestManagerFailureDefaultsToFailed(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	cfg.Workflow.QueuePollInterval = 0
	store := testsupport.MustOpenStore(t, cfg)

	failing := newStubStage("ripper")
	failing.executeErr = fmt.Errorf("boom")

	mgr := workflow.NewManager(cfg, store, logging.NewNop())
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
	mu             sync.Mutex
	queueStarts    []int
	queueCompletes []struct{ processed, failed int }
}

func (m *managerNotifier) Publish(ctx context.Context, event notifications.Event, payload notifications.Payload) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch event {
	case notifications.EventQueueStarted:
		if payload != nil {
			if count, ok := payload["count"].(int); ok {
				m.queueStarts = append(m.queueStarts, count)
			}
		}
	case notifications.EventQueueCompleted:
		processed := 0
		failed := 0
		if payload != nil {
			if val, ok := payload["processed"].(int); ok {
				processed = val
			}
			if val, ok := payload["failed"].(int); ok {
				failed = val
			}
		}
		m.queueCompletes = append(m.queueCompletes, struct{ processed, failed int }{processed: processed, failed: failed})
	}
	return nil
}

func (m *managerNotifier) startsCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queueStarts)
}

func (m *managerNotifier) completesCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queueCompletes)
}
