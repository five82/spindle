package ripping_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/queue"
	"spindle/internal/ripping"
	"spindle/internal/services/makemkv"
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

func TestRipperCreatesRippedFile(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})

	item, err := store.NewDisc(context.Background(), "Demo", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusIdentified
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	stubClient := &stubRipperClient{}
	stubEject := &stubEjector{}
	stubNotifier := &stubNotifier{}
	handler := ripping.NewRipperWithDependencies(cfg, store, zap.NewNop(), stubClient, stubEject, stubNotifier)
	item.Status = handler.ProcessingStatus()
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update processing: %v", err)
	}
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	item.Status = handler.NextStatus()
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Final update: %v", err)
	}

	updated, err := store.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.Status != queue.StatusRipped {
		t.Fatalf("expected status ripped, got %s", updated.Status)
	}
	if _, err := os.Stat(updated.RippedFile); err != nil {
		t.Fatalf("expected ripped file: %v", err)
	}
	if updated.ProgressMessage != "Disc content ripped" {
		t.Fatalf("unexpected progress message: %q", updated.ProgressMessage)
	}
	if !stubEject.called {
		t.Fatal("expected ejector to be called")
	}
	if len(stubNotifier.starts) == 0 {
		t.Fatal("expected rip start notification")
	}
	if len(stubNotifier.completions) == 0 {
		t.Fatal("expected rip completion notification")
	}
}

func TestRipperFallsBackWithoutClient(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})

	item, err := store.NewDisc(context.Background(), "Fallback", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusIdentified
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	handler := ripping.NewRipperWithDependencies(cfg, store, zap.NewNop(), nil, &stubEjector{}, &stubNotifier{})
	item.Status = handler.ProcessingStatus()
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update processing: %v", err)
	}
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	item.Status = handler.NextStatus()
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Final update: %v", err)
	}

	updated, err := store.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.RippedFile == "" {
		t.Fatal("expected ripped file path")
	}
	if _, err := os.Stat(updated.RippedFile); err != nil {
		t.Fatalf("expected placeholder file: %v", err)
	}
}

func TestRipperHealthReady(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	handler := ripping.NewRipperWithDependencies(cfg, store, zap.NewNop(), &stubRipperClient{}, &stubEjector{}, &stubNotifier{})
	health := handler.HealthCheck(context.Background())
	if !health.Ready {
		t.Fatalf("expected ready health, got %+v", health)
	}
}

func TestRipperHealthMissingClient(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	handler := ripping.NewRipperWithDependencies(cfg, store, zap.NewNop(), nil, &stubEjector{}, &stubNotifier{})
	health := handler.HealthCheck(context.Background())
	if health.Ready {
		t.Fatalf("expected not ready health, got %+v", health)
	}
	if !strings.Contains(strings.ToLower(health.Detail), "client") {
		t.Fatalf("expected detail to mention client, got %q", health.Detail)
	}
}

type stubRipperClient struct{}

func (s *stubRipperClient) Rip(ctx context.Context, discTitle, sourcePath, destDir string, progress func(makemkv.ProgressUpdate)) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	if progress != nil {
		progress(makemkv.ProgressUpdate{Stage: "Ripping", Percent: 25, Message: "starting"})
	}
	path := filepath.Join(destDir, sanitizeTestFileName(discTitle)+".mkv")
	if err := os.WriteFile(path, []byte("stub"), 0o644); err != nil {
		return "", err
	}
	if progress != nil {
		progress(makemkv.ProgressUpdate{Stage: "Ripping", Percent: 90, Message: "almost"})
	}
	return path, nil
}

func sanitizeTestFileName(name string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-", "?", "", "\"", "", "<", "", ">", "", "|", "")
	return strings.TrimSpace(replacer.Replace(name))
}

type stubEjector struct {
	called bool
}

func (s *stubEjector) Eject(ctx context.Context, device string) error {
	s.called = true
	return nil
}

type stubNotifier struct {
	starts      []string
	completions []string
}

func (s *stubNotifier) NotifyDiscDetected(ctx context.Context, discTitle, discType string) error {
	return nil
}

func (s *stubNotifier) NotifyIdentificationComplete(ctx context.Context, title, mediaType string) error {
	return nil
}

func (s *stubNotifier) NotifyRipStarted(ctx context.Context, discTitle string) error {
	s.starts = append(s.starts, discTitle)
	return nil
}

func (s *stubNotifier) NotifyRipCompleted(ctx context.Context, discTitle string) error {
	s.completions = append(s.completions, discTitle)
	return nil
}

func (s *stubNotifier) NotifyEncodingCompleted(ctx context.Context, discTitle string) error {
	return nil
}

func (s *stubNotifier) NotifyProcessingCompleted(ctx context.Context, title string) error {
	return nil
}

func (s *stubNotifier) NotifyOrganizationCompleted(ctx context.Context, mediaTitle, finalFile string) error {
	return nil
}

func (s *stubNotifier) NotifyQueueStarted(ctx context.Context, count int) error {
	return nil
}

func (s *stubNotifier) NotifyQueueCompleted(ctx context.Context, processed, failed int, duration time.Duration) error {
	return nil
}

func (s *stubNotifier) NotifyError(ctx context.Context, err error, contextLabel string) error {
	return nil
}

func (s *stubNotifier) NotifyUnidentifiedMedia(ctx context.Context, filename string) error {
	return nil
}

func (s *stubNotifier) TestNotification(ctx context.Context) error {
	return nil
}

type failingRipper struct{}

func (f failingRipper) Rip(ctx context.Context, discTitle, sourcePath, destDir string, progress func(makemkv.ProgressUpdate)) (string, error) {
	return "", errors.New("rip failed")
}

func TestRipperWrapsErrors(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	item, err := store.NewDisc(context.Background(), "Fail", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}

	handler := ripping.NewRipperWithDependencies(cfg, store, zap.NewNop(), failingRipper{}, &stubEjector{}, &stubNotifier{})
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err == nil {
		t.Fatal("expected execute error")
	}
}
