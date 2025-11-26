package ripping_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripcache"
	"spindle/internal/ripping"
	"spindle/internal/services/makemkv"
	"spindle/internal/testsupport"
)

const ripFixtureSize = 11 * 1024 * 1024

func writeStubFile(path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	var written int64
	for written < size {
		chunk := buf
		remaining := size - written
		if remaining < int64(len(buf)) {
			chunk = buf[:remaining]
		}
		if _, err := f.Write(chunk); err != nil {
			return err
		}
		written += int64(len(chunk))
	}
	return nil
}

func stubRipperProbe(t *testing.T) {
	t.Helper()
	restore := ripping.SetProbeForTests(func(ctx context.Context, binary, path string) (ffprobe.Result, error) {
		return ffprobe.Result{
			Streams: []ffprobe.Stream{
				{CodecType: "video"},
				{CodecType: "audio"},
			},
			Format: ffprobe.Format{
				Duration: "600",
				Size:     "12582912",
				BitRate:  "5000000",
			},
		}, nil
	})
	t.Cleanup(restore)
}

func TestRipperCreatesRippedFile(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)

	stubRipperProbe(t)

	item, err := store.NewDisc(context.Background(), "Demo", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusIdentified
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	stubClient := &stubRipperClient{}
	stubNotifier := &stubNotifier{}
	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), stubClient, stubNotifier)
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
	if len(stubNotifier.starts) == 0 {
		t.Fatal("expected rip start notification")
	}
	if len(stubNotifier.completions) == 0 {
		t.Fatal("expected rip completion notification")
	}
}

func TestRipperSelectsPrimaryTitleFromRipSpec(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)

	stubRipperProbe(t)

	item, err := store.NewDisc(context.Background(), "Main Feature", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusIdentified
	spec := map[string]any{
		"titles": []map[string]any{
			{"id": 0, "name": "Main Feature", "duration": 7200},
			{"id": 1, "name": "Bonus", "duration": 900},
		},
		"metadata": map[string]any{"media_type": "movie"},
	}
	encodedSpec, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	item.RipSpecData = string(encodedSpec)
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	stubClient := &stubRipperClient{}
	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), stubClient, &stubNotifier{})
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
	if len(stubClient.lastTitleIDs) != 1 || stubClient.lastTitleIDs[0] != 0 {
		t.Fatalf("expected makemkv to target main title, got %v", stubClient.lastTitleIDs)
	}
}

func TestRipperReusesCachedRip(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries(), testsupport.WithRipCache())
	store := testsupport.MustOpenStore(t, cfg)

	stubRipperProbe(t)

	item, err := store.NewDisc(context.Background(), "Cached Feature", "fp-cache")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusIdentified
	spec := map[string]any{
		"titles":   []map[string]any{{"id": 0, "name": "Main", "duration": 7200}},
		"metadata": map[string]any{"media_type": "movie"},
	}
	encodedSpec, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	item.RipSpecData = string(encodedSpec)
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	client := &countingRipperClient{failOnSecond: true}
	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), client, &stubNotifier{})

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

	if client.calls != 1 {
		t.Fatalf("expected one rip attempt, got %d", client.calls)
	}
	if strings.TrimSpace(item.RippedFile) == "" {
		t.Fatal("missing ripped file after first run")
	}
	item.Status = queue.StatusRipped
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("persist first run: %v", err)
	}
	first, err := store.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	// Second run should reuse cached rip and not call MakeMKV again.
	if err := handler.Prepare(context.Background(), first); err != nil {
		t.Fatalf("Prepare second: %v", err)
	}
	if err := handler.Execute(context.Background(), first); err != nil {
		t.Fatalf("Execute second: %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("expected cached rip to skip makemkv; calls=%d", client.calls)
	}
	second, err := store.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetByID second: %v", err)
	}
	if second.RippedFile != first.RippedFile {
		t.Fatalf("ripped file path changed on cache reuse: %q vs %q", second.RippedFile, first.RippedFile)
	}
}

func TestRipperFailsWithoutFingerprint(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries(), testsupport.WithRipCache())
	store := testsupport.MustOpenStore(t, cfg)

	stubRipperProbe(t)

	item, err := store.NewDisc(context.Background(), "Missing Fingerprint", "fp-missing")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusIdentified
	spec := map[string]any{
		"titles":   []map[string]any{{"id": 0, "name": "Main", "duration": 7200}},
		"metadata": map[string]any{"media_type": "movie"},
	}
	encodedSpec, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	item.RipSpecData = string(encodedSpec)
	item.DiscFingerprint = ""
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	client := &countingRipperClient{}
	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), client, &stubNotifier{})
	item.Status = queue.StatusRipping
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update processing: %v", err)
	}
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	err = handler.Execute(context.Background(), item)
	if err == nil {
		t.Fatal("expected Execute to fail when fingerprint missing")
	}
	if client.calls != 0 {
		t.Fatalf("makemkv should not run without fingerprint; calls=%d", client.calls)
	}
}

func TestRipperIgnoresInvalidCachedRip(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries(), testsupport.WithRipCache())
	store := testsupport.MustOpenStore(t, cfg)

	stubRipperProbe(t)

	item, err := store.NewDisc(context.Background(), "Bad Cache", "fp-badcache")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusIdentified
	spec := map[string]any{
		"titles":   []map[string]any{{"id": 0, "name": "Main", "duration": 7200}},
		"metadata": map[string]any{"media_type": "movie"},
	}
	encodedSpec, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	item.RipSpecData = string(encodedSpec)
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Seed cache with an obviously invalid rip (too small).
	manager := ripcache.NewManager(cfg, logging.NewNop())
	cacheDir := manager.Path(item)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	invalidPath := filepath.Join(cacheDir, "title_t00.mkv")
	if err := writeStubFile(invalidPath, 1024); err != nil {
		t.Fatalf("seed invalid cache: %v", err)
	}

	client := &countingRipperClient{}
	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), client, &stubNotifier{})
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
	if client.calls != 1 {
		t.Fatalf("expected makemkv to run after invalid cache, got %d calls", client.calls)
	}
	if info, err := os.Stat(invalidPath); err == nil && info.Size() == 1024 {
		t.Fatalf("invalid cache file still present; expected overwrite")
	}
}

func TestRipperFallsBackWithoutClient(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)

	stubRipperProbe(t)

	item, err := store.NewDisc(context.Background(), "Fallback", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusIdentified
	sourcePath := filepath.Join(cfg.StagingDir, "incoming", "fallback-source.mkv")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	testsupport.WriteFile(t, sourcePath, ripFixtureSize)
	item.SourcePath = sourcePath
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), nil, &stubNotifier{})
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
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)

	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), &stubRipperClient{}, &stubNotifier{})
	health := handler.HealthCheck(context.Background())
	if !health.Ready {
		t.Fatalf("expected ready health, got %+v", health)
	}
}

func TestRipperHealthMissingClient(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)

	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), nil, &stubNotifier{})
	health := handler.HealthCheck(context.Background())
	if health.Ready {
		t.Fatalf("expected not ready health, got %+v", health)
	}
	if !strings.Contains(strings.ToLower(health.Detail), "client") {
		t.Fatalf("expected detail to mention client, got %q", health.Detail)
	}
}

type stubRipperClient struct {
	lastTitleIDs []int
}

func (s *stubRipperClient) Rip(ctx context.Context, discTitle, sourcePath, destDir string, titleIDs []int, progress func(makemkv.ProgressUpdate)) (string, error) {
	s.lastTitleIDs = append([]int(nil), titleIDs...)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	if progress != nil {
		progress(makemkv.ProgressUpdate{Stage: "Ripping", Percent: 25, Message: "starting"})
	}
	path := filepath.Join(destDir, sanitizeTestFileName(discTitle)+".mkv")
	if err := writeStubFile(path, ripFixtureSize); err != nil {
		return "", err
	}
	if progress != nil {
		progress(makemkv.ProgressUpdate{Stage: "Ripping", Percent: 90, Message: "almost"})
	}
	return path, nil
}

type countingRipperClient struct {
	calls        int
	failOnSecond bool
}

func (s *countingRipperClient) Rip(ctx context.Context, discTitle, sourcePath, destDir string, titleIDs []int, progress func(makemkv.ProgressUpdate)) (string, error) {
	s.calls++
	if s.failOnSecond && s.calls > 1 {
		return "", errors.New("unexpected second rip call")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	if progress != nil {
		progress(makemkv.ProgressUpdate{Stage: "Ripping", Percent: 10, Message: "cached"})
	}
	path := filepath.Join(destDir, sanitizeTestFileName(discTitle)+".mkv")
	if err := writeStubFile(path, ripFixtureSize); err != nil {
		return "", err
	}
	if progress != nil {
		progress(makemkv.ProgressUpdate{Stage: "Ripping", Percent: 95, Message: "cached"})
	}
	return path, nil
}

func sanitizeTestFileName(name string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-", "?", "", "\"", "", "<", "", ">", "", "|", "")
	return strings.TrimSpace(replacer.Replace(name))
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

func (f failingRipper) Rip(ctx context.Context, discTitle, sourcePath, destDir string, titleIDs []int, progress func(makemkv.ProgressUpdate)) (string, error) {
	return "", errors.New("rip failed")
}

func TestRipperWrapsErrors(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)

	item, err := store.NewDisc(context.Background(), "Fail", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}

	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), failingRipper{}, &stubNotifier{})
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err == nil {
		t.Fatal("expected execute error")
	}
}
