package main

import (
	"context"
	"encoding/json"
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

	out, _, err = runCLI(t, []string{"queue", "clear", "--all"}, env.socketPath, env.configPath)
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

func TestQueueListJSON(t *testing.T) {
	env := setupCLITestEnv(t)
	ctx := context.Background()

	if _, err := env.store.NewDisc(ctx, "Alpha", "fp-alpha"); err != nil {
		t.Fatalf("alpha disc: %v", err)
	}
	if _, err := env.store.NewDisc(ctx, "Beta", "fp-beta"); err != nil {
		t.Fatalf("beta disc: %v", err)
	}

	out, _, err := runCLI(t, []string{"queue", "list", "--json"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue list --json: %v", err)
	}

	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	for _, item := range items {
		if _, ok := item["id"]; !ok {
			t.Fatal("missing 'id' key in JSON item")
		}
		if _, ok := item["status"]; !ok {
			t.Fatal("missing 'status' key in JSON item")
		}
	}
}

func TestQueueListJSONEmpty(t *testing.T) {
	env := setupCLITestEnv(t)

	out, _, err := runCLI(t, []string{"queue", "list", "--json"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue list --json empty: %v", err)
	}

	var items []any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if len(items) != 0 {
		t.Fatalf("expected empty array, got %d items", len(items))
	}
}

func TestQueueStatusJSON(t *testing.T) {
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

	out, _, err := runCLI(t, []string{"queue", "status", "--json"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue status --json: %v", err)
	}

	var stats map[string]any
	if err := json.Unmarshal([]byte(out), &stats); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if _, ok := stats["pending"]; !ok {
		t.Fatalf("expected 'pending' key in status JSON, got: %v", stats)
	}
	if _, ok := stats["failed"]; !ok {
		t.Fatalf("expected 'failed' key in status JSON, got: %v", stats)
	}
}

func TestQueueShowJSON(t *testing.T) {
	env := setupCLITestEnv(t)
	ctx := context.Background()

	item, err := env.store.NewDisc(ctx, "Alpha", "fp-alpha")
	if err != nil {
		t.Fatalf("alpha disc: %v", err)
	}

	out, _, err := runCLI(t, []string{"queue", "show", fmt.Sprintf("%d", item.ID), "--json"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue show --json: %v", err)
	}

	var detail map[string]any
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if detail["id"] != float64(item.ID) {
		t.Fatalf("expected id %d, got %v", item.ID, detail["id"])
	}
	if detail["discTitle"] != "Alpha" {
		t.Fatalf("expected discTitle Alpha, got %v", detail["discTitle"])
	}
}

func TestQueueShowJSONIncludesEpisodeIdentifiedCount(t *testing.T) {
	env := setupCLITestEnv(t)
	ctx := context.Background()

	item, err := env.store.NewDisc(ctx, "Alpha", "fp-alpha")
	if err != nil {
		t.Fatalf("alpha disc: %v", err)
	}
	item.RipSpecData = `{"episodes":[{"key":"s01_001","season":1,"episode":1}]}`
	if err := env.store.Update(ctx, item); err != nil {
		t.Fatalf("update item: %v", err)
	}

	out, _, err := runCLI(t, []string{"queue", "show", fmt.Sprintf("%d", item.ID), "--json"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue show --json: %v", err)
	}

	var detail map[string]any
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if detail["episodeIdentifiedCount"] != float64(1) {
		t.Fatalf("expected episodeIdentifiedCount 1, got %v", detail["episodeIdentifiedCount"])
	}
}

func TestQueueShowJSONNotFound(t *testing.T) {
	env := setupCLITestEnv(t)

	out, _, err := runCLI(t, []string{"queue", "show", "9999", "--json"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue show --json not found: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if result["error"] != "not_found" {
		t.Fatalf("expected error=not_found, got %v", result["error"])
	}
}

func TestQueueHealthJSON(t *testing.T) {
	env := setupCLITestEnv(t)
	ctx := context.Background()

	if _, err := env.store.NewDisc(ctx, "Alpha", "fp-alpha"); err != nil {
		t.Fatalf("alpha disc: %v", err)
	}

	out, _, err := runCLI(t, []string{"queue", "health", "--json"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue health --json: %v", err)
	}

	var health map[string]any
	if err := json.Unmarshal([]byte(out), &health); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	for _, key := range []string{"total", "pending", "processing", "failed", "completed"} {
		if _, ok := health[key]; !ok {
			t.Fatalf("missing %q key in health JSON", key)
		}
	}
	if health["total"] != float64(1) {
		t.Fatalf("expected total=1, got %v", health["total"])
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
	requireContains(t, out, "Episodes:")
	requireContains(t, out, "S05E01")
}
