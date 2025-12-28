package organizer_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
	"spindle/internal/notifications"
	"spindle/internal/organizer"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services/jellyfin"
	"spindle/internal/testsupport"
)

const organizedFixtureSize = 6 * 1024 * 1024

func stubOrganizerProbe(t *testing.T) {
	t.Helper()
	restore := organizer.SetProbeForTests(func(ctx context.Context, binary, path string) (ffprobe.Result, error) {
		return ffprobe.Result{
			Streams: []ffprobe.Stream{
				{CodecType: "video"},
				{CodecType: "audio"},
			},
			Format: ffprobe.Format{
				Duration: "3600",
				Size:     "20000000",
				BitRate:  "4500000",
			},
		}, nil
	})
	t.Cleanup(restore)
}

func TestOrganizerMovesFileToLibrary(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	stubOrganizerProbe(t)

	item, err := store.NewDisc(context.Background(), "Demo", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusEncoded
	item.DiscFingerprint = "ORGANIZERTESTFP1"
	stagingRoot := item.StagingRoot(cfg.Paths.StagingDir)
	encodedDir := filepath.Join(stagingRoot, "encoded")
	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		t.Fatalf("mkdir encoded: %v", err)
	}
	item.EncodedFile = filepath.Join(encodedDir, "demo.encoded.mkv")
	testsupport.WriteFile(t, item.EncodedFile, organizedFixtureSize)
	item.MetadataJSON = `{"title":"Demo", "filename":"Demo", " library_path":"` + filepath.Join(cfg.Paths.LibraryDir, cfg.Library.MoviesDir) + `", "movie":true}`
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	stubJellyfin := &stubJellyfinService{targetDir: filepath.Join(cfg.Paths.LibraryDir, cfg.Library.MoviesDir)}
	notifier := &stubNotifier{}
	handler := organizer.NewOrganizerWithDependencies(cfg, store, logging.NewNop(), stubJellyfin, notifier)
	item.Status = queue.StatusOrganizing
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update processing: %v", err)
	}
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	item.Status = queue.StatusCompleted
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Final update: %v", err)
	}

	updated, err := store.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.FinalFile == "" {
		t.Fatal("expected final file path")
	}
	if len(notifier.completed) == 0 {
		t.Fatal("expected library updated notification")
	}
	if _, err := os.Stat(stagingRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected staging root cleanup, err=%v", err)
	}
}

func TestOrganizerPersistsRipSpecPerEpisode(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	stubOrganizerProbe(t)

	item, err := store.NewDisc(context.Background(), "TV Disc", "fp-tv")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusEncoded
	item.DiscFingerprint = "ORGANIZERTESTFP_TV"

	stagingRoot := item.StagingRoot(cfg.Paths.StagingDir)
	encodedDir := filepath.Join(stagingRoot, "encoded")
	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		t.Fatalf("mkdir encoded: %v", err)
	}

	season := 1
	ep1Key := ripspec.EpisodeKey(season, 1)
	ep2Key := ripspec.EpisodeKey(season, 2)
	ep1Encoded := filepath.Join(encodedDir, "ep1.mkv")
	ep2Encoded := filepath.Join(encodedDir, "ep2.mkv")
	testsupport.WriteFile(t, ep1Encoded, organizedFixtureSize)
	testsupport.WriteFile(t, ep2Encoded, organizedFixtureSize)
	item.EncodedFile = ep1Encoded

	env := ripspec.Envelope{
		Metadata: map[string]any{"media_type": "tv"},
		Episodes: []ripspec.Episode{
			{Key: ep1Key, TitleID: 1, Season: season, Episode: 1, EpisodeTitle: "Episode One", OutputBasename: "Show - S01E01"},
			{Key: ep2Key, TitleID: 2, Season: season, Episode: 2, EpisodeTitle: "Episode Two", OutputBasename: "Show - S01E02"},
		},
		Assets: ripspec.Assets{
			Encoded: []ripspec.Asset{
				{EpisodeKey: ep1Key, TitleID: 1, Path: ep1Encoded},
				{EpisodeKey: ep2Key, TitleID: 2, Path: ep2Encoded},
			},
		},
	}
	encodedSpec, err := env.Encode()
	if err != nil {
		t.Fatalf("encode rip spec: %v", err)
	}
	item.RipSpecData = encodedSpec

	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	targetDir := filepath.Join(cfg.Paths.LibraryDir, cfg.Library.TVDir)
	stubJellyfin := newBlockingJellyfinService(targetDir)
	handler := organizer.NewOrganizerWithDependencies(cfg, store, logging.NewNop(), stubJellyfin, nil)

	item.Status = queue.StatusOrganizing
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
	case <-stubJellyfin.startedSecond:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for second organize invocation")
	}

	mid, err := store.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetByID (mid): %v", err)
	}
	midEnv, err := ripspec.Parse(mid.RipSpecData)
	if err != nil {
		t.Fatalf("parse rip spec (mid): %v", err)
	}
	if len(midEnv.Assets.Final) != 1 {
		t.Fatalf("expected 1 final asset mid-run, got %d", len(midEnv.Assets.Final))
	}
	if _, ok := midEnv.Assets.FindAsset("final", ep1Key); !ok {
		t.Fatalf("expected final asset for %s mid-run", ep1Key)
	}
	if _, ok := midEnv.Assets.FindAsset("final", ep2Key); ok {
		t.Fatalf("did not expect final asset for %s mid-run", ep2Key)
	}

	close(stubJellyfin.allowSecond)
	if err := <-errCh; err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

func TestOrganizerMovesGeneratedSubtitles(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	stubOrganizerProbe(t)

	item, err := store.NewDisc(context.Background(), "Demo", "fp-subtitles")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusEncoded
	stagingRoot := item.StagingRoot(cfg.Paths.StagingDir)
	encodedDir := filepath.Join(stagingRoot, "encoded")
	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		t.Fatalf("mkdir encoded: %v", err)
	}
	item.EncodedFile = filepath.Join(encodedDir, "demo.encoded.mkv")
	testsupport.WriteFile(t, item.EncodedFile, organizedFixtureSize)
	subtitlePath := filepath.Join(encodedDir, "demo.encoded.en.srt")
	if err := os.WriteFile(subtitlePath, []byte("1\n00:00:00,000 --> 00:00:01,000\nHello\n"), 0o644); err != nil {
		t.Fatalf("write subtitle: %v", err)
	}
	item.MetadataJSON = `{"title":"Demo","filename":"Demo"}`
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	targetDir := filepath.Join(cfg.Paths.LibraryDir, cfg.Library.MoviesDir)
	stubJellyfin := &stubJellyfinService{targetDir: targetDir}
	handler := organizer.NewOrganizerWithDependencies(cfg, store, logging.NewNop(), stubJellyfin, nil)
	item.Status = queue.StatusOrganizing
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	expectedSubtitle := filepath.Join(targetDir, "demo.encoded.en.srt")
	if _, err := os.Stat(expectedSubtitle); err != nil {
		t.Fatalf("expected subtitle at %s: %v", expectedSubtitle, err)
	}
	if _, err := os.Stat(subtitlePath); !os.IsNotExist(err) {
		t.Fatalf("expected subtitle removed from staging, err=%v", err)
	}
}

func TestOrganizerOverwritesExistingSubtitles(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	cfg.Library.OverwriteExisting = true
	store := testsupport.MustOpenStore(t, cfg)

	stubOrganizerProbe(t)

	item, err := store.NewDisc(context.Background(), "Demo", "fp-subtitles-overwrite")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusEncoded
	stagingRoot := item.StagingRoot(cfg.Paths.StagingDir)
	encodedDir := filepath.Join(stagingRoot, "encoded")
	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		t.Fatalf("mkdir encoded: %v", err)
	}
	item.EncodedFile = filepath.Join(encodedDir, "demo.encoded.mkv")
	testsupport.WriteFile(t, item.EncodedFile, organizedFixtureSize)
	subtitlePath := filepath.Join(encodedDir, "demo.encoded.en.srt")
	if err := os.WriteFile(subtitlePath, []byte("new subtitle"), 0o644); err != nil {
		t.Fatalf("write subtitle: %v", err)
	}
	item.MetadataJSON = `{"title":"Demo","filename":"Demo"}`
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	targetDir := filepath.Join(cfg.Paths.LibraryDir, cfg.Library.MoviesDir)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir library: %v", err)
	}
	existingSubtitle := filepath.Join(targetDir, "demo.encoded.en.srt")
	if err := os.WriteFile(existingSubtitle, []byte("old subtitle"), 0o644); err != nil {
		t.Fatalf("seed existing subtitle: %v", err)
	}

	stubJellyfin := &stubJellyfinService{targetDir: targetDir}
	handler := organizer.NewOrganizerWithDependencies(cfg, store, logging.NewNop(), stubJellyfin, nil)
	item.Status = queue.StatusOrganizing
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, err := os.ReadFile(existingSubtitle)
	if err != nil {
		t.Fatalf("read overwritten subtitle: %v", err)
	}
	if string(data) != "new subtitle" {
		t.Fatalf("expected subtitle to be overwritten, got %q", string(data))
	}
}

func TestOrganizerRoutesUnidentifiedToReview(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	stubOrganizerProbe(t)

	notifier := &stubNotifier{}
	handler := organizer.NewOrganizerWithDependencies(cfg, store, logging.NewNop(), &stubJellyfinService{}, notifier)

	seenTargets := map[string]struct{}{}
	for i := 0; i < 2; i++ {
		item, err := store.NewDisc(context.Background(), "Unknown", "fp-review")
		if err != nil {
			t.Fatalf("NewDisc: %v", err)
		}
		item.Status = queue.StatusEncoded
		item.DiscFingerprint = fmt.Sprintf("FPREVIEW%02dABCDEFG", i)
		stagingRoot := item.StagingRoot(cfg.Paths.StagingDir)
		encodedDir := filepath.Join(stagingRoot, "encoded")
		if err := os.MkdirAll(encodedDir, 0o755); err != nil {
			t.Fatalf("mkdir encoded: %v", err)
		}
		item.EncodedFile = filepath.Join(encodedDir, "unknown"+strconv.Itoa(i)+".mkv")
		testsupport.WriteFile(t, item.EncodedFile, organizedFixtureSize)
		item.NeedsReview = true
		item.ReviewReason = "No confident TMDB match"
		if err := handler.Prepare(context.Background(), item); err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if err := handler.Execute(context.Background(), item); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if item.Status != queue.StatusCompleted {
			t.Fatalf("expected completed status, got %s", item.Status)
		}
		if item.FinalFile == "" {
			t.Fatal("expected final file path")
		}
		if _, err := os.Stat(item.FinalFile); err != nil {
			t.Fatalf("expected review file to exist: %v", err)
		}
		if _, err := os.Stat(stagingRoot); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected staging root cleanup for review, err=%v", err)
		}
		if _, exists := seenTargets[item.FinalFile]; exists {
			t.Fatalf("duplicate review target generated: %s", item.FinalFile)
		}
		base := filepath.Base(item.FinalFile)
		if !strings.Contains(base, "no-confident-tmdb-match-fpreview") {
			t.Fatalf("expected fingerprint slug in filename, got %s", base)
		}
		seenTargets[item.FinalFile] = struct{}{}
	}

	if len(notifier.reviewed) != 2 {
		t.Fatalf("expected 2 review notifications, got %d", len(notifier.reviewed))
	}
	if len(notifier.completed) != 0 {
		t.Fatalf("did not expect organization-completed notifications, got %d", len(notifier.completed))
	}
}

func TestOrganizerWrapsErrors(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	stubOrganizerProbe(t)

	item, err := store.NewDisc(context.Background(), "Fail", "fp")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusEncoded
	item.DiscFingerprint = "ORGANIZERTESTFPFAIL"
	encodedDir := filepath.Join(item.StagingRoot(cfg.Paths.StagingDir), "encoded")
	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		t.Fatalf("mkdir encoded: %v", err)
	}
	item.EncodedFile = filepath.Join(encodedDir, "fail.mkv")
	testsupport.WriteFile(t, item.EncodedFile, organizedFixtureSize)
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	handler := organizer.NewOrganizerWithDependencies(cfg, store, logging.NewNop(), failingJellyfinService{}, nil)
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err == nil {
		t.Fatal("expected execute error")
	}
}

func TestOrganizerRoutesUnavailableLibraryToReview(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	stubOrganizerProbe(t)

	item, err := store.NewDisc(context.Background(), "Unavailable", "fp-unavail")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusEncoded
	encodedDir := filepath.Join(item.StagingRoot(cfg.Paths.StagingDir), "encoded")
	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		t.Fatalf("mkdir encoded: %v", err)
	}
	item.EncodedFile = filepath.Join(encodedDir, "unavailable.mkv")
	testsupport.WriteFile(t, item.EncodedFile, organizedFixtureSize)
	originalEncoded := item.EncodedFile
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	handler := organizer.NewOrganizerWithDependencies(cfg, store, logging.NewNop(), unavailableJellyfinService{}, &stubNotifier{})
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if item.Status != queue.StatusCompleted {
		t.Fatalf("expected completed status, got %s", item.Status)
	}
	if item.FinalFile == "" {
		t.Fatal("expected final file path")
	}
	if _, err := os.Stat(item.FinalFile); err != nil {
		t.Fatalf("expected review file to exist: %v", err)
	}
	if _, err := os.Stat(originalEncoded); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected encoded file to be moved: %v", err)
	}
	if !strings.Contains(strings.ToLower(item.ProgressStage), "review") {
		t.Fatalf("expected progress stage to mention review, got %q", item.ProgressStage)
	}
}

func TestOrganizerHealthReady(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	handler := organizer.NewOrganizerWithDependencies(cfg, store, logging.NewNop(), &stubJellyfinService{}, &stubNotifier{})
	health := handler.HealthCheck(context.Background())
	if !health.Ready {
		t.Fatalf("expected ready health, got %+v", health)
	}
}

func TestOrganizerHealthMissingJellyfin(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	handler := organizer.NewOrganizerWithDependencies(cfg, store, logging.NewNop(), nil, &stubNotifier{})
	health := handler.HealthCheck(context.Background())
	if health.Ready {
		t.Fatalf("expected not ready health, got %+v", health)
	}
	if !strings.Contains(strings.ToLower(health.Detail), "jellyfin") {
		t.Fatalf("expected detail to reference jellyfin, got %q", health.Detail)
	}
}

type stubNotifier struct {
	completed []string
	reviewed  []string
}

func (s *stubNotifier) Publish(ctx context.Context, event notifications.Event, payload notifications.Payload) error {
	switch event {
	case notifications.EventOrganizationCompleted:
		if payload != nil {
			if title, _ := payload["mediaTitle"].(string); title != "" {
				s.completed = append(s.completed, title)
			}
		}
	case notifications.EventUnidentifiedMedia:
		var label string
		if payload != nil {
			if name, ok := payload["filename"].(string); ok {
				label = name
			} else if name, ok := payload["label"].(string); ok {
				label = name
			}
		}
		s.reviewed = append(s.reviewed, label)
	}
	return nil
}

type stubJellyfinService struct {
	organized string
	targetDir string
}

func (s *stubJellyfinService) Organize(ctx context.Context, sourcePath string, meta jellyfin.MediaMetadata) (string, error) {
	destDir := s.targetDir
	if destDir == "" {
		destDir = filepath.Join(filepath.Dir(sourcePath), "library")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	targetPath := filepath.Join(destDir, filepath.Base(sourcePath))
	if err := os.Rename(sourcePath, targetPath); err != nil {
		return "", err
	}
	s.organized = targetPath
	return targetPath, nil
}

func (s *stubJellyfinService) Refresh(ctx context.Context, meta jellyfin.MediaMetadata) error {
	return nil
}

type blockingJellyfinService struct {
	targetDir     string
	count         int
	startedSecond chan struct{}
	allowSecond   chan struct{}
}

func newBlockingJellyfinService(targetDir string) *blockingJellyfinService {
	return &blockingJellyfinService{
		targetDir:     targetDir,
		startedSecond: make(chan struct{}),
		allowSecond:   make(chan struct{}),
	}
}

func (s *blockingJellyfinService) Organize(ctx context.Context, sourcePath string, meta jellyfin.MediaMetadata) (string, error) {
	s.count++
	if s.count == 2 {
		close(s.startedSecond)
		select {
		case <-s.allowSecond:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	destDir := s.targetDir
	if destDir == "" {
		destDir = filepath.Join(filepath.Dir(sourcePath), "library")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	targetPath := filepath.Join(destDir, filepath.Base(sourcePath))
	if err := os.Rename(sourcePath, targetPath); err != nil {
		return "", err
	}
	return targetPath, nil
}

func (s *blockingJellyfinService) Refresh(ctx context.Context, meta jellyfin.MediaMetadata) error {
	return nil
}

type failingJellyfinService struct{}

func (failingJellyfinService) Organize(ctx context.Context, sourcePath string, meta jellyfin.MediaMetadata) (string, error) {
	return "", errors.New("organize failed")
}

func (failingJellyfinService) Refresh(ctx context.Context, meta jellyfin.MediaMetadata) error {
	return nil
}

type unavailableJellyfinService struct{}

func (unavailableJellyfinService) Organize(ctx context.Context, sourcePath string, meta jellyfin.MediaMetadata) (string, error) {
	return "", fmt.Errorf("create target directory: %w", &os.PathError{
		Op:   "mkdir",
		Path: "/mnt/library",
		Err:  syscall.ENODEV,
	})
}

func (unavailableJellyfinService) Refresh(ctx context.Context, meta jellyfin.MediaMetadata) error {
	return nil
}
