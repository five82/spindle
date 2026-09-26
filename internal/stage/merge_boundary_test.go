package stage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func TestMergeSaveRetainsConcurrentChangesAndLocalState(t *testing.T) {
	store, item, first := newTestSession(t)
	first.Env.Version = ripspec.CurrentVersion
	first.Env.Fingerprint = "seed"
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}
	second, err := NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	second.Env.Fingerprint = "unsaved local change"
	if err := first.MergeSave(func(env *ripspec.Envelope) error { env.Fingerprint = "fresh"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := second.MergeSave(func(env *ripspec.Envelope) error {
		env.Assets.AddAsset(ripspec.AssetKindEncoded, ripspec.Asset{EpisodeKey: "main", Path: "encoded.mkv"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if second.Env.Fingerprint != "unsaved local change" {
		t.Fatalf("local state overwritten: %q", second.Env.Fingerprint)
	}
	persisted, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	env, err := ripspec.Parse(persisted.RipSpecData)
	if err != nil {
		t.Fatal(err)
	}
	if env.Fingerprint != "fresh" {
		t.Fatalf("fresh state lost: %q", env.Fingerprint)
	}
	if asset, ok := env.Assets.FindAsset(ripspec.AssetKindEncoded, "main"); !ok || asset.Path != "encoded.mkv" {
		t.Fatalf("merged asset: %+v %v", asset, ok)
	}
	if err := second.RefreshEnvelope(); err != nil {
		t.Fatal(err)
	}
	if second.Env.Fingerprint != "fresh" {
		t.Fatalf("refresh: %q", second.Env.Fingerprint)
	}
}

func TestMergeSaveRejectsMutationAndMissingState(t *testing.T) {
	store, item, s := newTestSession(t)
	s.Env.Version = ripspec.CurrentVersion
	if err := s.MergeSave(func(*ripspec.Envelope) error { return errors.New("do not save") }); err == nil || !strings.Contains(err.Error(), "do not save") {
		t.Fatalf("mutation error: %v", err)
	}
	fresh, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.RipSpecData != "" {
		t.Fatalf("unexpected persisted data: %q", fresh.RipSpecData)
	}
	if err := s.MergeSave(func(env *ripspec.Envelope) error { env.Fingerprint = "seed"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.MergeSave(func(env *ripspec.Envelope) error { env.Fingerprint = "next"; return nil }); err != nil {
		t.Fatal(err)
	}
	if s.Env.Fingerprint != "next" {
		t.Fatalf("local state: %q", s.Env.Fingerprint)
	}
	if err := (&Session{}).MergeSave(nil); err == nil {
		t.Fatal("incomplete merge accepted")
	}
	if err := (&Session{}).RefreshEnvelope(); err == nil {
		t.Fatal("incomplete refresh accepted")
	}
	if err := (&Session{}).MergeAddReviewReason("reason"); err == nil {
		t.Fatal("incomplete review accepted")
	}
}

func TestSessionCancelledSaveAndMissingItem(t *testing.T) {
	store, _, sess := newTestSession(t)
	if _, err := NewSession(context.Background(), store, nil, nil); err == nil || !strings.Contains(err.Error(), "nil queue item") {
		t.Fatalf("missing item: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sess.Ctx = ctx
	if err := sess.Save(); err != context.Canceled {
		t.Fatalf("cancelled save: %v", err)
	}
	if err := (&Session{}).Save(); err == nil || !strings.Contains(err.Error(), "incomplete save state") {
		t.Fatalf("incomplete save: %v", err)
	}
}

func TestMergeReviewDeduplicatesAgainstFreshItem(t *testing.T) {
	store, item, first := newTestSession(t)
	second, err := NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.MergeAddReviewReason("same reason"); err != nil {
		t.Fatal(err)
	}
	if err := second.MergeAddReviewReason("same reason"); err != nil {
		t.Fatal(err)
	}
	if err := second.MergeAddReviewReason("different reason"); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ReviewReasons()) != 2 || len(second.Item.ReviewReasons()) != 2 {
		t.Fatalf("review reasons: %v", got.ReviewReasons())
	}
}
