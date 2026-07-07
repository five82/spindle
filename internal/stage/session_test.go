package stage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

func newTestSession(t *testing.T) (*queue.Store, *queue.Item, *Session) {
	t.Helper()
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Test", "fp1")
	if err != nil {
		t.Fatalf("new disc: %v", err)
	}
	s, err := NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return store, item, s
}

// newTestSessionWithTask is like newTestSession but attaches a real,
// persisted task row so Session.Progress writes reach the database instead
// of staying in the detached in-memory task.
func newTestSessionWithTask(t *testing.T) (*queue.Store, *queue.Item, *Session) {
	t.Helper()
	store, item, s := newTestSession(t)
	if err := store.EnsureTasks(item, []queue.TaskSpec{{Type: queue.StageIdentification}}); err != nil {
		t.Fatalf("ensure tasks: %v", err)
	}
	tasks, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatalf("tasks for item: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	s.Task = tasks[0]
	return store, item, s
}

func TestNewSessionRequiresStore(t *testing.T) {
	_, err := NewSession(context.Background(), nil, &queue.Item{}, nil)
	if err == nil {
		t.Fatal("NewSession succeeded with nil store")
	}
}

func TestNewSessionRejectsInvalidRipSpec(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	_, err = NewSession(context.Background(), store, &queue.Item{RipSpecData: "{bad json"}, nil)
	if err == nil {
		t.Fatal("NewSession succeeded with invalid RipSpec")
	}
	if !strings.Contains(err.Error(), "invalid rip spec") {
		t.Fatalf("error = %v, want invalid rip spec", err)
	}
}

func TestSessionProgressPersistsTask(t *testing.T) {
	store, item, s := newTestSessionWithTask(t)

	if err := s.Progress(42, "Phase 1/1 - Testing", WithActiveEpisode("s01e02"), WithProgressBytes(10, 20), WithEncodingDetails(`{"substage":"encoding"}`)); err != nil {
		t.Fatalf("Progress: %v", err)
	}

	tasks, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatalf("tasks for item: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	got := tasks[0]
	if got.ProgressPercent != 42 {
		t.Fatalf("ProgressPercent = %v, want 42", got.ProgressPercent)
	}
	if got.ProgressMessage != "Phase 1/1 - Testing" {
		t.Fatalf("ProgressMessage = %q", got.ProgressMessage)
	}
	if got.ActiveAssetKey != "s01e02" {
		t.Fatalf("ActiveAssetKey = %q", got.ActiveAssetKey)
	}
	if got.ProgressBytesCopied != 10 || got.ProgressTotalBytes != 20 {
		t.Fatalf("bytes = %d/%d, want 10/20", got.ProgressBytesCopied, got.ProgressTotalBytes)
	}

	gotItem, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if gotItem.EncodingDetailsJSON != `{"substage":"encoding"}` {
		t.Fatalf("EncodingDetailsJSON = %q", gotItem.EncodingDetailsJSON)
	}
}

func TestSessionProgressDetachedTaskStaysInMemory(t *testing.T) {
	store, item, s := newTestSession(t)

	if err := s.Progress(42, "Phase 1/1 - Testing"); err != nil {
		t.Fatalf("Progress: %v", err)
	}

	if s.Task.ProgressPercent != 42 || s.Task.ProgressMessage != "Phase 1/1 - Testing" {
		t.Fatalf("detached task not updated in memory: %+v", s.Task)
	}
	tasks, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatalf("tasks for item: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("detached task should not persist a row, got %d", len(tasks))
	}
}

func TestSessionSavePersistsRipSpec(t *testing.T) {
	store, item, s := newTestSession(t)
	s.SetEnvelope(&ripspec.Envelope{Version: ripspec.CurrentVersion, Fingerprint: "abc"})

	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.RipSpecData == "" {
		t.Fatal("RipSpecData was not set")
	}
	parsed, err := ripspec.Parse(got.RipSpecData)
	if err != nil {
		t.Fatalf("parse saved RipSpec: %v", err)
	}
	if parsed.Fingerprint != "abc" {
		t.Fatalf("Fingerprint = %q, want abc", parsed.Fingerprint)
	}
}

func TestSessionSaveDoesNotChangeLifecycleFields(t *testing.T) {
	store, item, s := newTestSession(t)
	if err := store.FailStage(item, queue.StageRipping, "existing error"); err != nil {
		t.Fatalf("initial failure: %v", err)
	}

	item.Stage = queue.StageCompleted
	item.InProgress = 0
	item.FailedAtStage = ""
	item.ErrorMessage = ""
	s.SetEnvelope(&ripspec.Envelope{Version: ripspec.CurrentVersion, Fingerprint: "abc"})
	s.AddReviewReason("needs review")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.Stage != queue.StageFailed || got.InProgress != 0 || got.FailedAtStage != queue.StageRipping || got.ErrorMessage != "existing error" {
		t.Fatalf("lifecycle fields changed: stage=%q in_progress=%d failed_at=%q error=%q", got.Stage, got.InProgress, got.FailedAtStage, got.ErrorMessage)
	}
	parsed, err := ripspec.Parse(got.RipSpecData)
	if err != nil {
		t.Fatalf("parse saved RipSpec: %v", err)
	}
	if parsed.Fingerprint != "abc" || got.NeedsReview != 1 {
		t.Fatalf("work state not persisted: fingerprint=%q needs_review=%d", parsed.Fingerprint, got.NeedsReview)
	}
}

func TestSessionSaveAssetHelpersPersistEnvelope(t *testing.T) {
	store, item, s := newTestSession(t)
	s.SetEnvelope(&ripspec.Envelope{Version: ripspec.CurrentVersion})

	if err := s.SaveAssetSuccess(ripspec.AssetKindEncoded, ripspec.Asset{EpisodeKey: "s01e01", Path: "encoded.mkv"}); err != nil {
		t.Fatalf("SaveAssetSuccess: %v", err)
	}
	got, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatalf("get item after success: %v", err)
	}
	parsed, err := ripspec.Parse(got.RipSpecData)
	if err != nil {
		t.Fatalf("parse after success: %v", err)
	}
	asset, ok := parsed.Assets.FindAsset(ripspec.AssetKindEncoded, "s01e01")
	if !ok || !asset.IsCompleted() || asset.Path != "encoded.mkv" {
		t.Fatalf("encoded asset not persisted: %#v found=%v", asset, ok)
	}

	if err := s.SaveAssetFailure(ripspec.AssetKindEncoded, "s01e01", "encode failed"); err != nil {
		t.Fatalf("SaveAssetFailure: %v", err)
	}
	got, err = store.GetByID(item.ID)
	if err != nil {
		t.Fatalf("get item after failure: %v", err)
	}
	parsed, err = ripspec.Parse(got.RipSpecData)
	if err != nil {
		t.Fatalf("parse after failure: %v", err)
	}
	asset, ok = parsed.Assets.FindAsset(ripspec.AssetKindEncoded, "s01e01")
	if !ok || !asset.IsFailed() || asset.ErrorMsg != "encode failed" {
		t.Fatalf("failed asset not persisted: %#v found=%v", asset, ok)
	}
}

func TestSessionReviewHelpers(t *testing.T) {
	_, item, s := newTestSession(t)
	s.SetEnvelope(&ripspec.Envelope{
		Version:  ripspec.CurrentVersion,
		Episodes: []ripspec.Episode{{Key: "s01e01"}},
	})

	s.AddReviewReason("queue review")
	if item.NeedsReview != 1 || item.PrimaryReviewReason() != "queue review" {
		t.Fatalf("queue review not applied: needs=%d reason=%q", item.NeedsReview, item.ReviewReason)
	}
	if !s.AddEpisodeReviewReason("S01E01", "episode review") {
		t.Fatal("episode review helper returned false")
	}
	if got := s.Env.Episodes[0].ReviewReason; got != "episode review" {
		t.Fatalf("episode review reason = %q", got)
	}
}
