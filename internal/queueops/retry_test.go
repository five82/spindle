package queueops

import (
	"testing"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

func openTestStore(t *testing.T) *queue.Store {
	t.Helper()
	store, err := queue.Open(":memory:")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestRetryEpisodeClearsFailedAssets(t *testing.T) {
	store := openTestStore(t)
	item, _ := store.NewDisc("Show", "fp1")

	env := ripspec.Envelope{
		Version:  ripspec.CurrentVersion,
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{{Key: "s01e01", Season: 1, Episode: 1}},
	}
	env.Assets.AddAsset(ripspec.AssetKindEncoded, ripspec.Asset{EpisodeKey: "s01e01", Path: "/bad.mkv", Status: ripspec.AssetStatusFailed, ErrorMsg: "encode failed"})
	raw, err := env.Encode()
	if err != nil {
		t.Fatalf("encode ripspec: %v", err)
	}

	item.Stage = queue.StageFailed
	item.FailedAtStage = string(queue.StageEncoding)
	item.ErrorMessage = "encode failed"
	item.NeedsReview = 1
	item.ReviewReason = `["problem"]`
	item.RipSpecData = raw
	if err := store.Update(item); err != nil {
		t.Fatalf("update failed item: %v", err)
	}

	result, err := RetryEpisode(store, item.ID, "S01E01")
	if err != nil {
		t.Fatalf("retry episode: %v", err)
	}
	if result != RetryResultRetried {
		t.Fatalf("result = %q, want %q", result, RetryResultRetried)
	}

	got, _ := store.GetByID(item.ID)
	if got.Stage != queue.StageEncoding {
		t.Fatalf("stage = %q, want %q", got.Stage, queue.StageEncoding)
	}
	if got.ErrorMessage != "" || got.NeedsReview != 0 || got.ReviewReason != "" {
		t.Fatalf("failure/review fields not cleared: error=%q needs_review=%d reason=%q", got.ErrorMessage, got.NeedsReview, got.ReviewReason)
	}

	gotEnv, err := ripspec.Parse(got.RipSpecData)
	if err != nil {
		t.Fatalf("parse updated ripspec: %v", err)
	}
	asset, ok := gotEnv.Assets.FindAsset(ripspec.AssetKindEncoded, "s01e01")
	if !ok {
		t.Fatal("encoded asset missing")
	}
	if asset.Status != "" || asset.Path != "" || asset.ErrorMsg != "" {
		t.Fatalf("asset not cleared: %+v", asset)
	}
}
