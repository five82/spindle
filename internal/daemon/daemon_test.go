package daemon_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/daemon"
	"spindle/internal/queue"
	"spindle/internal/stage"
	"spindle/internal/workflow"
)

type noopStage struct{}

func (noopStage) Prepare(context.Context, *queue.Item) error { return nil }
func (noopStage) Execute(context.Context, *queue.Item) error { return nil }
func (noopStage) HealthCheck(context.Context) stage.Health {
	return stage.Healthy("noop")
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
	binDir := filepath.Join(base, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range []string{"makemkvcon", "drapto", "ffmpeg"} {
		path := filepath.Join(binDir, name)
		script := []byte("#!/bin/sh\nexit 0\n")
		if err := os.WriteFile(path, script, 0o755); err != nil {
			t.Fatalf("write stub %s: %v", name, err)
		}
	}
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatalf("set PATH: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})
	return &cfg
}

func TestDaemonStartStop(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	logger := zap.NewNop()
	mgr := workflow.NewManager(cfg, store, logger)
	mgr.ConfigureStages(workflow.StageSet{Identifier: noopStage{}})
	d, err := daemon.New(cfg, store, logger, mgr)
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
