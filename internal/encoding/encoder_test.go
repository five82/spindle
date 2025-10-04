package encoding_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spindle/internal/config"
	"spindle/internal/encoding"
	"spindle/internal/logging"
	"spindle/internal/notifications"
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
	binDir := filepath.Join(base, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range []string{"makemkvcon", "drapto"} {
		path := filepath.Join(binDir, name)
		script := []byte("#!/bin/sh\nexit 0\n")
		if err := os.WriteFile(path, script, 0o755); err != nil {
			t.Fatalf("write stub %s: %v", name, err)
		}
	}
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatalf("set PATH: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})
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
	handler := encoding.NewEncoderWithDependencies(cfg, store, logging.NewNop(), stubClient, notifier)
	item.Status = queue.StatusEncoding
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update processing: %v", err)
	}
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	item.Status = queue.StatusEncoded
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

	handler := encoding.NewEncoderWithDependencies(cfg, store, logging.NewNop(), nil, nil)
	item.Status = queue.StatusEncoding
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update processing: %v", err)
	}
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	item.Status = queue.StatusEncoded
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

	handler := encoding.NewEncoderWithDependencies(cfg, store, logging.NewNop(), failingClient{}, nil)
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

	handler := encoding.NewEncoderWithDependencies(cfg, store, logging.NewNop(), &stubDraptoClient{}, &stubNotifier{})
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

	handler := encoding.NewEncoderWithDependencies(cfg, store, logging.NewNop(), nil, &stubNotifier{})
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

func (s *stubNotifier) Publish(ctx context.Context, event notifications.Event, payload notifications.Payload) error {
	if event == notifications.EventEncodingCompleted {
		if payload != nil {
			if title, _ := payload["discTitle"].(string); title != "" {
				s.completed = append(s.completed, title)
			}
		}
	}
	return nil
}

type failingClient struct{}

func (failingClient) Encode(ctx context.Context, inputPath, outputDir string, progress func(drapto.ProgressUpdate)) (string, error) {
	return "", errors.New("encode failed")
}
