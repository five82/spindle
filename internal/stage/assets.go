package stage

import (
	"fmt"

	"github.com/five82/spindle/internal/ripspec"
)

// AssetJob describes one per-asset unit of stage work. ProgressIndex is
// zero-based and ProgressTotal is the denominator to use for user-facing
// phase/progress messages. Different planners may choose a filtered job list or
// the full envelope key list as the progress denominator to preserve the stage's
// existing progress semantics.
type AssetJob struct {
	Key           string
	Input         ripspec.Asset
	ProgressIndex int
	ProgressTotal int
}

// Number returns the one-based job number for progress messages.
func (j AssetJob) Number() int { return j.ProgressIndex + 1 }

// Percent converts a per-job percent into total stage progress for this job.
func (j AssetJob) Percent(currentJobPercent float64) float64 {
	return OverallPercent(j.ProgressIndex, j.ProgressTotal, currentJobPercent)
}

// CompletionPercent returns total stage progress after this job is complete.
func (j AssetJob) CompletionPercent() float64 {
	return OverallPercent(j.ProgressIndex+1, j.ProgressTotal, 0)
}

// PhaseMessage formats a user-visible stage progress message.
func (j AssetJob) PhaseMessage(action string) string {
	return fmt.Sprintf("Phase %d/%d - %s", j.Number(), j.ProgressTotal, action)
}

// CompletedAssetJobs returns one job for each completed asset at inputKind.
// Jobs preserve the asset slice order. This supports stages such as encoding
// whose work set is exactly the artifacts produced by the previous stage.
func CompletedAssetJobs(env *ripspec.Envelope, inputKind string) []AssetJob {
	if env == nil {
		return nil
	}
	assets := assetsForKind(env, inputKind)
	jobs := make([]AssetJob, 0, len(assets))
	for _, asset := range assets {
		if !asset.IsCompleted() {
			continue
		}
		jobs = append(jobs, AssetJob{
			Key:   asset.EpisodeKey,
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

// OverallPercent converts per-item progress into total stage progress for a
// fixed-size job list.
func OverallPercent(completedItems, totalItems int, currentItemPercent float64) float64 {
	if totalItems <= 0 {
		return 0
	}
	if completedItems < 0 {
		completedItems = 0
	}
	if completedItems > totalItems {
		completedItems = totalItems
	}
	if currentItemPercent < 0 {
		currentItemPercent = 0
	}
	if currentItemPercent > 100 {
		currentItemPercent = 100
	}
	progress := float64(completedItems) + (currentItemPercent / 100)
	if progress > float64(totalItems) {
		progress = float64(totalItems)
	}
	return progress / float64(totalItems) * 100
}

func assetsForKind(env *ripspec.Envelope, kind string) []ripspec.Asset {
	if env == nil {
		return nil
	}
	switch kind {
	case ripspec.AssetKindRipped:
		return env.Assets.Ripped
	case ripspec.AssetKindEncoded:
		return env.Assets.Encoded
	case ripspec.AssetKindSubtitled:
		return env.Assets.Subtitled
	case ripspec.AssetKindFinal:
		return env.Assets.Final
	default:
		return nil
	}
}
