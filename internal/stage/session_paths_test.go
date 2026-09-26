package stage

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func TestSessionStageDirAndEnvelopeReset(t *testing.T) {
	_, item, sess := newTestSession(t)
	base := t.TempDir()
	dir, err := sess.StageDir(base, "transcripts", "episode")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, strings.ToUpper(item.DiscFingerprint), "transcripts", "episode")
	if dir != want {
		t.Fatalf("dir = %q, want %q", dir, want)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("stage dir not created: %v", err)
	}
	sess.SetEnvelope(nil)
	if sess.Env == nil || sess.Env.Version != 0 {
		t.Fatalf("nil envelope not reset: %+v", sess.Env)
	}
	if err := os.WriteFile(filepath.Join(base, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.StageDir(filepath.Join(base, "file"), "child"); err == nil {
		t.Fatal("expected mkdir failure")
	}
}

func TestSessionProgressFailureIsNonFatal(t *testing.T) {
	store, _, sess := newTestSessionWithTask(t)
	sess.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	sess.Progress(55, "Phase 1/1 - Encoding", WithEncodingDetails(`{"state":"encoding"}`))
	if sess.Task.ProgressPercent != 55 {
		t.Fatalf("memory progress = %v", sess.Task.ProgressPercent)
	}
	sess.Progress(60, "Phase 1/1 - Encoding")
	if sess.Task.ProgressPercent != 60 {
		t.Fatalf("memory progress = %v", sess.Task.ProgressPercent)
	}
}

func TestSessionActiveEpisodeUpdatesAndClears(t *testing.T) {
	store, item, sess := newTestSessionWithTask(t)
	sess.Progress(25, "Phase 1/1 - Encoding")
	sess.SetActiveEpisode("s01e01")
	tasks, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].ActiveAssetKey != "s01e01" || tasks[0].ProgressPercent != 25 {
		t.Fatalf("active episode update: %+v", tasks[0])
	}
	sess.ClearActiveEpisode()
	tasks, err = store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].ActiveAssetKey != "" || tasks[0].ProgressPercent != 25 {
		t.Fatalf("active episode clear: %+v", tasks[0])
	}
	sess.SetEnvelope(&ripspec.Envelope{Version: ripspec.CurrentVersion})
	if sess.AddEpisodeReviewReason("missing", "review") {
		t.Fatal("missing episode accepted")
	}
}
