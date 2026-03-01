package api

import "context"

// QueueRemoveService captures queue operations needed by per-item remove workflows.
type QueueRemoveService interface {
	Remove(ctx context.Context, ids []int64) (int64, error)
}

type RemoveItemOutcome string

const (
	RemoveItemRemoved  RemoveItemOutcome = "removed"
	RemoveItemNotFound RemoveItemOutcome = "not_found"
)

type RemoveItemResult struct {
	ID      int64             `json:"id"`
	Outcome RemoveItemOutcome `json:"outcome"`
}

type RemoveItemsResult struct {
	RemovedCount int64              `json:"removedCount"`
	Items        []RemoveItemResult `json:"items"`
}

// RemoveItemsByID removes queue items one-by-one so each ID can report removed/not_found.
func RemoveItemsByID(ctx context.Context, service QueueRemoveService, ids []int64) (RemoveItemsResult, error) {
	result := RemoveItemsResult{Items: make([]RemoveItemResult, 0, len(ids))}
	for _, id := range ids {
		removed, err := service.Remove(ctx, []int64{id})
		if err != nil {
			return RemoveItemsResult{}, err
		}
		if removed > 0 {
			result.RemovedCount += removed
			result.Items = append(result.Items, RemoveItemResult{ID: id, Outcome: RemoveItemRemoved})
			continue
		}
		result.Items = append(result.Items, RemoveItemResult{ID: id, Outcome: RemoveItemNotFound})
	}
	return result, nil
}
