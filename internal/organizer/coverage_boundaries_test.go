package organizer

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/mediameta"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func TestCopyAssetsPreflightCancellationAndMissingSource(t *testing.T) {
	root := t.TempDir()
	store, err := queue.Open(filepath.Join(root, "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	item, err := store.NewDisc("Example", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sess.Logger = logger
	src := filepath.Join(root, "missing.mkv")
	sess.SetEnvelope(&ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "movie"}, Assets: ripspec.Assets{Encoded: []ripspec.Asset{{EpisodeKey: "main", Path: src, Status: ripspec.AssetStatusCompleted}}}})
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: filepath.Join(root, "staging")}}, nil, nil)
	meta := &mediameta.Metadata{}
	blocker := filepath.Join(root, "block")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.copyAssetsToDir(context.Background(), logger, sess, meta, filepath.Join(blocker, "child"), []string{"main"}, "library"); err == nil || !strings.Contains(err.Error(), "create library dir") {
		t.Fatalf("blocked destination: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := h.copyAssetsToDir(ctx, logger, sess, meta, filepath.Join(root, "library"), []string{"main"}, "library"); err != context.Canceled {
		t.Fatalf("canceled: %v", err)
	}
	if _, _, err := h.copyAssetsToDir(context.Background(), logger, sess, meta, filepath.Join(root, "library"), []string{"main"}, "library"); err == nil || !strings.Contains(err.Error(), "copy main to library") {
		t.Fatalf("missing source: %v", err)
	}
	if _, copied, err := h.copyAssetsToDir(context.Background(), logger, sess, meta, filepath.Join(root, "library"), nil, "library"); err != nil || copied != 0 {
		t.Fatalf("no assets: %d %v", copied, err)
	}
	h.cleanupStaging(logger, &queue.Item{}) // unresolved staging root must not delete unrelated files
	h.cfg.Paths.StagingDir = ""
	h.cleanupStaging(logger, &queue.Item{ID: 9}) // invalid root is a warning, not an error
	h.cfg.Library.MoviesDir = "../escape"
	if _, err := h.placeInLibrary(context.Background(), logger, sess, &mediameta.Metadata{MediaType: "movie", Title: "Example"}, []string{"main"}); err == nil || !strings.Contains(err.Error(), "resolve library path") {
		t.Fatalf("unsafe library destination: %v", err)
	}
}
