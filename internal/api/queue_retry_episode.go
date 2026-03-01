package api

import (
	"context"
	"fmt"
	"strings"

	"spindle/internal/queue"
	"spindle/internal/ripspec"
)

// RetryFailedEpisode clears per-episode failed assets and resets the queue item
// to the appropriate status for re-processing.
func RetryFailedEpisode(ctx context.Context, store *queue.Store, itemID int64, episodeKey string) (RetryItemResult, error) {
	if store == nil {
		return RetryItemResult{}, fmt.Errorf("queue store is required")
	}

	item, err := store.GetByID(ctx, itemID)
	if err != nil {
		return RetryItemResult{}, err
	}
	if item == nil {
		return RetryItemResult{ID: itemID, Outcome: RetryItemNotFound}, nil
	}

	if item.Status != queue.StatusFailed {
		return RetryItemResult{ID: itemID, Outcome: RetryItemNotFailed}, nil
	}

	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		return RetryItemResult{}, fmt.Errorf("parse rip spec: %w", err)
	}

	episodeKey = strings.ToLower(strings.TrimSpace(episodeKey))
	if episodeKey == "" {
		return RetryItemResult{ID: itemID, Outcome: RetryItemEpisodeNotFound}, nil
	}

	targetStatus := determineEpisodeRetryStatus(&env, episodeKey)
	if targetStatus == "" {
		return RetryItemResult{ID: itemID, Outcome: RetryItemEpisodeNotFound}, nil
	}

	env.Assets.ClearFailedAsset(ripspec.AssetKindEncoded, episodeKey)
	env.Assets.ClearFailedAsset(ripspec.AssetKindSubtitled, episodeKey)
	env.Assets.ClearFailedAsset(ripspec.AssetKindFinal, episodeKey)

	encoded, err := env.Encode()
	if err != nil {
		return RetryItemResult{}, fmt.Errorf("encode rip spec: %w", err)
	}

	item.RipSpecData = encoded
	item.Status = targetStatus
	item.ErrorMessage = ""
	item.NeedsReview = false
	item.ReviewReason = ""

	if err := store.Update(ctx, item); err != nil {
		return RetryItemResult{}, fmt.Errorf("update item: %w", err)
	}

	return RetryItemResult{
		ID:        itemID,
		Outcome:   RetryItemUpdated,
		NewStatus: string(targetStatus),
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
