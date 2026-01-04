package encoding_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spindle/internal/encoding"
	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services/drapto"
	"spindle/internal/testsupport"
)

const encodedFixtureSize = 6 * 1024 * 1024

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

func TestEncoderUsesDraptoClient(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)

	stubEncoderProbe(t)

	item, err := store.NewDisc(context.Background(), "Demo", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusRipped
	item.DiscFingerprint = "ENCODERTESTFP1"
	stagingRoot := item.StagingRoot(cfg.Paths.StagingDir)
	ripsDir := filepath.Join(stagingRoot, "rips")
	item.RippedFile = filepath.Join(ripsDir, "demo.mkv")
	testsupport.WriteFile(t, item.RippedFile, encodedFixtureSize)
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
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)

	stubEncoderProbe(t)

	item, err := store.NewDisc(context.Background(), "Fallback", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusRipped
	item.DiscFingerprint = "ENCODERTESTFP2"
	stagingRoot := item.StagingRoot(cfg.Paths.StagingDir)
	ripsDir := filepath.Join(stagingRoot, "rips")
	if err := os.MkdirAll(ripsDir, 0o755); err != nil {
		t.Fatalf("mkdir rips: %v", err)
	}
	item.RippedFile = filepath.Join(ripsDir, "demo.mkv")
	testsupport.WriteFile(t, item.RippedFile, encodedFixtureSize)
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
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)

	stubEncoderProbe(t)

	item, err := store.NewDisc(context.Background(), "Fail", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusRipped
	item.DiscFingerprint = "ENCODERTESTFP3"
	stagingRoot := item.StagingRoot(cfg.Paths.StagingDir)
	ripsDir := filepath.Join(stagingRoot, "rips")
	if err := os.MkdirAll(ripsDir, 0o755); err != nil {
		t.Fatalf("mkdir rips: %v", err)
	}
	item.RippedFile = filepath.Join(ripsDir, "demo.mkv")
	testsupport.WriteFile(t, item.RippedFile, encodedFixtureSize)

	handler := encoding.NewEncoderWithDependencies(cfg, store, logging.NewNop(), failingClient{}, nil)
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err == nil {
		t.Fatal("expected execute error")
	}
}

func TestEncoderRemovesStaleEncodedArtifacts(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)

	stubEncoderProbe(t)

	item, err := store.NewDisc(context.Background(), "Cleanup", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusRipped
	item.DiscFingerprint = "ENCODERTESTFP6"
	stagingRoot := item.StagingRoot(cfg.Paths.StagingDir)
	ripsDir := filepath.Join(stagingRoot, "rips")
	if err := os.MkdirAll(ripsDir, 0o755); err != nil {
		t.Fatalf("mkdir rips: %v", err)
	}
	item.RippedFile = filepath.Join(ripsDir, "demo.mkv")
	testsupport.WriteFile(t, item.RippedFile, encodedFixtureSize)

	encodedDir := filepath.Join(stagingRoot, "encoded")
	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		t.Fatalf("mkdir encoded: %v", err)
	}
	staleFile := filepath.Join(encodedDir, "stale.mkv")
	testsupport.WriteFile(t, staleFile, encodedFixtureSize)

	stubClient := &stubDraptoClient{}
	handler := encoding.NewEncoderWithDependencies(cfg, store, logging.NewNop(), stubClient, nil)
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Stat(staleFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale file to be removed, err=%v", err)
	}
	entries, err := os.ReadDir(encodedDir)
	if err != nil {
		t.Fatalf("ReadDir encoded: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected new encoded artifact in %s", encodedDir)
	}
}

func TestEncoderPersistsRipSpecPerEpisode(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)

	stubEncoderProbe(t)

	item, err := store.NewDisc(context.Background(), "TV Disc", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusRipped
	item.DiscFingerprint = "ENCODERTESTFP_TV"

	stagingRoot := item.StagingRoot(cfg.Paths.StagingDir)
	ripsDir := filepath.Join(stagingRoot, "rips")
	if err := os.MkdirAll(ripsDir, 0o755); err != nil {
		t.Fatalf("mkdir rips: %v", err)
	}

	season := 5
	ep1Key := ripspec.EpisodeKey(season, 1)
	ep2Key := ripspec.EpisodeKey(season, 2)
	ep1Rip := filepath.Join(ripsDir, "title_001.mkv")
	ep2Rip := filepath.Join(ripsDir, "title_002.mkv")
	testsupport.WriteFile(t, ep1Rip, encodedFixtureSize)
	testsupport.WriteFile(t, ep2Rip, encodedFixtureSize)

	item.RippedFile = ep1Rip

	env := ripspec.Envelope{
		Titles: []ripspec.Title{
			{ID: 1, Name: "Episode 1", Duration: 1800},
			{ID: 2, Name: "Episode 2", Duration: 1810},
		},
		Episodes: []ripspec.Episode{
			{Key: ep1Key, TitleID: 1, Season: season, Episode: 1, EpisodeTitle: "Episode One", OutputBasename: "Show - S05E01"},
			{Key: ep2Key, TitleID: 2, Season: season, Episode: 2, EpisodeTitle: "Episode Two", OutputBasename: "Show - S05E02"},
		},
		Assets: ripspec.Assets{
			Ripped: []ripspec.Asset{
				{EpisodeKey: ep1Key, TitleID: 1, Path: ep1Rip},
				{EpisodeKey: ep2Key, TitleID: 2, Path: ep2Rip},
			},
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

	stubClient := newBlockingDraptoClient()
	handler := encoding.NewEncoderWithDependencies(cfg, store, logging.NewNop(), stubClient, nil)
	item.Status = queue.StatusEncoding
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
		t.Fatal("timeout waiting for second encode invocation")
	}

	mid, err := store.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetByID (mid): %v", err)
	}
	midEnv, err := ripspec.Parse(mid.RipSpecData)
	if err != nil {
		t.Fatalf("parse rip spec (mid): %v", err)
	}
	if len(midEnv.Assets.Encoded) != 1 {
		t.Fatalf("expected 1 encoded asset mid-run, got %d", len(midEnv.Assets.Encoded))
	}
	if _, ok := midEnv.Assets.FindAsset("encoded", ep1Key); !ok {
		t.Fatalf("expected encoded asset for %s mid-run", ep1Key)
	}
	if _, ok := midEnv.Assets.FindAsset("encoded", ep2Key); ok {
		t.Fatalf("did not expect encoded asset for %s mid-run", ep2Key)
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
	if len(finalEnv.Assets.Encoded) != 2 {
		t.Fatalf("expected 2 encoded assets final, got %d", len(finalEnv.Assets.Encoded))
	}
	if _, ok := finalEnv.Assets.FindAsset("encoded", ep2Key); !ok {
		t.Fatalf("expected encoded asset for %s final", ep2Key)
	}
}

func TestEncoderHealthReady(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)

	handler := encoding.NewEncoderWithDependencies(cfg, store, logging.NewNop(), &stubDraptoClient{}, &stubNotifier{})
	health := handler.HealthCheck(context.Background())
	if !health.Ready {
		t.Fatalf("expected ready health, got %+v", health)
	}
}

func TestEncoderHealthMissingClient(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	store := testsupport.MustOpenStore(t, cfg)

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
	called        bool
	presetProfile string
}

func (s *stubDraptoClient) Encode(ctx context.Context, inputPath, outputDir string, opts drapto.EncodeOptions) (string, error) {
	s.called = true
	s.presetProfile = opts.PresetProfile
	if opts.Progress != nil {
		opts.Progress(drapto.ProgressUpdate{Stage: "Encoding", Percent: 50, Message: "Halfway"})
	}
	stem := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	if stem == "" {
		stem = filepath.Base(inputPath)
	}
	path := filepath.Join(outputDir, stem+".mkv")
	payload := bytes.Repeat([]byte{0xEC}, encodedFixtureSize)
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

func (failingClient) Encode(ctx context.Context, inputPath, outputDir string, opts drapto.EncodeOptions) (string, error) {
	return "", errors.New("encode failed")
}

type blockingDraptoClient struct {
	count         int
	startedSecond chan struct{}
	allowSecond   chan struct{}
}

func newBlockingDraptoClient() *blockingDraptoClient {
	return &blockingDraptoClient{
		startedSecond: make(chan struct{}),
		allowSecond:   make(chan struct{}),
	}
}

func (s *blockingDraptoClient) Encode(ctx context.Context, inputPath, outputDir string, opts drapto.EncodeOptions) (string, error) {
	s.count++
	if s.count == 2 {
		close(s.startedSecond)
		select {
		case <-s.allowSecond:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if opts.Progress != nil {
		opts.Progress(drapto.ProgressUpdate{Stage: "Encoding", Percent: 50, Message: "Halfway"})
	}
	stem := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	if stem == "" {
		stem = filepath.Base(inputPath)
	}
	path := filepath.Join(outputDir, stem+".mkv")
	payload := bytes.Repeat([]byte{0xEC}, encodedFixtureSize)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
