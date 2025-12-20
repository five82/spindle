package ripping_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripcache"
	"spindle/internal/ripping"
	"spindle/internal/ripspec"
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

func TestRipperPersistsRipSpecPerEpisode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)

	stubRipperProbe(t)

	item, err := store.NewDisc(context.Background(), "TV Disc", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusIdentified

	season := 1
	ep1Key := ripspec.EpisodeKey(season, 1)
	ep2Key := ripspec.EpisodeKey(season, 2)
	env := ripspec.Envelope{
		Metadata: map[string]any{"media_type": "tv"},
		Titles: []ripspec.Title{
			{ID: 1, Name: "Episode 1", Duration: 1800},
			{ID: 2, Name: "Episode 2", Duration: 1810},
		},
		Episodes: []ripspec.Episode{
			{Key: ep1Key, TitleID: 1, Season: season, Episode: 1, EpisodeTitle: "Episode One", OutputBasename: "Show - S01E01"},
			{Key: ep2Key, TitleID: 2, Season: season, Episode: 2, EpisodeTitle: "Episode Two", OutputBasename: "Show - S01E02"},
		},
	}
	encoded, err := env.Encode()
	if err != nil {
		t.Fatalf("encode rip spec: %v", err)
	}
	item.RipSpecData = encoded
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	stubClient := newBlockingEpisodeRipperClient()
	handler := ripping.NewRipperWithDependencies(cfg, store, logging.NewNop(), stubClient, &stubNotifier{})

	item.Status = queue.StatusRipping
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update processing: %v", err)
	}
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- handler.Execute(context.Background(), item)
	}()

	select {
	case <-stubClient.startedSecond:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for episode 2 rip")
	}

	mid, err := store.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetByID (mid): %v", err)
	}
	midEnv, err := ripspec.Parse(mid.RipSpecData)
	if err != nil {
		t.Fatalf("parse rip spec (mid): %v", err)
	}
	if len(midEnv.Assets.Ripped) != 1 {
		t.Fatalf("expected 1 ripped asset mid-run, got %d", len(midEnv.Assets.Ripped))
	}
	if _, ok := midEnv.Assets.FindAsset("ripped", ep1Key); !ok {
		t.Fatalf("expected ripped asset for %s mid-run", ep1Key)
	}
	if _, ok := midEnv.Assets.FindAsset("ripped", ep2Key); ok {
		t.Fatalf("did not expect ripped asset for %s mid-run", ep2Key)
	}

	close(stubClient.allowSecond)
	if err := <-errCh; err != nil {
		t.Fatalf("Execute: %v", err)
	}

	final, err := store.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetByID (final): %v", err)
	}
	finalEnv, err := ripspec.Parse(final.RipSpecData)
	if err != nil {
		t.Fatalf("parse rip spec (final): %v", err)
	}
	if len(finalEnv.Assets.Ripped) != 2 {
		t.Fatalf("expected 2 ripped assets final, got %d", len(finalEnv.Assets.Ripped))
	}
	if _, ok := finalEnv.Assets.FindAsset("ripped", ep2Key); !ok {
		t.Fatalf("expected ripped asset for %s final", ep2Key)
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
	if !strings.Contains(first.ProgressMessage, "Rip cache hit") {
		t.Fatalf("expected progress message to mention rip cache hit, got %q", first.ProgressMessage)
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
	sourcePath := filepath.Join(cfg.Paths.StagingDir, "incoming", "fallback-source.mkv")
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

type blockingEpisodeRipperClient struct {
	startedSecond chan struct{}
	allowSecond   chan struct{}
}

func newBlockingEpisodeRipperClient() *blockingEpisodeRipperClient {
	return &blockingEpisodeRipperClient{
		startedSecond: make(chan struct{}),
		allowSecond:   make(chan struct{}),
	}
}

func (s *blockingEpisodeRipperClient) Rip(ctx context.Context, discTitle, sourcePath, destDir string, titleIDs []int, progress func(makemkv.ProgressUpdate)) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	var lastPath string
	for idx, id := range titleIDs {
		if progress != nil {
			progress(makemkv.ProgressUpdate{Stage: "Ripping", Percent: 10, Message: fmt.Sprintf("episode %d starting", idx+1)})
			progress(makemkv.ProgressUpdate{Stage: "Ripping", Percent: 96, Message: fmt.Sprintf("episode %d nearly done", idx+1)})
		}
		path := filepath.Join(destDir, fmt.Sprintf("title_t%02d.mkv", id))
		if err := writeStubFile(path, ripFixtureSize); err != nil {
			return "", err
		}
		if progress != nil {
			progress(makemkv.ProgressUpdate{Stage: "Ripping", Percent: 100, Message: fmt.Sprintf("episode %d done", idx+1)})
		}
		lastPath = path
		if idx == 0 && len(titleIDs) > 1 {
			close(s.startedSecond)
			select {
			case <-s.allowSecond:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
	return lastPath, nil
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
