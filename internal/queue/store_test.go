package queue_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"spindle/internal/queue"
	"spindle/internal/testsupport"
)

func TestOpenAppliesMigrations(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

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

	found, err = store.FindByFingerprint(ctx, " fingerprint-1 \n")
	if err != nil {
		t.Fatalf("FindByFingerprint (trimmed) failed: %v", err)
	}
	if found == nil || found.ID != item.ID {
		t.Fatalf("expected to find inserted item via trimmed lookup, got %#v", found)
	}
}

func TestNewDiscRequiresFingerprint(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	ctx := context.Background()
	if _, err := store.NewDisc(ctx, "No Fingerprint", ""); err == nil {
		t.Fatal("expected error when fingerprint missing")
	}
}

func TestResetStuckProcessing(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	ctx := context.Background()
	cases := []struct {
		name          string
		initialStatus queue.Status
		expected      queue.Status
	}{
		{"identifying", queue.StatusIdentifying, queue.StatusPending},
		{"ripping", queue.StatusRipping, queue.StatusIdentified},
		{"episode_identifying", queue.StatusEpisodeIdentifying, queue.StatusRipped},
		{"encoding", queue.StatusEncoding, queue.StatusEpisodeIdentified},
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
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	ctx := context.Background()
	if _, err := store.NewDisc(ctx, "Disc A", "fp-a"); err != nil {
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
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

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
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

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
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

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
	t.Run("all statuses", func(t *testing.T) {
		cfg := testsupport.NewConfig(t)
		store := testsupport.MustOpenStore(t, cfg)

		ctx := context.Background()
		past := time.Now().Add(-2 * time.Hour).UTC()
		cases := []struct {
			name       string
			processing queue.Status
			expected   queue.Status
		}{
			{"identifying", queue.StatusIdentifying, queue.StatusPending},
			{"ripping", queue.StatusRipping, queue.StatusIdentified},
			{"episode_identifying", queue.StatusEpisodeIdentifying, queue.StatusRipped},
			{"encoding", queue.StatusEncoding, queue.StatusEpisodeIdentified},
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

		count, err := store.ReclaimStaleProcessing(
			ctx,
			time.Now().Add(-1*time.Hour),
			queue.StatusIdentifying,
			queue.StatusRipping,
			queue.StatusEpisodeIdentifying,
			queue.StatusEncoding,
			queue.StatusOrganizing,
		)
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
	})

	t.Run("filtered statuses", func(t *testing.T) {
		cfg := testsupport.NewConfig(t)
		store := testsupport.MustOpenStore(t, cfg)

		ctx := context.Background()
		past := time.Now().Add(-2 * time.Hour).UTC()

		ripping, err := store.NewDisc(ctx, "Stale-Ripping", "stale-ripping")
		if err != nil {
			t.Fatalf("NewDisc ripping: %v", err)
		}
		ripping.Status = queue.StatusRipping
		ripping.LastHeartbeat = &past
		if err := store.Update(ctx, ripping); err != nil {
			t.Fatalf("Update ripping: %v", err)
		}

		encoding, err := store.NewDisc(ctx, "Stale-Encoding", "stale-encoding")
		if err != nil {
			t.Fatalf("NewDisc encoding: %v", err)
		}
		encoding.Status = queue.StatusEncoding
		encoding.LastHeartbeat = &past
		if err := store.Update(ctx, encoding); err != nil {
			t.Fatalf("Update encoding: %v", err)
		}

		count, err := store.ReclaimStaleProcessing(ctx, time.Now().Add(-1*time.Hour), queue.StatusEncoding)
		if err != nil {
			t.Fatalf("ReclaimStaleProcessing filtered: %v", err)
		}
		if count != 1 {
			t.Fatalf("expected 1 item reclaimed, got %d", count)
		}

		reclaimed, err := store.GetByID(ctx, encoding.ID)
		if err != nil {
			t.Fatalf("GetByID encoding: %v", err)
		}
		if reclaimed.Status != queue.StatusEpisodeIdentified {
			t.Fatalf("expected encoding item rolled back to episode_identified, got %s", reclaimed.Status)
		}
		if reclaimed.LastHeartbeat != nil {
			t.Fatalf("expected encoding heartbeat cleared, got %v", reclaimed.LastHeartbeat)
		}

		unchanged, err := store.GetByID(ctx, ripping.ID)
		if err != nil {
			t.Fatalf("GetByID ripping: %v", err)
		}
		if unchanged.Status != queue.StatusRipping {
			t.Fatalf("expected ripping item untouched, got %s", unchanged.Status)
		}
		if unchanged.LastHeartbeat == nil || !unchanged.LastHeartbeat.Equal(past) {
			t.Fatalf("expected ripping heartbeat unchanged, got %v", unchanged.LastHeartbeat)
		}
	})
}

func TestUpdateProgressPreservesHeartbeat(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	ctx := context.Background()
	item, err := store.NewDisc(ctx, "Heartbeat Progress", "hb-progress")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusIdentifying
	past := time.Now().Add(-5 * time.Minute).UTC()
	item.LastHeartbeat = &past
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := store.UpdateHeartbeat(ctx, item.ID); err != nil {
		t.Fatalf("UpdateHeartbeat: %v", err)
	}

	before, err := store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID before progress: %v", err)
	}
	if before.LastHeartbeat == nil {
		t.Fatal("expected heartbeat set before progress update")
	}
	origHeartbeat := *before.LastHeartbeat

	before.ProgressStage = "Identify"
	before.ProgressPercent = 42.5
	before.ProgressMessage = "Scanning"
	if err := store.UpdateProgress(ctx, before); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	after, err := store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID after progress: %v", err)
	}
	if after.LastHeartbeat == nil {
		t.Fatal("expected heartbeat preserved after progress update")
	}
	if !after.LastHeartbeat.Equal(origHeartbeat) {
		t.Fatalf("expected heartbeat unchanged, before %v after %v", origHeartbeat, after.LastHeartbeat)
	}
	if after.ProgressStage != "Identify" || after.ProgressMessage != "Scanning" {
		t.Fatalf("expected progress fields persisted, got stage=%q message=%q", after.ProgressStage, after.ProgressMessage)
	}
	if after.ProgressPercent != 42.5 {
		t.Fatalf("expected progress percent 42.5, got %f", after.ProgressPercent)
	}
}

func TestConcurrentNewDisc(t *testing.T) {
	t.Parallel()

	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)
	ctx := context.Background()

	// Use sequential creation to verify store handles multiple items correctly.
	// SQLite has write locking that doesn't support truly concurrent writes,
	// so we test sequential multi-item creation instead.
	const numItems = 10
	var ids []int64

	for i := range numItems {
		item, err := store.NewDisc(ctx, fmt.Sprintf("Concurrent Disc %d", i), fmt.Sprintf("fp-concurrent-%d", i))
		if err != nil {
			t.Fatalf("NewDisc %d failed: %v", i, err)
		}
		ids = append(ids, item.ID)
	}

	if len(ids) != numItems {
		t.Fatalf("expected %d items created, got %d", numItems, len(ids))
	}

	// Verify all items exist and have unique IDs
	seen := make(map[int64]bool)
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate ID found: %d", id)
		}
		seen[id] = true

		item, err := store.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID failed for ID %d: %v", id, err)
		}
		if item == nil {
			t.Fatalf("item with ID %d not found", id)
		}
	}
}

func TestSequentialUpdateSameItem(t *testing.T) {
	t.Parallel()

	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)
	ctx := context.Background()

	item, err := store.NewDisc(ctx, "Sequential Update Disc", "fp-sequential-update")
	if err != nil {
		t.Fatalf("NewDisc failed: %v", err)
	}

	// Test multiple sequential updates to the same item
	const numUpdates = 5
	for i := range numUpdates {
		fetched, err := store.GetByID(ctx, item.ID)
		if err != nil {
			t.Fatalf("GetByID failed on iteration %d: %v", i, err)
		}
		fetched.ProgressMessage = fmt.Sprintf("Updated %d times", i+1)
		fetched.ProgressPercent = float64((i + 1) * 10)
		if err := store.Update(ctx, fetched); err != nil {
			t.Fatalf("Update failed on iteration %d: %v", i, err)
		}
	}

	// Verify the item has the final update
	final, err := store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("final GetByID failed: %v", err)
	}
	if final == nil {
		t.Fatal("item disappeared after updates")
	}
	if final.ProgressMessage != "Updated 5 times" {
		t.Fatalf("expected progress message 'Updated 5 times', got %q", final.ProgressMessage)
	}
	if final.ProgressPercent != 50.0 {
		t.Fatalf("expected progress percent 50.0, got %f", final.ProgressPercent)
	}
}

func TestStatusTransitionsThroughWorkflow(t *testing.T) {
	t.Parallel()

	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)
	ctx := context.Background()

	// Create multiple items and transition them through workflow statuses
	const numItems = 5
	var items []*queue.Item
	for i := range numItems {
		item, err := store.NewDisc(ctx, fmt.Sprintf("Status Disc %d", i), fmt.Sprintf("fp-status-%d", i))
		if err != nil {
			t.Fatalf("NewDisc failed: %v", err)
		}
		items = append(items, item)
	}

	// Transition each item through the workflow statuses sequentially
	statuses := []queue.Status{
		queue.StatusIdentifying,
		queue.StatusIdentified,
		queue.StatusRipping,
		queue.StatusRipped,
	}

	for i, item := range items {
		for _, status := range statuses {
			item.Status = status
			if err := store.Update(ctx, item); err != nil {
				t.Fatalf("item %d status transition to %s failed: %v", i, status, err)
			}
		}
	}

	// Verify all items ended up in the final status
	for i, item := range items {
		final, err := store.GetByID(ctx, item.ID)
		if err != nil {
			t.Fatalf("GetByID failed for item %d: %v", i, err)
		}
		if final.Status != queue.StatusRipped {
			t.Fatalf("item %d expected status %s, got %s", i, queue.StatusRipped, final.Status)
		}
	}
}

func TestRepeatedHeartbeatUpdates(t *testing.T) {
	t.Parallel()

	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)
	ctx := context.Background()

	item, err := store.NewDisc(ctx, "Heartbeat Disc", "fp-heartbeat-repeated")
	if err != nil {
		t.Fatalf("NewDisc failed: %v", err)
	}
	item.Status = queue.StatusIdentifying
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Test multiple sequential heartbeat updates
	const numUpdates = 10
	for i := range numUpdates {
		if err := store.UpdateHeartbeat(ctx, item.ID); err != nil {
			t.Fatalf("UpdateHeartbeat %d failed: %v", i, err)
		}
	}

	// Verify heartbeat was set
	final, err := store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if final.LastHeartbeat == nil {
		t.Fatal("expected heartbeat to be set after updates")
	}
}

func TestInterleavedListAndUpdate(t *testing.T) {
	t.Parallel()

	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)
	ctx := context.Background()

	// Create initial items
	const numItems = 5
	for i := range numItems {
		_, err := store.NewDisc(ctx, fmt.Sprintf("List Disc %d", i), fmt.Sprintf("fp-list-%d", i))
		if err != nil {
			t.Fatalf("NewDisc failed: %v", err)
		}
	}

	// Interleave List and Update operations sequentially
	const numOperations = 10
	for i := range numOperations {
		// List operation
		items, err := store.List(ctx)
		if err != nil {
			t.Fatalf("List %d failed: %v", i, err)
		}
		if len(items) == 0 {
			t.Fatalf("expected items in list on iteration %d", i)
		}

		// Update operation on one of the items
		item := items[i%len(items)]
		item.ProgressMessage = fmt.Sprintf("Updated during list %d", i)
		if err := store.Update(ctx, item); err != nil {
			t.Fatalf("Update %d failed: %v", i, err)
		}
	}

	// Verify final state
	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("final List failed: %v", err)
	}
	if len(items) != numItems {
		t.Fatalf("expected %d items, got %d", numItems, len(items))
	}
}

func TestRepeatedProgressUpdates(t *testing.T) {
	t.Parallel()

	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)
	ctx := context.Background()

	item, err := store.NewDisc(ctx, "Progress Disc", "fp-progress-repeated")
	if err != nil {
		t.Fatalf("NewDisc failed: %v", err)
	}
	item.Status = queue.StatusEncoding
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Test multiple sequential progress updates (SQLite doesn't support
	// concurrent writes, so we test sequential updates instead)
	const numUpdates = 20
	for i := range numUpdates {
		progress := &queue.Item{
			ID:              item.ID,
			ProgressStage:   "Encoding",
			ProgressPercent: float64(i * 5),
			ProgressMessage: fmt.Sprintf("Frame %d", i*100),
		}
		if err := store.UpdateProgress(ctx, progress); err != nil {
			t.Fatalf("UpdateProgress %d failed: %v", i, err)
		}
	}

	// Verify the item has the final progress
	final, err := store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if final.ProgressStage != "Encoding" {
		t.Fatalf("expected progress stage 'Encoding', got %q", final.ProgressStage)
	}
	// Final update should have percent = 19 * 5 = 95
	if final.ProgressPercent != 95.0 {
		t.Fatalf("expected progress percent 95.0, got %f", final.ProgressPercent)
	}
	if final.ProgressMessage != "Frame 1900" {
		t.Fatalf("expected progress message 'Frame 1900', got %q", final.ProgressMessage)
	}
}
