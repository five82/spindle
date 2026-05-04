package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/daemonctl"
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

			var queueStages []queue.Stage
			for _, s := range stages {
				queueStages = append(queueStages, queue.Stage(strings.ToLower(s)))
			}
			items, err := acc.List(queueStages...)
			if err != nil {
				return err
			}

			if len(items) == 0 {
				fmt.Println("No queue items")
				return nil
			}

			if flagVerbose {
				fmt.Println(labelStyle(fmt.Sprintf("%-6s %-40s %-24s %-20s %-20s %s", "ID", "Title", "Stage", "Created", "Updated", "Fingerprint")))
				fmt.Println(dimStyle(strings.Repeat("-", 140)))
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
						fmt.Printf("       %s %s (%.0f%%)\n", labelStyle("Progress:"), item.ProgressMessage, item.ProgressPercent)
					}
					if item.ErrorMessage != "" {
						fmt.Printf("       %s %s\n", failStyle("Error:"), item.ErrorMessage)
					}
				}
			} else {
				fmt.Println(labelStyle(fmt.Sprintf("%-6s %-30s %-24s %-20s %-14s", "ID", "Title", "Stage", "Created", "Fingerprint")))
				fmt.Println(dimStyle(strings.Repeat("-", 96)))
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

			fmt.Printf("%s %d\n", labelStyle("ID:         "), item.ID)
			fmt.Printf("%s %s\n", labelStyle("Title:      "), item.DiscTitle)
			fmt.Printf("%s %s\n", labelStyle("Stage:      "), item.Stage)
			if flagVerbose && item.FailedAtStage != "" {
				fmt.Printf("%s %s\n", labelStyle("FailedAt:   "), item.FailedAtStage)
			}
			fmt.Printf("%s %s\n", labelStyle("Created:    "), item.CreatedAt)
			fmt.Printf("%s %s\n", labelStyle("Updated:    "), item.UpdatedAt)
			fmt.Printf("%s %s\n", labelStyle("Fingerprint:"), item.DiscFingerprint)
			if item.ProgressMessage != "" {
				fmt.Printf("%s %s (%.0f%%)\n", labelStyle("Progress:   "), item.ProgressMessage, item.ProgressPercent)
			}
			if flagVerbose && item.ProgressTotalBytes > 0 {
				fmt.Printf("%s %s / %s\n", labelStyle("Bytes:      "),
					formatBytes(item.ProgressBytesCopied),
					formatBytes(item.ProgressTotalBytes))
			}
			if flagVerbose && item.ActiveEpisodeKey != "" {
				fmt.Printf("%s %s\n", labelStyle("Episode:    "), item.ActiveEpisodeKey)
			}
			if item.NeedsReview != 0 {
				fmt.Printf("%s %s\n", labelStyle("Review:     "), item.ReviewReason)
			}
			if item.ErrorMessage != "" {
				fmt.Printf("%s %s\n", failStyle("Error:      "), item.ErrorMessage)
			}
			if item.MetadataJSON != "" {
				fmt.Printf("%s %s\n", labelStyle("Metadata:   "), item.MetadataJSON)
			}
			if flagVerbose && item.RipSpecData != "" {
				fmt.Printf("%s %s\n", labelStyle("RipSpec:    "), prettyJSON(item.RipSpecData))
			}
			if flagVerbose && item.EncodingDetailsJSON != "" {
				fmt.Printf("%s %s\n", labelStyle("Encoding:   "), prettyJSON(item.EncodingDetailsJSON))
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

			if flagAll && !daemonctl.IsRunning(lockPath(), socketPath()) {
				if err := clearQueueDBFiles(cfg.QueueDBPath()); err != nil {
					return err
				}
				fmt.Println(successStyle("Queue database files removed"))
				return nil
			}

			acc, err := openQueueAccess()
			if err != nil {
				return err
			}

			if flagAll {
				if _, err := acc.Clear("all"); err != nil {
					return err
				}
				fmt.Println(successStyle("All queue items removed"))
				return nil
			}
			if flagCompleted {
				if _, err := acc.Clear("completed"); err != nil {
					return err
				}
				fmt.Println(successStyle("Completed queue items removed"))
				return nil
			}

			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid item ID: %s", arg)
				}
				if _, err := acc.Remove(id); err != nil {
					fmt.Fprintf(os.Stderr, "%s could not remove item %d: %v\n", warnStyle("Warning:"), id, err)
				} else {
					fmt.Println(successStyle(fmt.Sprintf("Removed item %d", id)))
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
			if episode != "" && len(args) != 1 {
				return fmt.Errorf("--episode requires exactly one item ID")
			}

			acc, err := openQueueAccess()
			if err != nil {
				return err
			}

			if episode != "" {
				id, err := strconv.ParseInt(args[0], 10, 64)
				if err != nil {
					return fmt.Errorf("invalid item ID: %s", args[0])
				}
				result, err := acc.RetryEpisode(id, episode)
				if err != nil {
					return err
				}
				switch result {
				case "retried":
					fmt.Println(successStyle(fmt.Sprintf("Retried episode %s on item %d", episode, id)))
				case "not_found":
					return fmt.Errorf("item %d not found", id)
				case "not_failed":
					return fmt.Errorf("item %d is not in failed state", id)
				case "episode_not_found":
					return fmt.Errorf("episode %s not found in item %d", episode, id)
				default:
					return fmt.Errorf("unexpected retry result: %s", result)
				}
				return nil
			}

			if len(args) == 0 {
				// Retry all failed.
				count, err := acc.Retry()
				if err != nil {
					return err
				}
				fmt.Println(successStyle(fmt.Sprintf("Retried %d failed item(s)", count)))
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

			count, err := acc.Retry(ids...)
			if err != nil {
				return err
			}
			if count == 0 {
				return fmt.Errorf("no failed items were retried")
			}
			fmt.Println(successStyle(fmt.Sprintf("Retried %d failed item(s)", count)))
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
			acc, err := openQueueAccess()
			if err != nil {
				return err
			}

			var ids []int64
			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid item ID: %s", arg)
				}
				ids = append(ids, id)
			}

			if _, err := acc.Stop(ids...); err != nil {
				return err
			}
			fmt.Println(successStyle(fmt.Sprintf("Stopped %d item(s)", len(ids))))
			return nil
		},
	}
}

func clearQueueDBFiles(dbPath string) error {
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	return nil
}
