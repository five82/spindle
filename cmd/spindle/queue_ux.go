package main

import (
	"context"

	"spindle/internal/queue"
)

// retryIDs validates each ID and retries eligible items.
// Works identically for IPC and direct-store paths.
func retryIDs(ctx context.Context, api queueAPI, ids []int64) (queueRetryResult, error) {
	result := queueRetryResult{
		Items: make([]queueRetryItemResult, 0, len(ids)),
	}

	for _, id := range ids {
		item, err := api.Describe(ctx, id)
		if err != nil {
			return queueRetryResult{}, err
		}
		if item == nil {
			result.Items = append(result.Items, queueRetryItemResult{ID: id, Outcome: queueRetryOutcomeNotFound})
			continue
		}
		if !statusIsRetryable(item.Status) {
			result.Items = append(result.Items, queueRetryItemResult{ID: id, Outcome: queueRetryOutcomeNotFailed})
			continue
		}

		updated, err := api.Retry(ctx, []int64{id})
		if err != nil {
			return queueRetryResult{}, err
		}
		if updated > 0 {
			result.UpdatedCount += updated
			result.Items = append(result.Items, queueRetryItemResult{ID: id, Outcome: queueRetryOutcomeUpdated})
			continue
		}

		result.Items = append(result.Items, queueRetryItemResult{ID: id, Outcome: queueRetryOutcomeNotFailed})
	}

	return result, nil
}

// stopIDs validates each ID and stops eligible items.
// Works identically for IPC and direct-store paths.
func stopIDs(ctx context.Context, api queueAPI, ids []int64) (queueStopResult, error) {
	result := queueStopResult{
		Items: make([]queueStopItemResult, 0, len(ids)),
	}

	for _, id := range ids {
		item, err := api.Describe(ctx, id)
		if err != nil {
			return queueStopResult{}, err
		}
		if item == nil {
			result.Items = append(result.Items, queueStopItemResult{ID: id, Outcome: queueStopOutcomeNotFound})
			continue
		}
		status := item.Status
		parsed, ok := queue.ParseStatus(status)
		if ok {
			switch parsed {
			case queue.StatusCompleted:
				result.Items = append(result.Items, queueStopItemResult{ID: id, Outcome: queueStopOutcomeAlreadyCompleted, PriorStatus: status})
				continue
			case queue.StatusFailed:
				result.Items = append(result.Items, queueStopItemResult{ID: id, Outcome: queueStopOutcomeAlreadyFailed, PriorStatus: status})
				continue
			}
		}

		updated, err := api.Stop(ctx, []int64{id})
		if err != nil {
			return queueStopResult{}, err
		}
		if updated > 0 {
			result.UpdatedCount += updated
			result.Items = append(result.Items, queueStopItemResult{ID: id, Outcome: queueStopOutcomeUpdated, PriorStatus: status})
			continue
		}
		result.Items = append(result.Items, queueStopItemResult{ID: id, Outcome: queueStopOutcomeAlreadyFailed, PriorStatus: status})
	}

	return result, nil
}

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

func statusIsRetryable(value string) bool {
	status, ok := queue.ParseStatus(value)
	return ok && status == queue.StatusFailed
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

func stopOutcomeString(o queueStopOutcome) string {
	switch o {
	case queueStopOutcomeUpdated:
		return "stopped"
	case queueStopOutcomeNotFound:
		return "not_found"
	case queueStopOutcomeAlreadyCompleted:
		return "already_completed"
	case queueStopOutcomeAlreadyFailed:
		return "already_failed"
	default:
		return "unknown"
	}
}
