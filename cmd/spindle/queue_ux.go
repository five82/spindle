package main

import (
	"context"

	"spindle/internal/queueaccess"
)

// removeIDs removes items by ID, using Remove's return value to distinguish
// found vs not-found (no pre-check needed).
func removeIDs(ctx context.Context, qa queueaccess.Access, ids []int64) (queueRemoveResult, error) {
	result := queueRemoveResult{
		Items: make([]queueRemoveItemResult, 0, len(ids)),
	}

	for _, id := range ids {
		removed, err := qa.Remove(ctx, []int64{id})
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
