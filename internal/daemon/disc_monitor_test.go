package daemon

import (
	"context"
	"testing"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/queue"
)

type stubFingerprintProvider struct {
	fingerprint string
	err         error
	onCompute   func() // optional callback to track if Compute was called
}

func (s *stubFingerprintProvider) Compute(ctx context.Context, device, discType string) (string, error) {
	if s.onCompute != nil {
		s.onCompute()
	}
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
	return &cfg
}

func TestDiscMonitorQueuesNewDisc(t *testing.T) {
	cfg := testMonitorConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	monitor := newDiscMonitor(cfg, store, logging.NewNop(), nil, nil)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
		return
	}

	monitor.fingerprintProvider = &stubFingerprintProvider{fingerprint: "fp-demo"}

	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		return &discInfo{Device: device, Label: "Demo Disc", Type: "Blu-ray"}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("monitor.Start: %v", err)
	}
	t.Cleanup(func() { monitor.Stop() })

	result, err := monitor.HandleDetectionForDevice(ctx, "/dev/sr0")
	if err != nil {
		t.Fatalf("HandleDetectionForDevice: %v", err)
	}
	if !result.Handled {
		t.Fatalf("expected disc to be handled, got message: %s", result.Message)
	}

	items, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].DiscTitle != "Demo Disc" {
		t.Fatalf("unexpected disc title %q", items[0].DiscTitle)
	}
	if items[0].DiscFingerprint != "fp-demo" {
		t.Fatalf("unexpected fingerprint %q", items[0].DiscFingerprint)
	}
	if items[0].Status != queue.StatusPending {
		t.Fatalf("expected status pending, got %s", items[0].Status)
	}
	if result.ItemID != items[0].ID {
		t.Fatalf("expected item ID %d, got %d", items[0].ID, result.ItemID)
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

	monitor := newDiscMonitor(cfg, store, logging.NewNop(), nil, nil)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
		return
	}

	monitor.fingerprintProvider = &stubFingerprintProvider{fingerprint: "fp-demo"}

	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		return &discInfo{Device: device, Label: "Refreshed Title", Type: "Blu-ray"}, nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := monitor.Start(runCtx); err != nil {
		t.Fatalf("monitor.Start: %v", err)
	}
	t.Cleanup(func() { monitor.Stop() })

	result, err := monitor.HandleDetectionForDevice(runCtx, "/dev/sr0")
	if err != nil {
		t.Fatalf("HandleDetectionForDevice: %v", err)
	}
	if !result.Handled {
		t.Fatalf("expected disc to be handled, got message: %s", result.Message)
	}

	updated, err := store.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("store.GetByID: %v", err)
	}
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

	monitor := newDiscMonitor(cfg, store, logging.NewNop(), nil, nil)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
		return
	}

	monitor.fingerprintProvider = &stubFingerprintProvider{fingerprint: "fp-done"}

	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		return &discInfo{Device: device, Label: "RAW_DISC_LABEL", Type: "Blu-ray"}, nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := monitor.Start(runCtx); err != nil {
		t.Fatalf("monitor.Start: %v", err)
	}
	t.Cleanup(func() { monitor.Stop() })

	result, err := monitor.HandleDetectionForDevice(runCtx, "/dev/sr0")
	if err != nil {
		t.Fatalf("HandleDetectionForDevice: %v", err)
	}
	if !result.Handled {
		t.Fatalf("expected disc to be handled (completed items still count), got message: %s", result.Message)
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
	item.Status = queue.StatusRipping // Actively being processed (disc-dependent stage)
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("store.Update: %v", err)
	}

	monitor := newDiscMonitor(cfg, store, logging.NewNop(), nil, nil)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
		return
	}

	// Track if disc access functions are called - they should NOT be called
	// when a disc-dependent stage is in progress
	detectCalled := false
	fingerprintCalled := false

	monitor.fingerprintProvider = &stubFingerprintProvider{
		fingerprint: "fp-active",
		onCompute: func() {
			fingerprintCalled = true
		},
	}

	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		detectCalled = true
		return &discInfo{Device: device, Label: "Same Disc Again", Type: "Blu-ray"}, nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := monitor.Start(runCtx); err != nil {
		t.Fatalf("monitor.Start: %v", err)
	}
	t.Cleanup(func() { monitor.Stop() })

	result, err := monitor.HandleDetectionForDevice(runCtx, "/dev/sr0")
	if err != nil {
		t.Fatalf("HandleDetectionForDevice: %v", err)
	}

	// Detection should be skipped (not handled) when disc-dependent stage is in progress
	if result.Handled {
		t.Fatalf("expected disc detection to be skipped when ripping in progress")
	}
	if result.Message != "disc in use by active workflow" {
		t.Fatalf("unexpected message: %s", result.Message)
	}

	// Verify no disc access occurred
	if detectCalled {
		t.Fatal("detect should not be called when disc-dependent stage in progress")
	}
	if fingerprintCalled {
		t.Fatal("fingerprint should not be computed when disc-dependent stage in progress")
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

	monitor := newDiscMonitor(cfg, store, logging.NewNop(), nil, nil)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
		return
	}

	monitor.fingerprintProvider = &stubFingerprintProvider{fingerprint: "fp-reinsert"}

	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		return &discInfo{Device: device, Label: "Same Disc Re-inserted", Type: "Blu-ray"}, nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := monitor.Start(runCtx); err != nil {
		t.Fatalf("monitor.Start: %v", err)
	}
	t.Cleanup(func() { monitor.Stop() })

	result, err := monitor.HandleDetectionForDevice(runCtx, "/dev/sr0")
	if err != nil {
		t.Fatalf("HandleDetectionForDevice: %v", err)
	}
	if !result.Handled {
		t.Fatalf("expected disc to be handled, got message: %s", result.Message)
	}

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

func TestDiscMonitorNoDiscDetected(t *testing.T) {
	cfg := testMonitorConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	monitor := newDiscMonitor(cfg, store, logging.NewNop(), nil, nil)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
		return
	}

	monitor.fingerprintProvider = &stubFingerprintProvider{fingerprint: "fp-demo"}

	// Simulate no disc in drive
	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		return nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("monitor.Start: %v", err)
	}
	t.Cleanup(func() { monitor.Stop() })

	result, err := monitor.HandleDetectionForDevice(ctx, "/dev/sr0")
	if err != nil {
		t.Fatalf("HandleDetectionForDevice: %v", err)
	}
	if result.Handled {
		t.Fatal("expected disc to not be handled when no disc detected")
	}
	if result.Message != "no disc detected in drive" {
		t.Fatalf("unexpected message: %s", result.Message)
	}
}

func TestDiscMonitorConcurrentDetection(t *testing.T) {
	cfg := testMonitorConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	monitor := newDiscMonitor(cfg, store, logging.NewNop(), nil, nil)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
		return
	}

	// Create a slow detect function that blocks
	detectStarted := make(chan struct{})
	detectComplete := make(chan struct{})

	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		close(detectStarted)
		<-detectComplete
		return &discInfo{Device: device, Label: "Demo Disc", Type: "Blu-ray"}, nil
	}
	monitor.fingerprintProvider = &stubFingerprintProvider{fingerprint: "fp-concurrent"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("monitor.Start: %v", err)
	}
	t.Cleanup(func() { monitor.Stop() })

	// Start first detection in background
	resultCh := make(chan *DiscDetectedResult, 1)
	go func() {
		result, _ := monitor.HandleDetectionForDevice(ctx, "/dev/sr0")
		resultCh <- result
	}()

	// Wait for first detection to start
	<-detectStarted

	// Try second detection - should be rejected
	result2, err := monitor.HandleDetectionForDevice(ctx, "/dev/sr0")
	if err != nil {
		t.Fatalf("second HandleDetectionForDevice: %v", err)
	}
	if result2.Handled {
		t.Fatal("expected second detection to be rejected while first is processing")
	}
	if result2.Message != "already processing a disc" {
		t.Fatalf("unexpected message: %s", result2.Message)
	}

	// Complete first detection
	close(detectComplete)

	// Wait for first detection to complete
	result1 := <-resultCh
	if !result1.Handled {
		t.Fatalf("expected first disc to be handled, got message: %s", result1.Message)
	}
}

func TestDiscMonitorHandleDetectionUsesConfiguredDevice(t *testing.T) {
	cfg := testMonitorConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	monitor := newDiscMonitor(cfg, store, logging.NewNop(), nil, nil)
	if monitor == nil {
		t.Fatal("expected monitor to be created")
		return
	}

	monitor.fingerprintProvider = &stubFingerprintProvider{fingerprint: "fp-configured"}

	var detectedDevice string
	monitor.detect = func(ctx context.Context, device string) (*discInfo, error) {
		detectedDevice = device
		return &discInfo{Device: device, Label: "Demo Disc", Type: "Blu-ray"}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("monitor.Start: %v", err)
	}
	t.Cleanup(func() { monitor.Stop() })

	// HandleDetection should use the configured device
	result, err := monitor.HandleDetection(ctx)
	if err != nil {
		t.Fatalf("HandleDetection: %v", err)
	}
	if !result.Handled {
		t.Fatalf("expected disc to be handled, got message: %s", result.Message)
	}

	// Verify it used the configured device (from config default)
	if detectedDevice != monitor.device {
		t.Fatalf("expected device %q, got %q", monitor.device, detectedDevice)
	}
}
