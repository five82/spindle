package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"spindle/internal/config"
	"spindle/internal/disc"
	"spindle/internal/logging"
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
	return &disc.ScanResult{}, nil
}

type stubFingerprintProvider struct {
	fingerprint string
	err         error
}

func (s *stubFingerprintProvider) Compute(ctx context.Context, device, discType string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.fingerprint, nil
}

func testMonitorConfig(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	cfg := config.Default()
	cfg.TMDB.APIKey = "test-key"
	cfg.Paths.StagingDir = base + "/staging"
	cfg.Paths.LibraryDir = base + "/library"
	cfg.Paths.LogDir = base + "/logs"
	cfg.Paths.ReviewDir = base + "/review"
	cfg.Workflow.DiscMonitorTimeout = 1
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

	monitor := newDiscMonitor(cfg, store, logging.NewNop(), nil)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
		return
	}

	monitor.pollInterval = 10 * time.Millisecond

	scanner := &stubDiscScanner{result: &disc.ScanResult{}}
	monitor.scanner = scanner
	monitor.fingerprintProvider = &stubFingerprintProvider{fingerprint: "fp-demo"}

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

	monitor := newDiscMonitor(cfg, store, logging.NewNop(), nil)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
		return
	}

	monitor.pollInterval = 10 * time.Millisecond
	monitor.scanner = &stubDiscScanner{result: &disc.ScanResult{}}
	monitor.fingerprintProvider = &stubFingerprintProvider{fingerprint: "fp-demo"}

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
	item, err := store.NewDisc(ctx, "Nice Title", "fp-done")
	if err != nil {
		t.Fatalf("store.NewDisc: %v", err)
	}
	item.Status = queue.StatusCompleted
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("store.Update: %v", err)
	}

	monitor := newDiscMonitor(cfg, store, logging.NewNop(), nil)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
		return
	}

	monitor.pollInterval = 10 * time.Millisecond
	monitor.scanner = &stubDiscScanner{result: &disc.ScanResult{}}
	monitor.fingerprintProvider = &stubFingerprintProvider{fingerprint: "fp-done"}

	var detectCalls atomic.Int32
	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		if detectCalls.Add(1) == 1 {
			return &discInfo{Device: device, Label: "RAW_DISC_LABEL", Type: "Blu-ray"}, nil
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
	if items[0].DiscTitle != "Nice Title" {
		t.Fatalf("expected completed item title to remain unchanged, got %q", items[0].DiscTitle)
	}
}

func TestDiscMonitorSkipsPollWhenPaused(t *testing.T) {
	cfg := testMonitorConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	var paused atomic.Bool
	paused.Store(true)

	monitor := newDiscMonitor(cfg, store, logging.NewNop(), paused.Load)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
		return
	}

	monitor.pollInterval = 10 * time.Millisecond
	monitor.scanner = &stubDiscScanner{result: &disc.ScanResult{}}
	monitor.fingerprintProvider = &stubFingerprintProvider{fingerprint: "fp-paused"}

	var detectCalls atomic.Int32
	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		detectCalls.Add(1)
		return &discInfo{Device: device, Label: "Should Not Queue", Type: "Blu-ray"}, nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := monitor.Start(runCtx); err != nil {
		t.Fatalf("monitor.Start: %v", err)
	}
	t.Cleanup(func() { monitor.Stop() })

	// Wait for several poll cycles while paused
	time.Sleep(100 * time.Millisecond)

	if detectCalls.Load() != 0 {
		t.Fatalf("expected no detect calls while paused, got %d", detectCalls.Load())
	}

	items, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items while paused, got %d", len(items))
	}

	// Unpause and verify detection resumes
	paused.Store(false)

	deadline := time.After(500 * time.Millisecond)
	for detectCalls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for detection call after unpause")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	items = waitForItems(t, store, 1)
	if items[0].DiscTitle != "Should Not Queue" {
		t.Fatalf("unexpected disc title after unpause: %q", items[0].DiscTitle)
	}
}

func TestDiscMonitorSkipsAlreadyInWorkflow(t *testing.T) {
	cfg := testMonitorConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()

	// Create an item that is currently being processed (in workflow)
	item, err := store.NewDisc(ctx, "Processing Disc", "fp-active")
	if err != nil {
		t.Fatalf("store.NewDisc: %v", err)
	}
	item.Status = queue.StatusRipping // Actively being processed
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("store.Update: %v", err)
	}

	monitor := newDiscMonitor(cfg, store, logging.NewNop(), nil)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
		return
	}

	monitor.pollInterval = 10 * time.Millisecond

	// Scanner should NOT be called because the disc is already in workflow
	scanner := &stubDiscScanner{result: &disc.ScanResult{}}
	monitor.scanner = scanner
	monitor.fingerprintProvider = &stubFingerprintProvider{fingerprint: "fp-active"}

	var detectCalls atomic.Int32
	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		if detectCalls.Add(1) == 1 {
			return &discInfo{Device: device, Label: "Same Disc Again", Type: "Blu-ray"}, nil
		}
		return nil, nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := monitor.Start(runCtx); err != nil {
		t.Fatalf("monitor.Start: %v", err)
	}
	t.Cleanup(func() { monitor.Stop() })

	// Wait for detection and processing
	deadline := time.After(500 * time.Millisecond)
	for detectCalls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for detection call")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Give some time for any potential scan to happen
	time.Sleep(50 * time.Millisecond)

	// Scanner should NOT have been called - disc is already in workflow
	if scanner.calls.Load() != 0 {
		t.Fatalf("expected scanner to not be called for disc already in workflow, got %d calls", scanner.calls.Load())
	}

	// Only one item should exist
	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected single item, got %d", len(items))
	}
	// Item should still be in ripping status
	if items[0].Status != queue.StatusRipping {
		t.Fatalf("expected item to remain in ripping status, got %s", items[0].Status)
	}
}

func TestDiscMonitorProcessesAfterWorkflowComplete(t *testing.T) {
	cfg := testMonitorConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()

	// Create an item that was completed
	item, err := store.NewDisc(ctx, "Previously Completed", "fp-reinsert")
	if err != nil {
		t.Fatalf("store.NewDisc: %v", err)
	}
	item.Status = queue.StatusCompleted
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("store.Update: %v", err)
	}

	monitor := newDiscMonitor(cfg, store, logging.NewNop(), nil)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
		return
	}

	monitor.pollInterval = 10 * time.Millisecond
	monitor.scanner = &stubDiscScanner{result: &disc.ScanResult{}}
	monitor.fingerprintProvider = &stubFingerprintProvider{fingerprint: "fp-reinsert"}

	var detectCalls atomic.Int32
	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		if detectCalls.Add(1) == 1 {
			return &discInfo{Device: device, Label: "Same Disc Re-inserted", Type: "Blu-ray"}, nil
		}
		return nil, nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := monitor.Start(runCtx); err != nil {
		t.Fatalf("monitor.Start: %v", err)
	}
	t.Cleanup(func() { monitor.Stop() })

	// Wait for detection
	deadline := time.After(500 * time.Millisecond)
	for detectCalls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for detection call")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Give time for processing
	time.Sleep(100 * time.Millisecond)

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected single item, got %d", len(items))
	}
	// Completed item should remain completed (not be re-queued)
	if items[0].Status != queue.StatusCompleted {
		t.Fatalf("expected completed item to remain completed, got %s", items[0].Status)
	}
}
