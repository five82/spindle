package api

import (
	"context"

	"spindle/internal/queue"
)

// QueueActionService captures queue operations needed by per-item retry/stop workflows.
type QueueActionService interface {
	Describe(ctx context.Context, id int64) (*QueueItem, error)
	Retry(ctx context.Context, ids []int64) (int64, error)
	Stop(ctx context.Context, ids []int64) (int64, error)
}

type RetryItemOutcome string

const (
	RetryItemUpdated         RetryItemOutcome = "retried"
	RetryItemNotFound        RetryItemOutcome = "not_found"
	RetryItemNotFailed       RetryItemOutcome = "not_failed"
	RetryItemEpisodeNotFound RetryItemOutcome = "episode_not_found"
)

type RetryItemResult struct {
	ID        int64            `json:"id"`
	Outcome   RetryItemOutcome `json:"outcome"`
	NewStatus string           `json:"new_status,omitempty"`
}

type RetryItemsResult struct {
	UpdatedCount int64             `json:"updatedCount"`
	Items        []RetryItemResult `json:"items"`
}

type StopItemOutcome string

const (
	StopItemUpdated          StopItemOutcome = "stopped"
	StopItemNotFound         StopItemOutcome = "not_found"
	StopItemAlreadyCompleted StopItemOutcome = "already_completed"
	StopItemAlreadyFailed    StopItemOutcome = "already_failed"
)

type StopItemResult struct {
	ID          int64           `json:"id"`
	Outcome     StopItemOutcome `json:"outcome"`
	PriorStatus string          `json:"prior_status,omitempty"`
}

type StopItemsResult struct {
	UpdatedCount int64            `json:"updatedCount"`
	Items        []StopItemResult `json:"items"`
}

// RetryFailedItemsByID validates IDs and retries only failed items.
func RetryFailedItemsByID(ctx context.Context, service QueueActionService, ids []int64) (RetryItemsResult, error) {
	result := RetryItemsResult{Items: make([]RetryItemResult, 0, len(ids))}
	for _, id := range ids {
		item, err := service.Describe(ctx, id)
		if err != nil {
			return RetryItemsResult{}, err
		}
		if item == nil {
			result.Items = append(result.Items, RetryItemResult{ID: id, Outcome: RetryItemNotFound})
			continue
		}
		status, ok := queue.ParseStatus(item.Status)
		if !ok || status != queue.StatusFailed {
			result.Items = append(result.Items, RetryItemResult{ID: id, Outcome: RetryItemNotFailed})
			continue
		}
		updated, err := service.Retry(ctx, []int64{id})
		if err != nil {
			return RetryItemsResult{}, err
		}
		if updated > 0 {
			result.UpdatedCount += updated
			result.Items = append(result.Items, RetryItemResult{ID: id, Outcome: RetryItemUpdated})
			continue
		}
		result.Items = append(result.Items, RetryItemResult{ID: id, Outcome: RetryItemNotFailed})
	}
	return result, nil
}

// StopItemsByID validates IDs and stops items unless already terminal.
func StopItemsByID(ctx context.Context, service QueueActionService, ids []int64) (StopItemsResult, error) {
	result := StopItemsResult{Items: make([]StopItemResult, 0, len(ids))}
	for _, id := range ids {
		item, err := service.Describe(ctx, id)
		if err != nil {
			return StopItemsResult{}, err
		}
		if item == nil {
			result.Items = append(result.Items, StopItemResult{ID: id, Outcome: StopItemNotFound})
			continue
		}
		status := item.Status
		parsed, ok := queue.ParseStatus(status)
		if ok {
			switch parsed {
			case queue.StatusCompleted:
				result.Items = append(result.Items, StopItemResult{ID: id, Outcome: StopItemAlreadyCompleted, PriorStatus: status})
				continue
			case queue.StatusFailed:
				result.Items = append(result.Items, StopItemResult{ID: id, Outcome: StopItemAlreadyFailed, PriorStatus: status})
				continue
			}
		}

		updated, err := service.Stop(ctx, []int64{id})
		if err != nil {
			return StopItemsResult{}, err
		}
		if updated > 0 {
			result.UpdatedCount += updated
			result.Items = append(result.Items, StopItemResult{ID: id, Outcome: StopItemUpdated, PriorStatus: status})
			continue
		}
		result.Items = append(result.Items, StopItemResult{ID: id, Outcome: StopItemAlreadyFailed, PriorStatus: status})
	}
	return result, nil
}
