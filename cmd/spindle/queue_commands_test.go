package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spindle/internal/queue"
)

func TestQueueStatusAndList(t *testing.T) {
	env := setupCLITestEnv(t)
	ctx := context.Background()

	if _, err := env.store.NewDisc(ctx, "Alpha", "fp-alpha"); err != nil {
		t.Fatalf("alpha disc: %v", err)
	}

	beta, err := env.store.NewDisc(ctx, "Beta", "fp-beta")
	if err != nil {
		t.Fatalf("beta disc: %v", err)
	}
	beta.Status = queue.StatusFailed
	if err := env.store.Update(ctx, beta); err != nil {
		t.Fatalf("beta failed: %v", err)
	}

	out, _, err := runCLI(t, []string{"queue", "status"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue status: %v", err)
	}
	requireContains(t, out, "Pending")
	requireContains(t, out, "Failed")

	out, _, err = runCLI(t, []string{"queue", "list"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue list: %v", err)
	}
	requireContains(t, out, "Alpha")
	requireContains(t, out, "Beta")
}

func TestQueueRetryAndClear(t *testing.T) {
	env := setupCLITestEnv(t)
	ctx := context.Background()

	alpha, err := env.store.NewDisc(ctx, "Alpha", "fp-alpha")
	if err != nil {
		t.Fatalf("alpha: %v", err)
	}
	alpha.Status = queue.StatusFailed
	if err := env.store.Update(ctx, alpha); err != nil {
		t.Fatalf("alpha failed: %v", err)
	}

	out, _, err := runCLI(t, []string{"queue", "retry"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue retry: %v", err)
	}
	requireContains(t, out, "Retried 1 failed items")

	updated, err := env.store.GetByID(ctx, alpha.ID)
	if err != nil {
		t.Fatalf("lookup alpha: %v", err)
	}
	if updated.Status != queue.StatusPending {
		t.Fatalf("expected pending, got %s", updated.Status)
	}

	updated.Status = queue.StatusFailed
	if err := env.store.Update(ctx, updated); err != nil {
		t.Fatalf("reset failed: %v", err)
	}

	out, _, err = runCLI(t, []string{"queue", "clear", "--failed"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue clear failed: %v", err)
	}
	requireContains(t, out, "Cleared 1 failed items")

	out, _, err = runCLI(t, []string{"queue", "clear"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue clear all: %v", err)
	}
	requireContains(t, out, "Cleared")
}

func TestAddFile(t *testing.T) {
	env := setupCLITestEnv(t)

	manualDir := filepath.Join(env.cfg.StagingDir, "manual")
	if err := os.MkdirAll(manualDir, 0o755); err != nil {
		t.Fatalf("mkdir manual: %v", err)
	}
	manualPath := filepath.Join(manualDir, "Manual Movie.mkv")
	if err := os.WriteFile(manualPath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write manual: %v", err)
	}

	out, _, err := runCLI(t, []string{"add-file", manualPath}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("add-file: %v", err)
	}
	requireContains(t, out, "Queued manual file")
}

func TestQueueHealthCommand(t *testing.T) {
	env := setupCLITestEnv(t)

	out, _, err := runCLI(t, []string{"queue-health"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue-health: %v", err)
	}
	requireContains(t, out, "Database path:")
	requireContains(t, out, "queue_items table present:")
}

func TestQueueRetrySpecificID(t *testing.T) {
	env := setupCLITestEnv(t)
	ctx := context.Background()

	alpha, err := env.store.NewDisc(ctx, "Alpha", "fp-alpha")
	if err != nil {
		t.Fatalf("alpha: %v", err)
	}
	alpha.Status = queue.StatusFailed
	if err := env.store.Update(ctx, alpha); err != nil {
		t.Fatalf("alpha failed: %v", err)
	}

	out, _, err := runCLI(t, []string{"queue", "retry", fmt.Sprintf("%d", alpha.ID)}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue retry specific: %v", err)
	}
	requireContains(t, out, fmt.Sprintf("Item %d reset for retry", alpha.ID))
}

func TestQueueRetryInvalidID(t *testing.T) {
	env := setupCLITestEnv(t)

	_, _, err := runCLI(t, []string{"queue", "retry", "abc"}, env.socketPath, env.configPath)
	if err == nil || !strings.Contains(err.Error(), "invalid item id") {
		t.Fatalf("expected invalid id error, got %v", err)
	}
}
