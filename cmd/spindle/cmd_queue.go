package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/queue"
)

func newQueueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Manage the processing queue",
	}
	cmd.AddCommand(
		newQueueListCmd(),
		newQueueShowCmd(),
		newQueueClearCmd(),
		newQueueRetryCmd(),
		newQueueStopCmd(),
	)
	return cmd
}

func newQueueListCmd() *cobra.Command {
	var stages []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List queue items",
		RunE: func(_ *cobra.Command, _ []string) error {
			acc, err := openQueueAccess()
			if err != nil {
				return err
			}
			items, err := acc.List()
			if err != nil {
				return err
			}

			// Filter by stage if specified.
			if len(stages) > 0 {
				stageSet := make(map[string]bool)
				for _, s := range stages {
					stageSet[strings.ToLower(s)] = true
				}
				var filtered []*queue.Item
				for _, item := range items {
					if stageSet[strings.ToLower(string(item.Stage))] {
						filtered = append(filtered, item)
					}
				}
				items = filtered
			}

			if len(items) == 0 {
				fmt.Println("No queue items")
				return nil
			}

			if flagVerbose {
				fmt.Printf("%-6s %-40s %-24s %-20s %-20s %s\n", "ID", "Title", "Stage", "Created", "Updated", "Fingerprint")
				fmt.Println(strings.Repeat("-", 140))
				for _, item := range items {
					fmt.Printf("%-6d %-40s %-24s %-20s %-20s %s\n",
						item.ID,
						item.DiscTitle,
						item.Stage,
						item.CreatedAt,
						item.UpdatedAt,
						item.DiscFingerprint,
					)
					if item.ProgressMessage != "" {
						fmt.Printf("       Progress: %s (%.0f%%)\n", item.ProgressMessage, item.ProgressPercent)
					}
					if item.ErrorMessage != "" {
						fmt.Printf("       Error: %s\n", item.ErrorMessage)
					}
				}
			} else {
				fmt.Printf("%-6s %-30s %-24s %-20s %-14s\n", "ID", "Title", "Stage", "Created", "Fingerprint")
				fmt.Println(strings.Repeat("-", 96))
				for _, item := range items {
					fp := item.DiscFingerprint
					if len(fp) > 12 {
						fp = fp[:12]
					}
					fmt.Printf("%-6d %-30s %-24s %-20s %-14s\n",
						item.ID,
						truncate(item.DiscTitle, 28),
						item.Stage,
						item.CreatedAt,
						fp,
					)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVarP(&stages, "stage", "s", nil, "Filter by queue stage (repeatable)")
	return cmd
}

func newQueueShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show detailed information for a queue item",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid item ID: %s", args[0])
			}

			acc, err := openQueueAccess()
			if err != nil {
				return err
			}
			item, err := acc.GetByID(id)
			if err != nil {
				return err
			}
			if item == nil {
				return fmt.Errorf("queue item %d not found", id)
			}

			if flagJSON {
				data, err := json.MarshalIndent(item, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
				return nil
			}

			fmt.Printf("ID:          %d\n", item.ID)
			fmt.Printf("Title:       %s\n", item.DiscTitle)
			fmt.Printf("Stage:       %s\n", item.Stage)
			if flagVerbose && item.FailedAtStage != "" {
				fmt.Printf("FailedAt:    %s\n", item.FailedAtStage)
			}
			fmt.Printf("Created:     %s\n", item.CreatedAt)
			fmt.Printf("Updated:     %s\n", item.UpdatedAt)
			fmt.Printf("Fingerprint: %s\n", item.DiscFingerprint)
			if item.ProgressMessage != "" {
				fmt.Printf("Progress:    %s (%.0f%%)\n", item.ProgressMessage, item.ProgressPercent)
			}
			if flagVerbose && item.ProgressTotalBytes > 0 {
				fmt.Printf("Bytes:       %s / %s\n",
					formatBytes(item.ProgressBytesCopied),
					formatBytes(item.ProgressTotalBytes))
			}
			if flagVerbose && item.ActiveEpisodeKey != "" {
				fmt.Printf("Episode:     %s\n", item.ActiveEpisodeKey)
			}
			if item.NeedsReview != 0 {
				fmt.Printf("Review:      %s\n", item.ReviewReason)
			}
			if item.ErrorMessage != "" {
				fmt.Printf("Error:       %s\n", item.ErrorMessage)
			}
			if item.MetadataJSON != "" {
				fmt.Printf("Metadata:    %s\n", item.MetadataJSON)
			}
			if flagVerbose && item.RipSpecData != "" {
				fmt.Printf("RipSpec:     %s\n", prettyJSON(item.RipSpecData))
			}
			if flagVerbose && item.EncodingDetailsJSON != "" {
				fmt.Printf("Encoding:    %s\n", prettyJSON(item.EncodingDetailsJSON))
			}
			return nil
		},
	}
}

func newQueueClearCmd() *cobra.Command {
	var flagAll, flagCompleted bool
	cmd := &cobra.Command{
		Use:   "clear [id...]",
		Short: "Remove queue items",
		RunE: func(_ *cobra.Command, args []string) error {
			if flagAll && flagCompleted {
				return fmt.Errorf("cannot combine --all and --completed")
			}
			if len(args) > 0 && (flagAll || flagCompleted) {
				return fmt.Errorf("cannot combine IDs with flags")
			}
			if len(args) == 0 && !flagAll && !flagCompleted {
				return fmt.Errorf("provide item IDs, --all, or --completed")
			}

			store, err := queue.Open(cfg.QueueDBPath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			if flagAll {
				if err := store.Clear(); err != nil {
					return err
				}
				fmt.Println("All queue items removed")
				return nil
			}
			if flagCompleted {
				if err := store.ClearCompleted(); err != nil {
					return err
				}
				fmt.Println("Completed queue items removed")
				return nil
			}

			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid item ID: %s", arg)
				}
				if err := store.Remove(id); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not remove item %d: %v\n", id, err)
				} else {
					fmt.Printf("Removed item %d\n", id)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&flagAll, "all", false, "Remove all items")
	cmd.Flags().BoolVar(&flagCompleted, "completed", false, "Remove only completed items")
	return cmd
}

func newQueueRetryCmd() *cobra.Command {
	var episode string
	cmd := &cobra.Command{
		Use:   "retry [id...]",
		Short: "Retry failed queue items",
		RunE: func(_ *cobra.Command, args []string) error {
			store, err := queue.Open(cfg.QueueDBPath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			if episode != "" && len(args) != 1 {
				return fmt.Errorf("--episode requires exactly one item ID")
			}

			if len(args) == 0 && episode == "" {
				// Retry all failed.
				if err := store.RetryFailed(); err != nil {
					return err
				}
				fmt.Println("All failed items retried")
				return nil
			}

			var ids []int64
			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid item ID: %s", arg)
				}
				ids = append(ids, id)
			}

			if err := store.RetryFailed(ids...); err != nil {
				return err
			}
			fmt.Printf("Retried %d item(s)\n", len(ids))
			return nil
		},
	}
	cmd.Flags().StringVarP(&episode, "episode", "e", "", "Retry only a specific episode (e.g., s01e05)")
	return cmd
}

func newQueueStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <id...>",
		Short: "Stop processing for specific queue items",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			store, err := queue.Open(cfg.QueueDBPath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			var ids []int64
			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid item ID: %s", arg)
				}
				ids = append(ids, id)
			}

			if err := store.StopItems(ids...); err != nil {
				return err
			}
			fmt.Printf("Stopped %d item(s)\n", len(ids))
			return nil
		},
	}
}
