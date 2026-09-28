package stage

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

func TestSessionMergeFailsSafelyWhenItemDisappears(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	item, err := store.NewDisc("Example", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Task.ItemID != item.ID || sess.Ctx == nil {
		t.Fatalf("session defaults: %+v", sess)
	}
	sess.SetEnvelope(&ripspec.Envelope{Version: ripspec.CurrentVersion})
	mutate := func(*ripspec.Envelope) error { return errors.New("reject mutation") }
	if err := sess.MergeSave(mutate); err == nil || !strings.Contains(err.Error(), "reject mutation") {
		t.Fatalf("mutation failure: %v", err)
	}
	if _, err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := sess.MergeSave(func(*ripspec.Envelope) error { return nil }); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("removed merge: %v", err)
	}
	if err := sess.RefreshEnvelope(); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("removed refresh: %v", err)
	}
	if err := sess.MergeAddReviewReason("check audio"); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("removed review: %v", err)
	}
	if _, err := NewSession(context.Background(), store, &queue.Item{RipSpecData: "not json"}, nil); err == nil {
		t.Fatal("invalid envelope accepted")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for name, fn := range map[string]func() error{
		"merge":   func() error { return sess.MergeSave(func(*ripspec.Envelope) error { return nil }) },
		"refresh": sess.RefreshEnvelope,
		"review":  func() error { return sess.MergeAddReviewReason("needs review") },
		"save":    sess.Save,
	} {
		if err := fn(); err == nil {
			t.Errorf("%s on closed queue store succeeded", name)
		}
	}
}
