package workflow_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spindle/internal/config"
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

func TestWorkflowIntegrationEndToEnd(t *testing.T) {
	cfgValue := config.Default()
	cfg := &cfgValue

	stubValidationProbes(t)
	base := t.TempDir()
	cfg.StagingDir = filepath.Join(base, "staging")
	cfg.LibraryDir = filepath.Join(base, "library")
	cfg.LogDir = filepath.Join(base, "logs")
	cfg.ReviewDir = filepath.Join(base, "review")
	cfg.TMDBAPIKey = "integration-test-key"
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

	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})

	logger := logging.NewNop()
	notifier := &stubNotifier{}

	tmdbClient := &fakeTMDB{
		response: &tmdb.Response{
			Results: []tmdb.Result{
				{
					ID:          101,
					Title:       "Integration Disc",
					MediaType:   "movie",
					VoteAverage: 8.5,
					VoteCount:   120,
				},
			},
		},
	}

	scanner := &fakeDiscScanner{
		result: &disc.ScanResult{
			Fingerprint: "fp-integration-001",
			Titles: []disc.Title{
				{ID: 1, Name: "Integration Disc", Duration: 5400},
			},
		},
	}

	ripperClient := &fakeMakemkvClient{}
	draptoClient := &stubDraptoClient{}
	plexClient := &stubPlexService{root: cfg.LibraryDir, moviesDir: cfg.MoviesDir, tvDir: cfg.TVDir}

	identifier := identification.NewIdentifierWithDependencies(cfg, store, logger, tmdbClient, scanner, notifier)
	ripper := ripping.NewRipperWithDependencies(cfg, store, logger, ripperClient, notifier)
	encoder := encoding.NewEncoderWithDependencies(cfg, store, logger, draptoClient, notifier)
	organizer := organizer.NewOrganizerWithDependencies(cfg, store, logger, plexClient, notifier)

	mgr := workflow.NewManagerWithNotifier(cfg, store, logger, notifier)
	mgr.ConfigureStages(workflow.StageSet{
		Identifier: identifier,
		Ripper:     ripper,
		Encoder:    encoder,
		Organizer:  organizer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("manager.Start: %v", err)
	}
	t.Cleanup(func() {
		mgr.Stop()
	})

	item, err := store.NewDisc(ctx, "Integration Disc", "fp-integration")
	if err != nil {
		t.Fatalf("store.NewDisc: %v", err)
	}

	deadline := time.After(120 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for workflow completion")
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
				t.Fatal("expected metadata json to be populated")
			}
			meta := queue.MetadataFromJSON(updated.MetadataJSON, updated.DiscTitle)
			if meta.Title() != "Integration Disc" {
				t.Fatalf("expected metadata title 'Integration Disc', got %q", meta.Title())
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
			if len(notifier.encodeCompletes) == 0 {
				t.Fatal("expected encoding notification")
			}
			if len(notifier.organizeCompletes) == 0 {
				t.Fatal("expected organization notification")
			}
			if len(notifier.processingCompletes) == 0 {
				t.Fatal("expected processing completion notification")
			}
			if !plexClient.organizeCalled {
				t.Fatal("expected plex organize to run")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
