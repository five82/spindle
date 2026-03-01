package main

import (
	"context"
)

// removeIDs removes items by ID, using Remove's return value to distinguish
// found vs not-found (no pre-check needed).
func removeIDs(ctx context.Context, api queueAPI, ids []int64) (queueRemoveResult, error) {
	result := queueRemoveResult{
		Items: make([]queueRemoveItemResult, 0, len(ids)),
	}

	for _, id := range ids {
		removed, err := api.Remove(ctx, []int64{id})
		if err != nil {
			return queueRemoveResult{}, err
		}
		if removed > 0 {
			result.RemovedCount += removed
			result.Items = append(result.Items, queueRemoveItemResult{ID: id, Outcome: queueRemoveOutcomeRemoved})
		} else {
			result.Items = append(result.Items, queueRemoveItemResult{ID: id, Outcome: queueRemoveOutcomeNotFound})
		}
	}

	return result, nil
}

func retryOutcomeString(o queueRetryOutcome) string {
	switch o {
	case queueRetryOutcomeUpdated:
		return "retried"
	case queueRetryOutcomeNotFound:
		return "not_found"
	case queueRetryOutcomeNotFailed:
		return "not_failed"
	case queueRetryOutcomeEpisodeNotFound:
		return "episode_not_found"
	default:
		return "unknown"
	}
}
