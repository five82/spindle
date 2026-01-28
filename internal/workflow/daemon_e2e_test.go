package workflow_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spindle/internal/daemon"
	"spindle/internal/disc"
	"spindle/internal/encoding"
	"spindle/internal/identification"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/organizer"
	"spindle/internal/queue"
	"spindle/internal/ripping"
	"spindle/internal/testsupport"
	"spindle/internal/workflow"
)

func TestDaemonEndToEndWorkflow(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())

	stubValidationProbes(t)

	base := t.TempDir()
	cfg.Paths.StagingDir = filepath.Join(base, "staging")
	cfg.Paths.LibraryDir = filepath.Join(base, "library")
	cfg.Paths.LogDir = filepath.Join(base, "logs")
	cfg.Paths.ReviewDir = filepath.Join(base, "review")
	cfg.Paths.APIBind = "127.0.0.1:0"
	cfg.TMDB.APIKey = "daemon-e2e-key"
	cfg.MakeMKV.OpticalDrive = filepath.Join(base, "devices", "sr0")
	cfg.Library.MoviesDir = "movies"
	cfg.Library.TVDir = "tv"
	cfg.Workflow.QueuePollInterval = 0
	cfg.Workflow.ErrorRetryInterval = 1
	cfg.Workflow.HeartbeatInterval = 1
	cfg.Workflow.HeartbeatTimeout = 5

	if err := cfg.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories: %v", err)
	}
	logPath := filepath.Join(cfg.Paths.LogDir, "daemon-e2e.log")

	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}

	logger := logging.NewNop()
	notifier := &stubNotifier{}

	tmdbClient := &fakeTMDB{
		response: &tmdb.Response{
			Results: []tmdb.Result{
				{
					ID:          202,
					Title:       "Daemon Disc",
					MediaType:   "movie",
					VoteAverage: 9.0,
					VoteCount:   42,
				},
			},
		},
	}

	scanner := &fakeDiscScanner{
		result: &disc.ScanResult{
			Fingerprint: "fp-daemon-001",
			Titles: []disc.Title{
				{ID: 1, Name: "Daemon Disc", Duration: 6000},
			},
		},
	}

	ripperClient := &fakeMakemkvClient{}
	draptoClient := &stubDraptoClient{}
	jellyfinClient := &stubJellyfinService{root: cfg.Paths.LibraryDir, moviesDir: cfg.Library.MoviesDir, tvDir: cfg.Library.TVDir}

	identifier := identification.NewIdentifierWithDependencies(cfg, store, logger, tmdbClient, scanner, notifier)
	ripper := ripping.NewRipperWithDependencies(cfg, store, logger, ripperClient, notifier)
	encoder := encoding.NewEncoderWithDependencies(cfg, store, logger, draptoClient, notifier)
	organizerStage := organizer.NewOrganizerWithDependencies(cfg, store, logger, jellyfinClient, notifier)

	mgr := workflow.NewManagerWithNotifier(cfg, store, logger, notifier)
	mgr.ConfigureStages(workflow.StageSet{
		Identifier: identifier,
		Ripper:     ripper,
		Encoder:    encoder,
		Organizer:  organizerStage,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d, err := daemon.New(cfg, store, logger, mgr, logPath, logging.NewStreamHub(128), nil)
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		d.Stop(context.Background())
		_ = d.Close()
	})

	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon.Start: %v", err)
	}

	item, err := store.NewDisc(ctx, "Daemon Disc", "fp-daemon")
	if err != nil {
		t.Fatalf("store.NewDisc: %v", err)
	}

	// Wait for workflow completion with timeout
	waitCtx, waitCancel := context.WithTimeout(ctx, 30*time.Second)
	defer waitCancel()

	var finalItem *queue.Item
	for {
		select {
		case <-waitCtx.Done():
			t.Fatalf("timed out waiting for daemon workflow completion: last status %s", item.Status)
		default:
		}

		updated, err := store.GetByID(ctx, item.ID)
		if err != nil {
			t.Fatalf("store.GetByID: %v", err)
		}
		if updated == nil {
			t.Fatal("queue item disappeared")
			return
		}
		if updated.Status == queue.StatusFailed {
			t.Fatalf("queue item ended in status %s: %s", updated.Status, updated.ErrorMessage)
		}
		item = updated
		if updated.Status == queue.StatusCompleted {
			finalItem = updated
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Run subtests to verify different aspects of the completed workflow
	t.Run("item status", func(t *testing.T) {
		if finalItem.Status != queue.StatusCompleted {
			t.Fatalf("expected status %s, got %s", queue.StatusCompleted, finalItem.Status)
		}
	})

	t.Run("file paths populated", func(t *testing.T) {
		if finalItem.RippedFile == "" {
			t.Error("expected ripped file path to be set")
		}
		if finalItem.EncodedFile == "" {
			t.Error("expected encoded file path to be set")
		}
		if finalItem.FinalFile == "" {
			t.Error("expected final file path to be set")
		}
	})

	t.Run("final file exists", func(t *testing.T) {
		if _, err := os.Stat(finalItem.FinalFile); err != nil {
			t.Fatalf("final file missing: %v", err)
		}
	})

	t.Run("metadata populated", func(t *testing.T) {
		if finalItem.MetadataJSON == "" {
			t.Fatal("expected metadata json to be set")
		}
		meta := queue.MetadataFromJSON(finalItem.MetadataJSON, finalItem.DiscTitle)
		if meta.Title() != "Daemon Disc" {
			t.Fatalf("expected metadata title 'Daemon Disc', got %q", meta.Title())
		}
	})

	t.Run("TMDB client invoked", func(t *testing.T) {
		if len(tmdbClient.queries) == 0 {
			t.Error("expected TMDB search to be invoked")
		}
	})

	t.Run("disc scanner invoked", func(t *testing.T) {
		if scanner.calls == 0 {
			t.Error("expected disc scanner to be used")
		}
	})

	t.Run("MakeMKV ripper invoked", func(t *testing.T) {
		if ripperClient.calls == 0 {
			t.Error("expected MakeMKV ripper to be invoked")
		}
	})

	t.Run("jellyfin organize called", func(t *testing.T) {
		if !jellyfinClient.organizeCalled {
			t.Error("expected jellyfin organize to run")
		}
	})

	t.Run("notifications sent", func(t *testing.T) {
		if len(notifier.ripStarts) == 0 {
			t.Error("expected rip start notification")
		}
		if len(notifier.ripCompletes) == 0 {
			t.Error("expected rip completion notification")
		}
		if len(notifier.encodeCompletes) == 0 {
			t.Error("expected encoding notification")
		}
		if len(notifier.organizeCompletes) == 0 {
			t.Error("expected organization notification")
		}
		if len(notifier.processingCompletes) == 0 {
			t.Error("expected processing completion notification")
		}
	})
}
