package encoding_test

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
	"spindle/internal/encoding"
	"spindle/internal/queue"
	"spindle/internal/services/drapto"
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

func TestEncoderUsesDraptoClient(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	item, err := store.NewDisc(context.Background(), "Demo", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusRipped
	item.RippedFile = filepath.Join(cfg.StagingDir, "demo.mkv")
	if err := os.MkdirAll(cfg.StagingDir, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	if err := os.WriteFile(item.RippedFile, []byte("data"), 0o644); err != nil {
		t.Fatalf("write ripped file: %v", err)
	}
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	stubClient := &stubDraptoClient{}
	notifier := &stubNotifier{}
	handler := encoding.NewEncoderWithDependencies(cfg, store, zap.NewNop(), stubClient, notifier)
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
	if updated.EncodedFile == "" {
		t.Fatal("expected encoded file path")
	}
	if !strings.Contains(updated.ProgressMessage, "Encoding completed") {
		t.Fatalf("unexpected progress message: %q", updated.ProgressMessage)
	}
	if !stubClient.called {
		t.Fatal("expected client encode to be called")
	}
	if len(notifier.completed) == 0 {
		t.Fatal("expected encoding completion notification")
	}
}

func TestEncoderFallsBackWithoutClient(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	item, err := store.NewDisc(context.Background(), "Fallback", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusRipped
	item.RippedFile = filepath.Join(cfg.StagingDir, "demo.mkv")
	if err := os.MkdirAll(cfg.StagingDir, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	if err := os.WriteFile(item.RippedFile, []byte("data"), 0o644); err != nil {
		t.Fatalf("write ripped file: %v", err)
	}
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	handler := encoding.NewEncoderWithDependencies(cfg, store, zap.NewNop(), nil, nil)
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
	if updated.EncodedFile == "" {
		t.Fatal("expected encoded file path")
	}
}

func TestEncoderWrapsErrors(t *testing.T) {
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
	item.Status = queue.StatusRipped
	item.RippedFile = filepath.Join(cfg.StagingDir, "demo.mkv")
	if err := os.MkdirAll(cfg.StagingDir, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	if err := os.WriteFile(item.RippedFile, []byte("data"), 0o644); err != nil {
		t.Fatalf("write ripped file: %v", err)
	}

	handler := encoding.NewEncoderWithDependencies(cfg, store, zap.NewNop(), failingClient{}, nil)
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err == nil {
		t.Fatal("expected execute error")
	}
}

func TestEncoderHealthReady(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	handler := encoding.NewEncoderWithDependencies(cfg, store, zap.NewNop(), &stubDraptoClient{}, &stubNotifier{})
	health := handler.HealthCheck(context.Background())
	if !health.Ready {
		t.Fatalf("expected ready health, got %+v", health)
	}
}

func TestEncoderHealthMissingClient(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	handler := encoding.NewEncoderWithDependencies(cfg, store, zap.NewNop(), nil, &stubNotifier{})
	health := handler.HealthCheck(context.Background())
	if health.Ready {
		t.Fatalf("expected not ready health, got %+v", health)
	}
	if !strings.Contains(strings.ToLower(health.Detail), "client") {
		t.Fatalf("expected detail to mention client, got %q", health.Detail)
	}
}

type stubDraptoClient struct {
	called bool
}

func (s *stubDraptoClient) Encode(ctx context.Context, inputPath, outputDir string, progress func(drapto.ProgressUpdate)) (string, error) {
	s.called = true
	if progress != nil {
		progress(drapto.ProgressUpdate{Stage: "Encoding", Percent: 50, Message: "Halfway"})
	}
	return filepath.Join(outputDir, filepath.Base(inputPath)+".av1.mkv"), nil
}

type stubNotifier struct {
	completed []string
}

func (s *stubNotifier) NotifyDiscDetected(ctx context.Context, discTitle, discType string) error {
	return nil
}

func (s *stubNotifier) NotifyIdentificationComplete(ctx context.Context, title, mediaType string) error {
	return nil
}

func (s *stubNotifier) NotifyRipStarted(ctx context.Context, discTitle string) error {
	return nil
}

func (s *stubNotifier) NotifyRipCompleted(ctx context.Context, discTitle string) error {
	return nil
}

func (s *stubNotifier) NotifyEncodingCompleted(ctx context.Context, discTitle string) error {
	s.completed = append(s.completed, discTitle)
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

type failingClient struct{}

func (failingClient) Encode(ctx context.Context, inputPath, outputDir string, progress func(drapto.ProgressUpdate)) (string, error) {
	return "", errors.New("encode failed")
}
