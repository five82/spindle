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
