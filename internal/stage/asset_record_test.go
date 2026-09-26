package stage

import (
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func TestSessionAssetRecordHelpers(t *testing.T) {
	var nilSession *Session
	if nilSession.CompletedAssetJobs(ripspec.AssetKindRipped) != nil {
		t.Fatal("nil session returned completed jobs")
	}
	if jobs, skipped := nilSession.PendingKeyedAssetJobs(ripspec.AssetKindRipped, ripspec.AssetKindEncoded); jobs != nil || skipped != nil {
		t.Fatalf("nil session returned jobs: %v %v", jobs, skipped)
	}
	sess := &Session{Env: &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
		Assets:   ripspec.Assets{Ripped: []ripspec.Asset{{EpisodeKey: "main", Status: ripspec.AssetStatusCompleted, Path: "source.mkv"}}},
	}}
	if jobs := sess.CompletedAssetJobs(ripspec.AssetKindRipped); len(jobs) != 1 || jobs[0].Input.Path != "source.mkv" {
		t.Fatalf("completed jobs: %+v", jobs)
	}
	sess.RecordAssetFailure(ripspec.AssetKindEncoded, "main", "worker died")
	if jobs, skipped := sess.PendingKeyedAssetJobs(ripspec.AssetKindRipped, ripspec.AssetKindEncoded); len(jobs) != 1 || len(skipped) != 0 {
		t.Fatalf("failed output must be retryable: %+v %v", jobs, skipped)
	}
	sess.RecordAssetSuccess(ripspec.AssetKindEncoded, ripspec.Asset{EpisodeKey: "main", Path: "output.mkv"})
	if asset, found := sess.Env.Assets.FindAsset(ripspec.AssetKindEncoded, "main"); !found || !asset.IsCompleted() || asset.Path != "output.mkv" {
		t.Fatalf("recorded asset: %+v", asset)
	}
	if jobs, skipped := sess.PendingKeyedAssetJobs(ripspec.AssetKindRipped, ripspec.AssetKindEncoded); len(jobs) != 0 || len(skipped) != 1 || skipped[0] != "main" {
		t.Fatalf("completed output must be skipped: %+v %v", jobs, skipped)
	}
}
