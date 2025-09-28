package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/disc"
	"spindle/internal/queue"
)

type stubDiscScanner struct {
	result *disc.ScanResult
	err    error
	calls  atomic.Int32
}

func (s *stubDiscScanner) Scan(ctx context.Context, device string) (*disc.ScanResult, error) {
	s.calls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	if s.result != nil {
		return s.result, nil
	}
	return &disc.ScanResult{Fingerprint: "fp-default"}, nil
}

func testMonitorConfig(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	cfg := config.Default()
	cfg.TMDBAPIKey = "test-key"
	cfg.StagingDir = base + "/staging"
	cfg.LibraryDir = base + "/library"
	cfg.LogDir = base + "/logs"
	cfg.ReviewDir = base + "/review"
	cfg.DiscMonitorTimeout = 1
	return &cfg
}

func waitForItems(t *testing.T, store *queue.Store, expected int) []*queue.Item {
	t.Helper()
	deadline := time.After(500 * time.Millisecond)
	for {
		items, err := store.List(context.Background())
		if err != nil {
			t.Fatalf("store.List: %v", err)
		}
		if len(items) == expected {
			return items
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for %d items (found %d)", expected, len(items))
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func waitForStatus(t *testing.T, store *queue.Store, id int64, status queue.Status) *queue.Item {
	t.Helper()
	deadline := time.After(500 * time.Millisecond)
	for {
		item, err := store.GetByID(context.Background(), id)
		if err != nil {
			t.Fatalf("store.GetByID: %v", err)
		}
		if item.Status == status {
			return item
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for status %s (latest %s)", status, item.Status)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestDiscMonitorQueuesNewDisc(t *testing.T) {
	cfg := testMonitorConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	monitor := newDiscMonitor(cfg, store, zap.NewNop())
	if monitor == nil {
		t.Fatal("expected monitor to be created")
	}

	monitor.pollInterval = 10 * time.Millisecond

	scanner := &stubDiscScanner{result: &disc.ScanResult{Fingerprint: "fp-demo"}}
	monitor.scanner = scanner

	var detectCalls atomic.Int32
	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		if detectCalls.Add(1) == 1 {
			return &discInfo{Device: device, Label: "Demo Disc", Type: "Blu-ray"}, nil
		}
		return nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("monitor.Start: %v", err)
	}
	t.Cleanup(func() { monitor.Stop() })

	items := waitForItems(t, store, 1)
	if items[0].DiscTitle != "Demo Disc" {
		t.Fatalf("unexpected disc title %q", items[0].DiscTitle)
	}
	if items[0].DiscFingerprint != "fp-demo" {
		t.Fatalf("unexpected fingerprint %q", items[0].DiscFingerprint)
	}
	if items[0].Status != queue.StatusPending {
		t.Fatalf("expected status pending, got %s", items[0].Status)
	}
}

func TestDiscMonitorResetsExistingItem(t *testing.T) {
	cfg := testMonitorConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()
	item, err := store.NewDisc(ctx, "Old Title", "fp-demo")
	if err != nil {
		t.Fatalf("store.NewDisc: %v", err)
	}
	item.Status = queue.StatusFailed
	item.ProgressStage = "Failed earlier"
	item.ProgressPercent = 42
	item.ProgressMessage = "Something broke"
	item.NeedsReview = true
	item.ReviewReason = "manual"
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("store.Update: %v", err)
	}

	monitor := newDiscMonitor(cfg, store, zap.NewNop())
	if monitor == nil {
		t.Fatal("expected monitor to be created")
	}

	monitor.pollInterval = 10 * time.Millisecond
	monitor.scanner = &stubDiscScanner{result: &disc.ScanResult{Fingerprint: "fp-demo"}}

	var detectCalls atomic.Int32
	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		if detectCalls.Add(1) == 1 {
			return &discInfo{Device: device, Label: "Refreshed Title", Type: "Blu-ray"}, nil
		}
		return nil, nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := monitor.Start(runCtx); err != nil {
		t.Fatalf("monitor.Start: %v", err)
	}
	t.Cleanup(func() { monitor.Stop() })

	updated := waitForStatus(t, store, item.ID, queue.StatusPending)
	if updated.Status != queue.StatusPending {
		t.Fatalf("expected pending status, got %s", updated.Status)
	}
	if updated.ProgressStage != "Awaiting identification" {
		t.Fatalf("unexpected progress stage %q", updated.ProgressStage)
	}
	if updated.NeedsReview {
		t.Fatal("expected NeedsReview to be false")
	}
	if updated.DiscTitle != "Refreshed Title" {
		t.Fatalf("expected title to refresh, got %q", updated.DiscTitle)
	}
}

func TestDiscMonitorSkipsCompletedDuplicate(t *testing.T) {
	cfg := testMonitorConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()
	item, err := store.NewDisc(ctx, "Done", "fp-done")
	if err != nil {
		t.Fatalf("store.NewDisc: %v", err)
	}
	item.Status = queue.StatusCompleted
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("store.Update: %v", err)
	}

	monitor := newDiscMonitor(cfg, store, zap.NewNop())
	if monitor == nil {
		t.Fatal("expected monitor to be created")
	}

	monitor.pollInterval = 10 * time.Millisecond
	monitor.scanner = &stubDiscScanner{result: &disc.ScanResult{Fingerprint: "fp-done"}}

	var detectCalls atomic.Int32
	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		if detectCalls.Add(1) == 1 {
			return &discInfo{Device: device, Label: "Done", Type: "Blu-ray"}, nil
		}
		return nil, nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := monitor.Start(runCtx); err != nil {
		t.Fatalf("monitor.Start: %v", err)
	}
	t.Cleanup(func() { monitor.Stop() })

	deadline := time.After(500 * time.Millisecond)
	for detectCalls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for detection call")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected single item, got %d", len(items))
	}
	if items[0].Status != queue.StatusCompleted {
		t.Fatalf("expected completed item to remain completed, got %s", items[0].Status)
	}
}
