package ripcache

import (
	"context"
	"testing"

	"spindle/internal/queue"
)

func TestRipCacheMetadataRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	item := &queue.Item{
		DiscTitle:       "Test Disc",
		DiscFingerprint: "FP-1234",
		RipSpecData:     `{"fingerprint":"FP-1234"}`,
		MetadataJSON:    `{"title":"Test Disc","media_type":"movie"}`,
		NeedsReview:     true,
		ReviewReason:    "test reason",
	}
	manager := &Manager{}
	if err := manager.WriteMetadata(context.Background(), item, dir); err != nil {
		t.Fatalf("WriteMetadata failed: %v", err)
	}
	meta, ok, err := LoadMetadata(dir)
	if err != nil {
		t.Fatalf("LoadMetadata failed: %v", err)
	}
	if !ok {
		t.Fatal("expected metadata to exist")
	}
	if meta.DiscFingerprint != item.DiscFingerprint {
		t.Fatalf("fingerprint mismatch: got %q want %q", meta.DiscFingerprint, item.DiscFingerprint)
	}
	if meta.RipSpecData != item.RipSpecData {
		t.Fatalf("rip spec mismatch: got %q want %q", meta.RipSpecData, item.RipSpecData)
	}
	if meta.MetadataJSON != item.MetadataJSON {
		t.Fatalf("metadata json mismatch: got %q want %q", meta.MetadataJSON, item.MetadataJSON)
	}
	if !meta.NeedsReview || meta.ReviewReason != item.ReviewReason {
		t.Fatalf("review data mismatch: needs_review=%v reason=%q", meta.NeedsReview, meta.ReviewReason)
	}
}

func TestRipCacheMetadataMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, ok, err := LoadMetadata(dir)
	if err != nil {
		t.Fatalf("LoadMetadata failed: %v", err)
	}
	if ok {
		t.Fatal("expected metadata to be missing")
	}
}
