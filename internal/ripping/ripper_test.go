package ripping_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/notifications"
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
	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), stubClient, stubEject, stubNotifier)
	item.Status = queue.StatusRipping
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update processing: %v", err)
	}
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	item.Status = queue.StatusRipped
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

	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), nil, &stubEjector{}, &stubNotifier{})
	item.Status = queue.StatusRipping
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update processing: %v", err)
	}
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	item.Status = queue.StatusRipped
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

	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), &stubRipperClient{}, &stubEjector{}, &stubNotifier{})
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

	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), nil, &stubEjector{}, &stubNotifier{})
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

func (s *stubNotifier) Publish(ctx context.Context, event notifications.Event, payload notifications.Payload) error {
	switch event {
	case notifications.EventRipStarted:
		if payload != nil {
			if title, _ := payload["discTitle"].(string); title != "" {
				s.starts = append(s.starts, title)
			}
		}
	case notifications.EventRipCompleted:
		if payload != nil {
			if title, _ := payload["discTitle"].(string); title != "" {
				s.completions = append(s.completions, title)
			}
		}
	}
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

	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), failingRipper{}, &stubEjector{}, &stubNotifier{})
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err == nil {
		t.Fatal("expected execute error")
	}
}
