package workflow_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spindle/internal/config"
	"spindle/internal/daemon"
	"spindle/internal/disc"
	"spindle/internal/encoding"
	"spindle/internal/identification"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/organizer"
	"spindle/internal/queue"
	"spindle/internal/ripping"
	"spindle/internal/workflow"
)

func TestDaemonEndToEndWorkflow(t *testing.T) {
	cfgValue := config.Default()
	cfg := &cfgValue

	base := t.TempDir()
	cfg.StagingDir = filepath.Join(base, "staging")
	cfg.LibraryDir = filepath.Join(base, "library")
	cfg.LogDir = filepath.Join(base, "logs")
	cfg.ReviewDir = filepath.Join(base, "review")
	cfg.TMDBAPIKey = "daemon-e2e-key"
	cfg.OpticalDrive = filepath.Join(base, "devices", "sr0")
	cfg.MoviesDir = "movies"
	cfg.TVDir = "tv"
	cfg.QueuePollInterval = 0
	cfg.ErrorRetryInterval = 1
	cfg.WorkflowHeartbeatInterval = 1
	cfg.WorkflowHeartbeatTimeout = 5

	if err := cfg.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories: %v", err)
	}
	logPath := filepath.Join(cfg.LogDir, "daemon-e2e.log")

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
	ejector := &fakeEjector{}
	draptoClient := &stubDraptoClient{}
	plexClient := &stubPlexService{root: cfg.LibraryDir, moviesDir: cfg.MoviesDir, tvDir: cfg.TVDir}

	identifier := identification.NewIdentifierWithDependencies(cfg, store, logger, tmdbClient, scanner, notifier)
	ripper := ripping.NewRipperWithDependencies(cfg, store, logger, ripperClient, ejector, notifier)
	encoder := encoding.NewEncoderWithDependencies(cfg, store, logger, draptoClient, notifier)
	organizerStage := organizer.NewOrganizerWithDependencies(cfg, store, logger, plexClient, notifier)

	mgr := workflow.NewManagerWithNotifier(cfg, store, logger, notifier)
	mgr.ConfigureStages(workflow.StageSet{
		Identifier: identifier,
		Ripper:     ripper,
		Encoder:    encoder,
		Organizer:  organizerStage,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d, err := daemon.New(cfg, store, logger, mgr, logPath)
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		d.Stop()
		_ = d.Close()
	})

	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon.Start: %v", err)
	}

	item, err := store.NewDisc(ctx, "Daemon Disc", "")
	if err != nil {
		t.Fatalf("store.NewDisc: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 180*time.Second)
	defer waitCancel()
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
		}
		if updated.Status == queue.StatusFailed || updated.Status == queue.StatusReview {
			t.Fatalf("queue item ended in status %s: %s", updated.Status, updated.ErrorMessage)
		}
		item = updated
		if updated.Status == queue.StatusCompleted {
			if updated.RippedFile == "" {
				t.Fatal("expected ripped file path")
			}
			if updated.EncodedFile == "" {
				t.Fatal("expected encoded file path")
			}
			if updated.FinalFile == "" {
				t.Fatal("expected final file path")
			}
			if _, err := os.Stat(updated.FinalFile); err != nil {
				t.Fatalf("final file missing: %v", err)
			}
			if updated.MetadataJSON == "" {
				t.Fatal("expected metadata json")
			}
			meta := queue.MetadataFromJSON(updated.MetadataJSON, updated.DiscTitle)
			if meta.Title() != "Daemon Disc" {
				t.Fatalf("expected metadata title 'Daemon Disc', got %q", meta.Title())
			}
			if len(tmdbClient.queries) == 0 {
				t.Fatal("expected TMDB search to be invoked")
			}
			if scanner.calls == 0 {
				t.Fatal("expected disc scanner to be used")
			}
			if ripperClient.calls == 0 {
				t.Fatal("expected MakeMKV ripper to be invoked")
			}
			if ejector.calls == 0 {
				t.Fatal("expected ejector to run")
			}
			if len(notifier.encodeCompletes) == 0 {
				t.Fatal("expected encoding notification")
			}
			if len(notifier.organizeCompletes) == 0 {
				t.Fatal("expected organization notification")
			}
			if len(notifier.processingCompletes) == 0 {
				t.Fatal("expected processing completion notification")
			}
			if len(notifier.ripStarts) == 0 {
				t.Fatal("expected rip start notification")
			}
			if len(notifier.ripCompletes) == 0 {
				t.Fatal("expected rip completion notification")
			}
			if !plexClient.organizeCalled {
				t.Fatal("expected plex organize to run")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
