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

func TestCopyAssetsReplacesPartialDestinationAndRetainsSource(t *testing.T) {
	dir := t.TempDir()
	store, err := queue.Open(filepath.Join(dir, "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	item, err := store.NewDisc("Film", "fp")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "source.mkv")
	if err := os.WriteFile(src, []byte("complete video"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "movie"}}
	env.Assets.AddAsset(ripspec.AssetKindEncoded, ripspec.Asset{EpisodeKey: "main", Path: src, Status: ripspec.AssetStatusCompleted})
	sess.SetEnvelope(env)
	sess.Task = &queue.Task{}
	sess.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	meta := mediameta.FromJSON(`{"title":"Film","media_type":"movie","year":"2020"}`, "Film")
	destDir := filepath.Join(dir, "library")
	if err := os.Mkdir(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(destDir, mediameta.DestFilename(&meta, "main", ".mkv", 0, 0, 0))
	if err := os.WriteFile(dest, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := New(&config.Config{}, nil, nil)
	got, count, err := h.copyAssetsToDir(context.Background(), sess.Logger, sess, &meta, destDir, []string{"main"}, "library")
	if err != nil || count != 1 || got != dest {
		t.Fatalf("copy: %s %d %v", got, count, err)
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "complete video" {
		t.Fatalf("partial file not replaced: %q %v", data, err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("library delivery moved source: %v", err)
	}
}

func TestMoveOrCopyRejectsNonCrossDeviceRenameError(t *testing.T) {
	err := moveOrCopyWithProgress(filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "dest"), nil)
	if err == nil || !strings.Contains(err.Error(), "move file") {
		t.Fatalf("rename: %v", err)
	}
}
