package api

import (
	"context"
	"testing"

	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/testsupport"
)

func TestRetryFailedEpisodeNotFound(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)

	result, err := RetryFailedEpisode(context.Background(), store, 9999, "s01e01")
	if err != nil {
		t.Fatalf("RetryFailedEpisode error: %v", err)
	}
	if result.Outcome != RetryItemNotFound {
		t.Fatalf("expected not_found, got %v", result.Outcome)
	}
}

func TestRetryFailedEpisodeNotFailedStatus(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)
	item := testsupport.NewDisc(t, store, "Disc", "fp")

	result, err := RetryFailedEpisode(context.Background(), store, item.ID, "s01e01")
	if err != nil {
		t.Fatalf("RetryFailedEpisode error: %v", err)
	}
	if result.Outcome != RetryItemNotFailed {
		t.Fatalf("expected not_failed, got %v", result.Outcome)
	}
}

func TestRetryFailedEpisodeUpdatesItemAndClearsFailedAssets(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)
	item := testsupport.NewDisc(t, store, "Disc", "fp")

	env := ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s01e01", Season: 1, Episode: 1, TitleID: 1},
		},
		Assets: ripspec.Assets{
			Encoded: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/tmp/failed.mkv", Status: ripspec.AssetStatusFailed, ErrorMsg: "boom"},
			},
		},
	}
	encoded, err := env.Encode()
	if err != nil {
		t.Fatalf("env encode: %v", err)
	}

	item.Status = queue.StatusFailed
	item.RipSpecData = encoded
	item.ErrorMessage = "failed"
	item.NeedsReview = true
	item.ReviewReason = "reason"
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("store.Update: %v", err)
	}

	result, err := RetryFailedEpisode(context.Background(), store, item.ID, "S01E01")
	if err != nil {
		t.Fatalf("RetryFailedEpisode error: %v", err)
	}
	if result.Outcome != RetryItemUpdated {
		t.Fatalf("expected updated outcome, got %v", result.Outcome)
	}
	if result.NewStatus != string(queue.StatusEpisodeIdentified) {
		t.Fatalf("expected new status %s, got %s", queue.StatusEpisodeIdentified, result.NewStatus)
	}

	updated, err := store.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.Status != queue.StatusEpisodeIdentified {
		t.Fatalf("expected status %s, got %s", queue.StatusEpisodeIdentified, updated.Status)
	}
	if updated.ErrorMessage != "" || updated.ReviewReason != "" || updated.NeedsReview {
		t.Fatalf("expected review/error fields reset, got error=%q review=%q needsReview=%v", updated.ErrorMessage, updated.ReviewReason, updated.NeedsReview)
	}

	updatedEnv, err := ripspec.Parse(updated.RipSpecData)
	if err != nil {
		t.Fatalf("parse updated rip spec: %v", err)
	}
	asset, ok := updatedEnv.Assets.FindAsset(ripspec.AssetKindEncoded, "s01e01")
	if !ok {
		t.Fatal("expected encoded asset for s01e01")
	}
	if asset.Status != "" || asset.ErrorMsg != "" || asset.Path != "" {
		t.Fatalf("expected failed asset cleared, got status=%q error=%q path=%q", asset.Status, asset.ErrorMsg, asset.Path)
	}
}

func TestRetryFailedEpisodeMissingEpisode(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	store := testsupport.MustOpenStore(t, cfg)
	item := testsupport.NewDisc(t, store, "Disc", "fp")
	item.Status = queue.StatusFailed
	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("store.Update: %v", err)
	}

	result, err := RetryFailedEpisode(context.Background(), store, item.ID, "s01e09")
	if err != nil {
		t.Fatalf("RetryFailedEpisode error: %v", err)
	}
	if result.Outcome != RetryItemEpisodeNotFound {
		t.Fatalf("expected episode_not_found, got %v", result.Outcome)
	}
}
