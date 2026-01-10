package main

import (
	"context"
	"fmt"
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

func TestQueueStopSpecificID(t *testing.T) {
	env := setupCLITestEnv(t)
	ctx := context.Background()

	item, err := env.store.NewDisc(ctx, "Alpha", "fp-alpha")
	if err != nil {
		t.Fatalf("alpha: %v", err)
	}
	item.Status = queue.StatusEncoding
	if err := env.store.Update(ctx, item); err != nil {
		t.Fatalf("alpha encoding: %v", err)
	}

	out, _, err := runCLI(t, []string{"queue", "stop", fmt.Sprintf("%d", item.ID)}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue stop: %v", err)
	}
	requireContains(t, out, "stop requested")
	requireContains(t, out, "will halt after current stage")

	updated, err := env.store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("lookup alpha: %v", err)
	}
	if updated.Status != queue.StatusFailed {
		t.Fatalf("expected failed, got %s", updated.Status)
	}
	if updated.ReviewReason != queue.UserStopReason {
		t.Fatalf("expected review reason %q, got %q", queue.UserStopReason, updated.ReviewReason)
	}
	if !updated.NeedsReview {
		t.Fatalf("expected needs_review to be true")
	}
}

func TestQueueRetryInvalidID(t *testing.T) {
	env := setupCLITestEnv(t)

	_, _, err := runCLI(t, []string{"queue", "retry", "abc"}, env.socketPath, env.configPath)
	if err == nil || !strings.Contains(err.Error(), "invalid item id") {
		t.Fatalf("expected invalid id error, got %v", err)
	}
}

func TestQueueShowDisplaysFingerprints(t *testing.T) {
	env := setupCLITestEnv(t)
	ctx := context.Background()

	item, err := env.store.NewDisc(ctx, "Showcase", "fp-showcase")
	if err != nil {
		t.Fatalf("new disc: %v", err)
	}
	item.Status = queue.StatusIdentified
	item.ProgressStage = "Identified"
	item.ProgressPercent = 100
	item.RipSpecData = `{"content_key":"tmdb:tv:123","titles":[{"id":1,"name":"Episode 1","duration":1800,"title_hash":"abc123"}],"episodes":[{"key":"s05e01","title_id":1,"season":5,"episode":1,"episode_title":"Pilot"}],"assets":{"encoded":[{"episode_key":"s05e01","path":"/encoded/Show - S05E01.mkv"}]}}`
	item.MetadataJSON = `{"title":"Showcase"}`
	if err := env.store.Update(ctx, item); err != nil {
		t.Fatalf("update item: %v", err)
	}

	out, _, err := runCLI(t, []string{"queue", "show", fmt.Sprintf("%d", item.ID)}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue show: %v", err)
	}
	requireContains(t, out, "Content Key: tmdb:tv:123")
	requireContains(t, out, "Fingerprint abc123")
	requireContains(t, out, "Drapto Preset: Default")
	requireContains(t, out, "Episodes:")
	requireContains(t, out, "S05E01")
}
