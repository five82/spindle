package stage

import (
	"context"
	"testing"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

func TestSessionProgressWithoutStoreMutatesItem(t *testing.T) {
	item := &queue.Item{}
	s, err := NewSession(context.Background(), nil, item)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if err := s.Progress(42, "Phase 1/1 - Testing", WithActiveEpisode("s01e02"), WithProgressBytes(10, 20)); err != nil {
		t.Fatalf("Progress: %v", err)
	}

	if item.ProgressPercent != 42 {
		t.Fatalf("ProgressPercent = %v, want 42", item.ProgressPercent)
	}
	if item.ProgressMessage != "Phase 1/1 - Testing" {
		t.Fatalf("ProgressMessage = %q", item.ProgressMessage)
	}
	if item.ActiveEpisodeKey != "s01e02" {
		t.Fatalf("ActiveEpisodeKey = %q", item.ActiveEpisodeKey)
	}
	if item.ProgressBytesCopied != 10 || item.ProgressTotalBytes != 20 {
		t.Fatalf("bytes = %d/%d, want 10/20", item.ProgressBytesCopied, item.ProgressTotalBytes)
	}
}

func TestSessionSaveWithoutStoreEncodesRipSpec(t *testing.T) {
	item := &queue.Item{}
	s, err := NewSession(context.Background(), nil, item)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	s.SetEnvelope(&ripspec.Envelope{Version: ripspec.CurrentVersion, Fingerprint: "abc"})

	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if item.RipSpecData == "" {
		t.Fatal("RipSpecData was not set")
	}
	parsed, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		t.Fatalf("parse saved RipSpec: %v", err)
	}
	if parsed.Fingerprint != "abc" {
		t.Fatalf("Fingerprint = %q, want abc", parsed.Fingerprint)
	}
}

func TestSessionSyncAssetPaths(t *testing.T) {
	item := &queue.Item{}
	s, err := NewSession(context.Background(), nil, item)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	s.SetEnvelope(&ripspec.Envelope{Version: ripspec.CurrentVersion})

	s.RecordAssetSuccess(ripspec.AssetKindRipped, ripspec.Asset{EpisodeKey: "s01e01", Path: "first-rip.mkv"})
	s.RecordAssetSuccess(ripspec.AssetKindRipped, ripspec.Asset{EpisodeKey: "s01e02", Path: "second-rip.mkv"})
	s.RecordAssetSuccess(ripspec.AssetKindEncoded, ripspec.Asset{EpisodeKey: "s01e01", Path: "encoded.mkv"})
	s.RecordAssetSuccess(ripspec.AssetKindFinal, ripspec.Asset{EpisodeKey: "s01e01", Path: "final.mkv"})

	if item.RippedFile != "second-rip.mkv" {
		t.Fatalf("RippedFile = %q, want second-rip.mkv", item.RippedFile)
	}
	if item.EncodedFile != "encoded.mkv" {
		t.Fatalf("EncodedFile = %q, want encoded.mkv", item.EncodedFile)
	}
	if item.FinalFile != "final.mkv" {
		t.Fatalf("FinalFile = %q, want final.mkv", item.FinalFile)
	}

	s.RecordAssetFailure(ripspec.AssetKindFinal, "s01e01", "copy failed")
	if item.FinalFile != "" {
		t.Fatalf("FinalFile after failure = %q, want empty", item.FinalFile)
	}
}

func TestSessionSaveAssetHelpersPersistEnvelope(t *testing.T) {
	item := &queue.Item{}
	s, err := NewSession(context.Background(), nil, item)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	s.SetEnvelope(&ripspec.Envelope{Version: ripspec.CurrentVersion})

	if err := s.SaveAssetSuccess(ripspec.AssetKindEncoded, ripspec.Asset{EpisodeKey: "s01e01", Path: "encoded.mkv"}); err != nil {
		t.Fatalf("SaveAssetSuccess: %v", err)
	}
	parsed, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		t.Fatalf("parse after success: %v", err)
	}
	asset, ok := parsed.Assets.FindAsset(ripspec.AssetKindEncoded, "s01e01")
	if !ok || !asset.IsCompleted() || asset.Path != "encoded.mkv" {
		t.Fatalf("encoded asset not persisted: %#v found=%v", asset, ok)
	}
	if item.EncodedFile != "encoded.mkv" {
		t.Fatalf("EncodedFile = %q, want encoded.mkv", item.EncodedFile)
	}

	if err := s.SaveAssetFailure(ripspec.AssetKindEncoded, "s01e01", "encode failed"); err != nil {
		t.Fatalf("SaveAssetFailure: %v", err)
	}
	parsed, err = ripspec.Parse(item.RipSpecData)
	if err != nil {
		t.Fatalf("parse after failure: %v", err)
	}
	asset, ok = parsed.Assets.FindAsset(ripspec.AssetKindEncoded, "s01e01")
	if !ok || !asset.IsFailed() || asset.ErrorMsg != "encode failed" {
		t.Fatalf("failed asset not persisted: %#v found=%v", asset, ok)
	}
	if item.EncodedFile != "" {
		t.Fatalf("EncodedFile after failure = %q, want empty", item.EncodedFile)
	}
}

func TestSessionReviewHelpers(t *testing.T) {
	item := &queue.Item{}
	s, err := NewSession(context.Background(), nil, item)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
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
