package organizer_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
	"spindle/internal/notifications"
	"spindle/internal/organizer"
	"spindle/internal/queue"
	"spindle/internal/services/plex"
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
	stagingRoot := item.StagingRoot(cfg.StagingDir)
	encodedDir := filepath.Join(stagingRoot, "encoded")
	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		t.Fatalf("mkdir encoded: %v", err)
	}
	item.EncodedFile = filepath.Join(encodedDir, "demo.encoded.mkv")
	testsupport.WriteFile(t, item.EncodedFile, organizedFixtureSize)
	item.MetadataJSON = `{"title":"Demo", "filename":"Demo", " library_path":"` + filepath.Join(cfg.LibraryDir, cfg.MoviesDir) + `", "movie":true}`
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	stubPlex := &stubPlexService{targetDir: filepath.Join(cfg.LibraryDir, cfg.MoviesDir)}
	notifier := &stubNotifier{}
	handler := organizer.NewOrganizerWithDependencies(cfg, store, logging.NewNop(), stubPlex, notifier)
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

func TestOrganizerRoutesUnidentifiedToReview(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	stubOrganizerProbe(t)

	notifier := &stubNotifier{}
	handler := organizer.NewOrganizerWithDependencies(cfg, store, logging.NewNop(), &stubPlexService{}, notifier)

	seenTargets := map[string]struct{}{}
	for i := 0; i < 2; i++ {
		item, err := store.NewDisc(context.Background(), "Unknown", "fp-review")
		if err != nil {
			t.Fatalf("NewDisc: %v", err)
		}
		item.Status = queue.StatusEncoded
		item.DiscFingerprint = fmt.Sprintf("FPREVIEW%02dABCDEFG", i)
		stagingRoot := item.StagingRoot(cfg.StagingDir)
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
	encodedDir := filepath.Join(item.StagingRoot(cfg.StagingDir), "encoded")
	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		t.Fatalf("mkdir encoded: %v", err)
	}
	item.EncodedFile = filepath.Join(encodedDir, "fail.mkv")
	testsupport.WriteFile(t, item.EncodedFile, organizedFixtureSize)
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}

	handler := organizer.NewOrganizerWithDependencies(cfg, store, logging.NewNop(), failingPlexService{}, nil)
	if err := handler.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := handler.Execute(context.Background(), item); err == nil {
		t.Fatal("expected execute error")
	}
}

func TestOrganizerHealthReady(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	handler := organizer.NewOrganizerWithDependencies(cfg, store, logging.NewNop(), &stubPlexService{}, &stubNotifier{})
	health := handler.HealthCheck(context.Background())
	if !health.Ready {
		t.Fatalf("expected ready health, got %+v", health)
	}
}

func TestOrganizerHealthMissingPlex(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	handler := organizer.NewOrganizerWithDependencies(cfg, store, logging.NewNop(), nil, &stubNotifier{})
	health := handler.HealthCheck(context.Background())
	if health.Ready {
		t.Fatalf("expected not ready health, got %+v", health)
	}
	if !strings.Contains(strings.ToLower(health.Detail), "plex") {
		t.Fatalf("expected detail to reference plex, got %q", health.Detail)
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

type stubPlexService struct {
	organized string
	targetDir string
}

func (s *stubPlexService) Organize(ctx context.Context, sourcePath string, meta plex.MediaMetadata) (string, error) {
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

func (s *stubPlexService) Refresh(ctx context.Context, meta plex.MediaMetadata) error {
	return nil
}

type failingPlexService struct{}

func (failingPlexService) Organize(ctx context.Context, sourcePath string, meta plex.MediaMetadata) (string, error) {
	return "", errors.New("organize failed")
}

func (failingPlexService) Refresh(ctx context.Context, meta plex.MediaMetadata) error {
	return nil
}
