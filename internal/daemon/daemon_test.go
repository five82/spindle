package daemon_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"spindle/internal/daemon"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/stage"
	"spindle/internal/testsupport"
	"spindle/internal/workflow"
)

type noopStage struct{}

func (noopStage) Prepare(context.Context, *queue.Item) error { return nil }
func (noopStage) Execute(context.Context, *queue.Item) error { return nil }
func (noopStage) HealthCheck(context.Context) stage.Health {
	return stage.Healthy("noop")
}

func TestDiscAccessHooksStopStartNetlink(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)
	logPath := filepath.Join(cfg.Paths.LogDir, "daemon-dischooks-test.log")
	logger := logging.NewNop()
	mgr := workflow.NewManager(cfg, store, logger)
	mgr.ConfigureStages(workflow.StageSet{Identifier: noopStage{}})
	d, err := daemon.New(cfg, store, logger, mgr, logPath, logging.NewStreamHub(128), nil, notifications.NewService(cfg))
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	ctx := context.Background()

	// Before starting the daemon, hooks should still be safe
	// (netlink monitor exists but was never started - no netlink privileges in CI).
	d.BeforeDiscAccess()
	if !d.DiscPaused() {
		t.Fatal("expected DiscPaused to be true after BeforeDiscAccess")
	}
	status := d.Status(ctx)
	if !status.NetlinkPausedForDisc {
		t.Fatal("expected NetlinkPausedForDisc to be true during disc access")
	}

	d.AfterDiscAccess()
	if d.DiscPaused() {
		t.Fatal("expected DiscPaused to be false after AfterDiscAccess")
	}
	status = d.Status(ctx)
	if status.NetlinkPausedForDisc {
		t.Fatal("expected NetlinkPausedForDisc to be false after disc access")
	}

	// Repeated calls should not panic
	d.BeforeDiscAccess()
	d.BeforeDiscAccess() // double call
	if !d.DiscPaused() {
		t.Fatal("expected DiscPaused to remain true after double BeforeDiscAccess")
	}

	d.AfterDiscAccess()
	d.AfterDiscAccess() // double call
	if d.DiscPaused() {
		t.Fatal("expected DiscPaused to remain false after double AfterDiscAccess")
	}
}

func TestDaemonStartStop(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)
	logPath := filepath.Join(cfg.Paths.LogDir, "daemon-test.log")
	logger := logging.NewNop()
	mgr := workflow.NewManager(cfg, store, logger)
	mgr.ConfigureStages(workflow.StageSet{Identifier: noopStage{}})
	d, err := daemon.New(cfg, store, logger, mgr, logPath, logging.NewStreamHub(128), nil, notifications.NewService(cfg))
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	t.Cleanup(func() {
		d.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	status := d.Status(ctx)
	if !status.Running {
		t.Fatal("expected daemon to report running")
	}
	if len(status.Dependencies) == 0 {
		t.Fatal("expected dependency status to be populated")
	}
	for _, dep := range status.Dependencies {
		if dep.Optional {
			continue
		}
		if !dep.Available {
			t.Fatalf("expected dependency %s to be available, got detail %q", dep.Name, dep.Detail)
		}
	}

	// Second start should fail
	if err := d.Start(ctx); err == nil {
		t.Fatal("expected second start to fail")
	}

	d.Stop(context.Background())
	time.Sleep(50 * time.Millisecond)
	status = d.Status(ctx)
	if status.Running {
		t.Fatal("expected daemon to be stopped")
	}
}
