package queue_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"spindle/internal/config"
	"spindle/internal/queue"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	cfg := config.Default()
	cfg.TMDBAPIKey = "test"
	cfg.StagingDir = filepath.Join(base, "staging")
	cfg.LibraryDir = filepath.Join(base, "library")
	cfg.LogDir = filepath.Join(base, "logs")
	cfg.ReviewDir = filepath.Join(base, "review")
	return &cfg
}

func TestOpenAppliesMigrations(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})

	ctx := context.Background()
	item, err := store.NewDisc(ctx, "Sample Disc", "fingerprint-1")
	if err != nil {
		t.Fatalf("NewDisc failed: %v", err)
	}
	if item.ID == 0 {
		t.Fatal("expected item ID to be assigned")
	}

	fetched, err := store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if fetched == nil || fetched.DiscTitle != "Sample Disc" {
		t.Fatalf("unexpected fetched item: %#v", fetched)
	}

	found, err := store.FindByFingerprint(ctx, "fingerprint-1")
	if err != nil {
		t.Fatalf("FindByFingerprint failed: %v", err)
	}
	if found == nil || found.ID != item.ID {
		t.Fatalf("expected to find inserted item, got %#v", found)
	}
}

func TestNewFileSetsDefaults(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()
	manualPath := filepath.Join(cfg.StagingDir, "manual", "Sample Movie.mkv")

	item, err := store.NewFile(ctx, manualPath)
	if err != nil {
		t.Fatalf("NewFile failed: %v", err)
	}
	if item.Status != queue.StatusRipped {
		t.Fatalf("expected ripped status, got %s", item.Status)
	}
	if item.RippedFile != manualPath {
		t.Fatalf("expected ripped file to match source, got %s", item.RippedFile)
	}
	if item.DiscTitle != "Sample Movie" {
		t.Fatalf("unexpected disc title: %s", item.DiscTitle)
	}
	if item.MetadataJSON == "" {
		t.Fatal("expected metadata json to be populated")
	}
	meta := queue.MetadataFromJSON(item.MetadataJSON, "")
	if meta.Title() != "Sample Movie" {
		t.Fatalf("expected metadata title to match, got %s", meta.Title())
	}
	if meta.GetFilename() == "" {
		t.Fatal("expected metadata filename to be populated")
	}
}

func TestResetStuckProcessing(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})

	ctx := context.Background()
	cases := []struct {
		name          string
		initialStatus queue.Status
		expected      queue.Status
	}{
		{"identifying", queue.StatusIdentifying, queue.StatusPending},
		{"ripping", queue.StatusRipping, queue.StatusIdentified},
		{"encoding", queue.StatusEncoding, queue.StatusRipped},
		{"organizing", queue.StatusOrganizing, queue.StatusEncoded},
	}
	var ids []int64
	for i, tc := range cases {
		item, err := store.NewDisc(ctx, fmt.Sprintf("Disc-%s", tc.name), fmt.Sprintf("fingerprint-reset-%d", i))
		if err != nil {
			t.Fatalf("NewDisc failed: %v", err)
		}
		item.Status = tc.initialStatus
		item.ProgressStage = tc.name
		if err := store.Update(ctx, item); err != nil {
			t.Fatalf("Update failed: %v", err)
		}
		ids = append(ids, item.ID)
	}

	count, err := store.ResetStuckProcessing(ctx)
	if err != nil {
		t.Fatalf("ResetStuckProcessing failed: %v", err)
	}
	if int(count) != len(cases) {
		t.Fatalf("expected %d items reset, got %d", len(cases), count)
	}

	for idx, tc := range cases {
		updated, err := store.GetByID(ctx, ids[idx])
		if err != nil {
			t.Fatalf("GetByID failed: %v", err)
		}
		if updated.Status != tc.expected {
			t.Fatalf("%s: expected status %s, got %s", tc.name, tc.expected, updated.Status)
		}
		if updated.LastHeartbeat != nil {
			t.Fatalf("%s: expected heartbeat cleared", tc.name)
		}
	}
}

func TestItemsByStatusOrdering(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})

	ctx := context.Background()
	_, err = store.NewDisc(ctx, "Disc A", "fp-a")
	if err != nil {
		t.Fatalf("NewDisc failed: %v", err)
	}
	b, err := store.NewDisc(ctx, "Disc B", "fp-b")
	if err != nil {
		t.Fatalf("NewDisc failed: %v", err)
	}
	b.Status = queue.StatusIdentified
	if err := store.Update(ctx, b); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	items, err := store.ItemsByStatus(ctx, queue.StatusIdentified)
	if err != nil {
		t.Fatalf("ItemsByStatus failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one identified item, got %d", len(items))
	}
	if items[0].DiscTitle != "Disc B" {
		t.Fatalf("expected Disc B, got %s", items[0].DiscTitle)
	}
}

func TestListSupportsStatusFilter(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})

	ctx := context.Background()
	a, err := store.NewDisc(ctx, "Disc A", "fp-a")
	if err != nil {
		t.Fatalf("NewDisc failed: %v", err)
	}
	b, err := store.NewDisc(ctx, "Disc B", "fp-b")
	if err != nil {
		t.Fatalf("NewDisc failed: %v", err)
	}
	b.Status = queue.StatusIdentified
	if err := store.Update(ctx, b); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	c, err := store.NewDisc(ctx, "Disc C", "fp-c")
	if err != nil {
		t.Fatalf("NewDisc failed: %v", err)
	}
	c.Status = queue.StatusFailed
	c.ErrorMessage = "boom"
	if err := store.Update(ctx, c); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0].ID != a.ID || items[1].ID != b.ID || items[2].ID != c.ID {
		t.Fatalf("expected order A,B,C, got IDs %d,%d,%d", items[0].ID, items[1].ID, items[2].ID)
	}

	filtered, err := store.List(ctx, queue.StatusIdentified, queue.StatusFailed)
	if err != nil {
		t.Fatalf("Filtered list failed: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 items, got %d", len(filtered))
	}
	if filtered[0].ID != b.ID || filtered[1].ID != c.ID {
		t.Fatalf("unexpected filtered order: got %d,%d", filtered[0].ID, filtered[1].ID)
	}
}

func TestRetryFailed(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})

	ctx := context.Background()
	a, err := store.NewDisc(ctx, "ItemA", "fp-a")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	b, err := store.NewDisc(ctx, "ItemB", "fp-b")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	for _, item := range []*queue.Item{a, b} {
		item.Status = queue.StatusFailed
		item.ErrorMessage = "boom"
		if err := store.Update(ctx, item); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}

	updated, err := store.RetryFailed(ctx)
	if err != nil {
		t.Fatalf("RetryFailed all: %v", err)
	}
	if updated != 2 {
		t.Fatalf("expected 2 items retried, got %d", updated)
	}

	item, err := store.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if item.Status != queue.StatusPending {
		t.Fatalf("expected item A pending, got %s", item.Status)
	}

	// Mark B failed again and retry targeted selection.
	b.Status = queue.StatusFailed
	if err := store.Update(ctx, b); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err = store.RetryFailed(ctx, b.ID)
	if err != nil {
		t.Fatalf("RetryFailed targeted: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expected 1 item retried, got %d", updated)
	}
}

func TestUpdateHeartbeat(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})

	ctx := context.Background()
	item, err := store.NewDisc(ctx, "Heartbeat", "hb")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusIdentifying
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := store.UpdateHeartbeat(ctx, item.ID); err != nil {
		t.Fatalf("UpdateHeartbeat: %v", err)
	}

	updated, err := store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.LastHeartbeat == nil {
		t.Fatal("expected last heartbeat to be set")
	}
}

func TestReclaimStaleProcessing(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})

	ctx := context.Background()
	past := time.Now().Add(-2 * time.Hour).UTC()
	cases := []struct {
		name       string
		processing queue.Status
		expected   queue.Status
	}{
		{"identifying", queue.StatusIdentifying, queue.StatusPending},
		{"ripping", queue.StatusRipping, queue.StatusIdentified},
		{"encoding", queue.StatusEncoding, queue.StatusRipped},
		{"organizing", queue.StatusOrganizing, queue.StatusEncoded},
	}
	var ids []int64
	for i, tc := range cases {
		item, err := store.NewDisc(ctx, fmt.Sprintf("Stale-%s", tc.name), fmt.Sprintf("stale-%d", i))
		if err != nil {
			t.Fatalf("NewDisc: %v", err)
		}
		item.Status = tc.processing
		item.LastHeartbeat = &past
		if err := store.Update(ctx, item); err != nil {
			t.Fatalf("Update: %v", err)
		}
		ids = append(ids, item.ID)
	}

	count, err := store.ReclaimStaleProcessing(ctx, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("ReclaimStaleProcessing: %v", err)
	}
	if int(count) != len(cases) {
		t.Fatalf("expected %d items reclaimed, got %d", len(cases), count)
	}

	for idx, tc := range cases {
		updated, err := store.GetByID(ctx, ids[idx])
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if updated.Status != tc.expected {
			t.Fatalf("%s: expected status %s after reclaim, got %s", tc.name, tc.expected, updated.Status)
		}
		if updated.LastHeartbeat != nil {
			t.Fatalf("%s: expected heartbeat cleared, got %v", tc.name, updated.LastHeartbeat)
		}
	}
}
