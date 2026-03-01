package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"spindle/internal/api"
)

func parsePositiveIDs(args []string) ([]int64, error) {
	ids := make([]int64, 0, len(args))
	for _, arg := range args {
		id, err := strconv.ParseInt(strings.TrimSpace(arg), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid item id %q", arg)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func writeQueueRemoveResultJSON(cmd *cobra.Command, result api.RemoveItemsResult) error {
	type jsonItem struct {
		ID      int64  `json:"id"`
		Outcome string `json:"outcome"`
	}
	items := make([]jsonItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, jsonItem{ID: item.ID, Outcome: string(item.Outcome)})
	}
	return writeJSON(cmd, map[string]any{"items": items})
}

func printQueueRemoveResult(out io.Writer, result api.RemoveItemsResult) {
	for _, item := range result.Items {
		switch item.Outcome {
		case api.RemoveItemNotFound:
			fmt.Fprintf(out, "Item %d not found\n", item.ID)
		case api.RemoveItemRemoved:
			fmt.Fprintf(out, "Item %d removed\n", item.ID)
		}
	}
}

func bulkClearLabel(all, completed, failed bool) string {
	switch {
	case completed:
		return "completed items"
	case failed:
		return "failed items"
	case all:
		return "queue items"
	default:
		return "queue items"
	}
}

func writeQueueRetryResultJSON(cmd *cobra.Command, result api.RetryItemsResult) error {
	type jsonItem struct {
		ID      int64  `json:"id"`
		Outcome string `json:"outcome"`
	}
	items := make([]jsonItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, jsonItem{ID: item.ID, Outcome: string(item.Outcome)})
	}
	return writeJSON(cmd, map[string]any{"items": items})
}

func printQueueRetryResult(out io.Writer, result api.RetryItemsResult) {
	for _, item := range result.Items {
		switch item.Outcome {
		case api.RetryItemNotFound:
			fmt.Fprintf(out, "Item %d not found\n", item.ID)
		case api.RetryItemNotFailed:
			fmt.Fprintf(out, "Item %d is not in a retryable state (only failed items can be retried)\n", item.ID)
		case api.RetryItemUpdated:
			fmt.Fprintf(out, "Item %d reset for retry\n", item.ID)
		}
	}
}

func writeQueueEpisodeRetryJSON(cmd *cobra.Command, id int64, episodeKey string, result api.RetryItemResult) error {
	return writeJSON(cmd, map[string]any{
		"id":         id,
		"episode":    episodeKey,
		"outcome":    string(result.Outcome),
		"new_status": result.NewStatus,
	})
}

func printQueueEpisodeRetryResult(out io.Writer, id int64, episodeKey string, result api.RetryItemResult) {
	switch result.Outcome {
	case api.RetryItemNotFound:
		fmt.Fprintf(out, "Item %d not found\n", id)
	case api.RetryItemNotFailed:
		fmt.Fprintf(out, "Item %d is not in a retryable state\n", id)
	case api.RetryItemEpisodeNotFound:
		fmt.Fprintf(out, "Episode %s not found in item %d\n", episodeKey, id)
	case api.RetryItemUpdated:
		fmt.Fprintf(out, "Episode %s in item %d cleared for retry (item reset to %s)\n",
			strings.ToUpper(episodeKey), id, result.NewStatus)
	}
}

func writeQueueStopResultJSON(cmd *cobra.Command, result api.StopItemsResult) error {
	type jsonItem struct {
		ID            int64  `json:"id"`
		Outcome       string `json:"outcome"`
		PriorStatus   string `json:"prior_status,omitempty"`
		WasProcessing bool   `json:"was_processing,omitempty"`
	}
	items := make([]jsonItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, jsonItem{
			ID:            item.ID,
			Outcome:       string(item.Outcome),
			PriorStatus:   item.PriorStatus,
			WasProcessing: item.WasProcessing,
		})
	}
	return writeJSON(cmd, map[string]any{"items": items})
}

func printQueueStopResult(out io.Writer, result api.StopItemsResult) {
	for _, item := range result.Items {
		switch item.Outcome {
		case api.StopItemNotFound:
			fmt.Fprintf(out, "Item %d not found\n", item.ID)
		case api.StopItemAlreadyCompleted:
			fmt.Fprintf(out, "Item %d is already completed\n", item.ID)
		case api.StopItemAlreadyFailed:
			fmt.Fprintf(out, "Item %d is already failed\n", item.ID)
		case api.StopItemUpdated:
			message := fmt.Sprintf("Item %d stop requested", item.ID)
			if item.WasProcessing {
				statusLabel := formatStatusLabel(item.PriorStatus)
				message = fmt.Sprintf("Item %d stop requested (currently %s; will halt after current stage)", item.ID, statusLabel)
			}
			fmt.Fprintln(out, message)
		}
	}
}
