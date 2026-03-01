package api

import (
	"context"
	"fmt"
	"strings"

	"spindle/internal/queue"
	"spindle/internal/ripspec"
)

type RetryEpisodeOutcome int

const (
	RetryEpisodeUpdated RetryEpisodeOutcome = iota
	RetryEpisodeNotFound
	RetryEpisodeNotFailed
	RetryEpisodeEpisodeNotFound
)

type RetryEpisodeResult struct {
	ItemID     int64
	Outcome    RetryEpisodeOutcome
	NewStatus  string
	EpisodeKey string
}

// RetryFailedEpisode clears per-episode failed assets and resets the queue item
// to the appropriate status for re-processing.
func RetryFailedEpisode(ctx context.Context, store *queue.Store, itemID int64, episodeKey string) (RetryEpisodeResult, error) {
	if store == nil {
		return RetryEpisodeResult{}, fmt.Errorf("queue store is required")
	}

	item, err := store.GetByID(ctx, itemID)
	if err != nil {
		return RetryEpisodeResult{}, err
	}
	if item == nil {
		return RetryEpisodeResult{ItemID: itemID, Outcome: RetryEpisodeNotFound}, nil
	}

	if item.Status != queue.StatusFailed {
		return RetryEpisodeResult{ItemID: itemID, Outcome: RetryEpisodeNotFailed}, nil
	}

	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		return RetryEpisodeResult{}, fmt.Errorf("parse rip spec: %w", err)
	}

	episodeKey = strings.ToLower(strings.TrimSpace(episodeKey))
	if episodeKey == "" {
		return RetryEpisodeResult{ItemID: itemID, Outcome: RetryEpisodeEpisodeNotFound}, nil
	}

	targetStatus := determineEpisodeRetryStatus(&env, episodeKey)
	if targetStatus == "" {
		return RetryEpisodeResult{ItemID: itemID, Outcome: RetryEpisodeEpisodeNotFound}, nil
	}

	env.Assets.ClearFailedAsset(ripspec.AssetKindEncoded, episodeKey)
	env.Assets.ClearFailedAsset(ripspec.AssetKindSubtitled, episodeKey)
	env.Assets.ClearFailedAsset(ripspec.AssetKindFinal, episodeKey)

	encoded, err := env.Encode()
	if err != nil {
		return RetryEpisodeResult{}, fmt.Errorf("encode rip spec: %w", err)
	}

	item.RipSpecData = encoded
	item.Status = targetStatus
	item.ErrorMessage = ""
	item.NeedsReview = false
	item.ReviewReason = ""

	if err := store.Update(ctx, item); err != nil {
		return RetryEpisodeResult{}, fmt.Errorf("update item: %w", err)
	}

	return RetryEpisodeResult{
		ItemID:     itemID,
		Outcome:    RetryEpisodeUpdated,
		NewStatus:  string(targetStatus),
		EpisodeKey: episodeKey,
	}, nil
}

func determineEpisodeRetryStatus(env *ripspec.Envelope, episodeKey string) queue.Status {
	if env == nil {
		return ""
	}
	if env.EpisodeByKey(episodeKey) == nil {
		return ""
	}

	if asset, ok := env.Assets.FindAsset(ripspec.AssetKindFinal, episodeKey); ok && asset.IsFailed() {
		return queue.StatusEncoded
	}
	if asset, ok := env.Assets.FindAsset(ripspec.AssetKindSubtitled, episodeKey); ok && asset.IsFailed() {
		return queue.StatusEncoded
	}
	if asset, ok := env.Assets.FindAsset(ripspec.AssetKindEncoded, episodeKey); ok && asset.IsFailed() {
		if len(env.Episodes) > 0 {
			return queue.StatusEpisodeIdentified
		}
		return queue.StatusRipped
	}

	return queue.StatusRipped
}
