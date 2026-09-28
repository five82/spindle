package stage

import (
	"fmt"

	"github.com/five82/spindle/internal/ripspec"
)

// AssetJob describes one per-asset unit of stage work. ProgressIndex is
// zero-based and ProgressTotal is the denominator to use for user-facing
// phase messages only. Position is never evidence of completed work;
// completion counts come from recorded assets.
type AssetJob struct {
	Key           string
	Input         ripspec.Asset
	ProgressIndex int
	ProgressTotal int
}

// Number returns the one-based job number for progress messages.
func (j AssetJob) Number() int { return j.ProgressIndex + 1 }

// PhaseMessage formats a user-visible stage progress message.
func (j AssetJob) PhaseMessage(action string) string {
	return fmt.Sprintf("Phase %d/%d - %s", j.Number(), j.ProgressTotal, action)
}

// CompletedAssetJobs returns one job for each envelope asset key whose
// asset at inputKind is completed, in envelope key order. This supports
// stages whose work set is exactly the completed artifacts of an earlier
// stage (apply consumes encoded assets, analysis consumes ripped assets).
func CompletedAssetJobs(env *ripspec.Envelope, inputKind string) []AssetJob {
	if env == nil {
		return nil
	}
	keys := env.AssetKeys()
	jobs := make([]AssetJob, 0, len(keys))
	for _, key := range keys {
		asset, found := env.Assets.FindAsset(inputKind, key)
		if !found || !asset.IsCompleted() {
			continue
		}
		jobs = append(jobs, AssetJob{
			Key:   key,
			Input: asset,
		})
	}
	for i := range jobs {
		jobs[i].ProgressIndex = i
		jobs[i].ProgressTotal = len(jobs)
	}
	return jobs
}

// PendingKeyedAssetJobs returns jobs for envelope asset keys whose input asset
// is completed and whose output asset is not already completed. It also returns
// keys skipped because the output already exists, so callers can log the
// stage-specific resume decision.
func PendingKeyedAssetJobs(env *ripspec.Envelope, inputKind, outputKind string) (jobs []AssetJob, skippedCompleted []string) {
	if env == nil {
		return nil, nil
	}
	keys := env.AssetKeys()
	jobs = make([]AssetJob, 0, len(keys))
	for i, key := range keys {
		if existing, found := env.Assets.FindAsset(outputKind, key); found && existing.IsCompleted() {
			skippedCompleted = append(skippedCompleted, key)
			continue
		}
		asset, found := env.Assets.FindAsset(inputKind, key)
		if !found || !asset.IsCompleted() {
			continue
		}
		jobs = append(jobs, AssetJob{
			Key:           key,
			Input:         asset,
			ProgressIndex: i,
			ProgressTotal: len(keys),
		})
	}
	return jobs, skippedCompleted
}

// CompletedAssetJobs returns one job for each completed asset in the session
// envelope at inputKind.
func (s *Session) CompletedAssetJobs(inputKind string) []AssetJob {
	if s == nil {
		return nil
	}
	return CompletedAssetJobs(s.Env, inputKind)
}

// PendingKeyedAssetJobs returns jobs for the session envelope using
// PendingKeyedAssetJobs.
func (s *Session) PendingKeyedAssetJobs(inputKind, outputKind string) ([]AssetJob, []string) {
	if s == nil {
		return nil, nil
	}
	return PendingKeyedAssetJobs(s.Env, inputKind, outputKind)
}

// RecordAssetSuccess appends or replaces a completed asset at kind.
func (s *Session) RecordAssetSuccess(kind string, asset ripspec.Asset) {
	asset.Status = ripspec.AssetStatusCompleted
	s.AddAsset(kind, asset)
}

// RecordAssetFailure appends or replaces a failed asset at kind.
func (s *Session) RecordAssetFailure(kind, key, errMsg string) {
	s.AddAsset(kind, ripspec.Asset{
		EpisodeKey: key,
		Status:     ripspec.AssetStatusFailed,
		ErrorMsg:   errMsg,
	})
}

// SaveAssetSuccess records a completed asset and persists it through a
// merge save, so concurrent stages of the same item cannot lose the write.
// The session's in-memory envelope adopts the merged state.
func (s *Session) SaveAssetSuccess(kind string, asset ripspec.Asset) error {
	return s.MergeSave(func(env *ripspec.Envelope) error {
		env.Assets.AddAsset(kind, asset)
		return nil
	})
}

// SaveAssetFailure records a failed asset and persists it through a merge
// save (see SaveAssetSuccess).
func (s *Session) SaveAssetFailure(kind, key, errMsg string) error {
	return s.MergeSave(func(env *ripspec.Envelope) error {
		env.Assets.AddAsset(kind, ripspec.Asset{
			EpisodeKey: key,
			Status:     ripspec.AssetStatusFailed,
			ErrorMsg:   errMsg,
		})
		return nil
	})
}
