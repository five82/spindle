package daemon_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"spindle/internal/daemon"
	"spindle/internal/logging"
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

func TestDaemonStartStop(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries("makemkvcon", "drapto", "ffmpeg"))
	store := testsupport.MustOpenStore(t, cfg)
	logPath := filepath.Join(cfg.Paths.LogDir, "daemon-test.log")
	logger := logging.NewNop()
	mgr := workflow.NewManager(cfg, store, logger)
	mgr.ConfigureStages(workflow.StageSet{Identifier: noopStage{}})
	d, err := daemon.New(cfg, store, logger, mgr, logPath, logging.NewStreamHub(128), nil)
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
		if !dep.Available {
			t.Fatalf("expected dependency %s to be available, got detail %q", dep.Name, dep.Detail)
		}
	}

	// Second start should fail
	if err := d.Start(ctx); err == nil {
		t.Fatal("expected second start to fail")
	}

	d.Stop()
	time.Sleep(50 * time.Millisecond)
	status = d.Status(ctx)
	if status.Running {
		t.Fatal("expected daemon to be stopped")
	}
}
