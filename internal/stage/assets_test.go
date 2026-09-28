package stage

import (
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func TestCompletedAssetJobsPreservesEnvelopeKeyOrder(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01e01"},
			{Key: "s01e02"},
			{Key: "s01e03"},
		},
		// Assets recorded out of episode order: jobs must still follow the
		// envelope key order, not completion order.
		Assets: ripspec.Assets{Ripped: []ripspec.Asset{
			{EpisodeKey: "s01e03", Path: "/rip/3.mkv", Status: ripspec.AssetStatusCompleted},
			{EpisodeKey: "s01e02", Path: "", Status: ripspec.AssetStatusFailed},
			{EpisodeKey: "s01e01", Path: "/rip/1.mkv", Status: ripspec.AssetStatusCompleted},
		}},
	}

	jobs := CompletedAssetJobs(env, ripspec.AssetKindRipped)
	if len(jobs) != 2 {
		t.Fatalf("len(jobs) = %d, want 2", len(jobs))
	}
	if jobs[0].Key != "s01e01" || jobs[0].ProgressIndex != 0 || jobs[0].ProgressTotal != 2 {
		t.Fatalf("job[0] = %+v", jobs[0])
	}
	if jobs[1].Key != "s01e03" || jobs[1].ProgressIndex != 1 || jobs[1].ProgressTotal != 2 {
		t.Fatalf("job[1] = %+v", jobs[1])
	}
}

func TestPendingKeyedAssetJobsSkipsCompletedOutputAndMissingInput(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01e01"},
			{Key: "s01e02"},
			{Key: "s01e03"},
		},
		Assets: ripspec.Assets{
			Encoded: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/enc/1.mkv", Status: ripspec.AssetStatusCompleted},
				{EpisodeKey: "s01e03", Path: "/enc/3.mkv", Status: ripspec.AssetStatusCompleted},
			},
			Subtitled: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/sub/1.mkv", Status: ripspec.AssetStatusCompleted},
			},
		},
	}

	jobs, skipped := PendingKeyedAssetJobs(env, ripspec.AssetKindEncoded, ripspec.AssetKindSubtitled)
	if len(skipped) != 1 || skipped[0] != "s01e01" {
		t.Fatalf("skipped = %#v, want [s01e01]", skipped)
	}
	if len(jobs) != 1 {
		t.Fatalf("len(jobs) = %d, want 1", len(jobs))
	}
	if jobs[0].Key != "s01e03" || jobs[0].ProgressIndex != 2 || jobs[0].ProgressTotal != 3 {
		t.Fatalf("job = %+v", jobs[0])
	}
}

func TestAssetJobProgressHelpers(t *testing.T) {
	job := AssetJob{ProgressIndex: 1, ProgressTotal: 4}
	if got := job.Number(); got != 2 {
		t.Fatalf("Number() = %d, want 2", got)
	}
	if got := job.PhaseMessage("Encoding title00.mkv"); got != "Phase 2/4 - Encoding title00.mkv" {
		t.Fatalf("PhaseMessage() = %q", got)
	}
}
