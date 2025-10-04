package workflow_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"spindle/internal/config"
	"spindle/internal/encoding"
	"spindle/internal/logging"
	"spindle/internal/organizer"
	"spindle/internal/queue"
)

func manualTestConfig(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	cfg := config.Default()
	cfg.TMDBAPIKey = "test"
	cfg.StagingDir = filepath.Join(base, "staging")
	cfg.LibraryDir = filepath.Join(base, "library")
	cfg.LogDir = filepath.Join(base, "logs")
	cfg.ReviewDir = filepath.Join(base, "review")
	return &cfg
}

func TestManualFileIngestionCompletes(t *testing.T) {
	cfg := manualTestConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})

	ctx := context.Background()
	manualDir := filepath.Join(cfg.StagingDir, "manual")
	if err := os.MkdirAll(manualDir, 0o755); err != nil {
		t.Fatalf("mkdir manual: %v", err)
	}
	manualPath := filepath.Join(manualDir, "Manual Movie.mkv")
	if err := os.WriteFile(manualPath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write manual file: %v", err)
	}

	item, err := store.NewFile(ctx, manualPath)
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}

	logger := logging.NewNop()
	encNotifier := &stubNotifier{}
	encClient := &stubDraptoClient{}
	encoder := encoding.NewEncoderWithDependencies(cfg, store, logger, encClient, encNotifier)

	// Transition to encoding stage
	item.Status = queue.StatusEncoding
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("Update to encoding: %v", err)
	}
	if err := encoder.Prepare(ctx, item); err != nil {
		t.Fatalf("Encoder prepare: %v", err)
	}
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("Persist prepare: %v", err)
	}
	if err := encoder.Execute(ctx, item); err != nil {
		t.Fatalf("Encoder execute: %v", err)
	}
	item.Status = queue.StatusEncoded
	item.LastHeartbeat = nil
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("Persist encode result: %v", err)
	}

	organizedNotifier := &stubNotifier{}
	plexSvc := &stubPlexService{root: cfg.LibraryDir, moviesDir: cfg.MoviesDir, tvDir: cfg.TVDir}
	organizerStage := organizer.NewOrganizerWithDependencies(cfg, store, logger, plexSvc, organizedNotifier)

	item, err = store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID after encode: %v", err)
	}
	if item.Status != queue.StatusEncoded {
		t.Fatalf("expected encoded status, got %s", item.Status)
	}

	item.Status = queue.StatusOrganizing
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("Update to organizing: %v", err)
	}
	if err := organizerStage.Prepare(ctx, item); err != nil {
		t.Fatalf("Organizer prepare: %v", err)
	}
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("Persist organizer prepare: %v", err)
	}
	if err := organizerStage.Execute(ctx, item); err != nil {
		t.Fatalf("Organizer execute: %v", err)
	}
	item.Status = queue.StatusCompleted
	item.LastHeartbeat = nil
	if err := store.Update(ctx, item); err != nil {
		t.Fatalf("Persist organizer result: %v", err)
	}

	final, err := store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID final: %v", err)
	}
	if final.Status != queue.StatusCompleted {
		t.Fatalf("expected completed status, got %s", final.Status)
	}
	if final.FinalFile == "" {
		t.Fatal("expected final file path")
	}
	if _, err := os.Stat(final.FinalFile); err != nil {
		t.Fatalf("expected final file to exist: %v", err)
	}
	if final.MetadataJSON == "" {
		t.Fatal("expected metadata to be stored")
	}
	meta := queue.MetadataFromJSON(final.MetadataJSON, "")
	if meta.Title() == "" {
		t.Fatal("expected metadata title")
	}
	if meta.GetFilename() == "" {
		t.Fatal("expected metadata filename")
	}
	if len(encNotifier.encodeCompletes) == 0 {
		t.Fatal("expected encoder notifier to fire")
	}
	if len(organizedNotifier.organizeCompletes) == 0 {
		t.Fatal("expected organizer notifier to fire")
	}
	if len(organizedNotifier.processingCompletes) == 0 {
		t.Fatal("expected processing completion notifier to fire")
	}
	if !plexSvc.organizeCalled {
		t.Fatal("expected plex organize to run")
	}
}
