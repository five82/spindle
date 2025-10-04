package encoding_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spindle/internal/config"
	"spindle/internal/encoding"
	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/services/drapto"
)

func stubEncoderProbe(t *testing.T) {
	t.Helper()
	restore := encoding.SetProbeForTests(func(ctx context.Context, binary, path string) (ffprobe.Result, error) {
		return ffprobe.Result{
			Streams: []ffprobe.Stream{
				{CodecType: "video"},
				{CodecType: "audio"},
			},
			Format: ffprobe.Format{
				Duration: "5400",
				Size:     "15000000",
				BitRate:  "5000000",
			},
		}, nil
	})
	t.Cleanup(restore)
}

func writeLargeFile(t *testing.T, path string, size int) {
	t.Helper()
	if size <= 0 {
		size = 1
	}
	payload := bytes.Repeat([]byte{0x42}, size)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir large file: %v", err)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write large file: %v", err)
	}
}

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

	stubEncoderProbe(t)

	item, err := store.NewDisc(context.Background(), "Demo", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusRipped
	item.DiscFingerprint = "ENCODERTESTFP1"
	stagingRoot := item.StagingRoot(cfg.StagingDir)
	ripsDir := filepath.Join(stagingRoot, "rips")
	if err := os.MkdirAll(ripsDir, 0o755); err != nil {
		t.Fatalf("mkdir rips: %v", err)
	}
	item.RippedFile = filepath.Join(ripsDir, "demo.mkv")
	writeLargeFile(t, item.RippedFile, 8*1024*1024)
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

	stubEncoderProbe(t)

	item, err := store.NewDisc(context.Background(), "Fallback", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusRipped
	item.DiscFingerprint = "ENCODERTESTFP2"
	stagingRoot := item.StagingRoot(cfg.StagingDir)
	ripsDir := filepath.Join(stagingRoot, "rips")
	if err := os.MkdirAll(ripsDir, 0o755); err != nil {
		t.Fatalf("mkdir rips: %v", err)
	}
	item.RippedFile = filepath.Join(ripsDir, "demo.mkv")
	writeLargeFile(t, item.RippedFile, 8*1024*1024)
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

	stubEncoderProbe(t)

	item, err := store.NewDisc(context.Background(), "Fail", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusRipped
	item.DiscFingerprint = "ENCODERTESTFP3"
	stagingRoot := item.StagingRoot(cfg.StagingDir)
	ripsDir := filepath.Join(stagingRoot, "rips")
	if err := os.MkdirAll(ripsDir, 0o755); err != nil {
		t.Fatalf("mkdir rips: %v", err)
	}
	item.RippedFile = filepath.Join(ripsDir, "demo.mkv")
	writeLargeFile(t, item.RippedFile, 8*1024*1024)

	handler := encoding.NewEncoderWithDependencies(cfg, store, logging.NewNop(), failingClient{}, nil)
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err == nil {
		t.Fatal("expected execute error")
	}
}

func TestEncoderFailsWhenEncodedArtifactMissing(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	stubEncoderProbe(t)

	item, err := store.NewDisc(context.Background(), "Missing", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusRipped
	item.DiscFingerprint = "ENCODERTESTFP4"
	stagingRoot := item.StagingRoot(cfg.StagingDir)
	ripsDir := filepath.Join(stagingRoot, "rips")
	if err := os.MkdirAll(ripsDir, 0o755); err != nil {
		t.Fatalf("mkdir rips: %v", err)
	}
	item.RippedFile = filepath.Join(ripsDir, "demo.mkv")
	writeLargeFile(t, item.RippedFile, 8*1024*1024)

	handler := encoding.NewEncoderWithDependencies(cfg, store, logging.NewNop(), missingArtifactClient{}, nil)
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err == nil {
		t.Fatal("expected execute error when encoded artifact missing")
	}
}

func TestEncoderFailsWhenEncodedArtifactEmpty(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	stubEncoderProbe(t)

	item, err := store.NewDisc(context.Background(), "Empty", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusRipped
	item.DiscFingerprint = "ENCODERTESTFP5"
	stagingRoot := item.StagingRoot(cfg.StagingDir)
	ripsDir := filepath.Join(stagingRoot, "rips")
	if err := os.MkdirAll(ripsDir, 0o755); err != nil {
		t.Fatalf("mkdir rips: %v", err)
	}
	item.RippedFile = filepath.Join(ripsDir, "demo.mkv")
	writeLargeFile(t, item.RippedFile, 8*1024*1024)

	handler := encoding.NewEncoderWithDependencies(cfg, store, logging.NewNop(), emptyArtifactClient{}, nil)
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err == nil {
		t.Fatal("expected execute error when encoded artifact empty")
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
	stem := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	if stem == "" {
		stem = filepath.Base(inputPath)
	}
	path := filepath.Join(outputDir, stem+".mkv")
	payload := bytes.Repeat([]byte{0xEC}, 10*1024*1024)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return "", err
	}
	return path, nil
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

type missingArtifactClient struct{}

func (missingArtifactClient) Encode(ctx context.Context, inputPath, outputDir string, progress func(drapto.ProgressUpdate)) (string, error) {
	stem := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	if stem == "" {
		stem = filepath.Base(inputPath)
	}
	return filepath.Join(outputDir, stem+".mkv"), nil
}

type emptyArtifactClient struct{}

func (emptyArtifactClient) Encode(ctx context.Context, inputPath, outputDir string, progress func(drapto.ProgressUpdate)) (string, error) {
	stem := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	if stem == "" {
		stem = filepath.Base(inputPath)
	}
	path := filepath.Join(outputDir, stem+".mkv")
	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	return path, file.Close()
}
