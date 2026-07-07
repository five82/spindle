package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/daemonctl"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/queueops"
)

// printTaskLines renders per-task status lines: running tasks show percent
// and message (bytes and active asset key in verbose mode), failed tasks
// show their error. Progress now lives per task (the scheduler's tasks
// table), so what used to be a single item-level "Progress: X% message"
// line is one line per running task.
func printTaskLines(indent string, tasks []httpapi.TaskResponse, verbose bool) {
	for _, t := range tasks {
		switch queue.TaskState(t.State) {
		case queue.TaskRunning:
			fmt.Printf("%s%s %s (%.0f%%)\n", indent, labelStyle(fmt.Sprintf("Progress (%s):", t.Type)), t.Progress.Message, t.Progress.Percent)
			if verbose && t.Progress.TotalBytes > 0 {
				fmt.Printf("%s  %s %s / %s\n", indent, labelStyle("Bytes:"), formatBytes(t.Progress.BytesCopied), formatBytes(t.Progress.TotalBytes))
			}
			if verbose && t.ActiveAssetKey != "" {
				fmt.Printf("%s  %s %s\n", indent, labelStyle("Asset:"), t.ActiveAssetKey)
			}
		case queue.TaskFailed:
			fmt.Printf("%s%s %s: %s\n", indent, failStyle("Failed:"), t.Type, t.Error)
		}
	}
}

func newQueueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "queue",
		Short:   "Manage the processing queue",
		GroupID: groupQueue,
	}
	cmd.AddCommand(
		newQueueListCmd(),
		newQueueShowCmd(),
		newQueueClearCmd(),
		newQueueRetryCmd(),
		newQueueCancelCmd(),
		newQueueAuditCmd(),
	)
	return cmd
}

func parseQueueID(arg string) (int64, error) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid item ID: %s", arg)
	}
	return id, nil
}

func parseQueueIDs(args []string) ([]int64, error) {
	ids := make([]int64, 0, len(args))
	for _, arg := range args {
		id, err := parseQueueID(arg)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func newQueueListCmd() *cobra.Command {
	var stages []string
	var asJSON bool
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

			if asJSON {
				return printJSON(items)
			}

			if len(items) == 0 {
				fmt.Println("No queue items")
				return nil
			}

			if flagVerbose {
				fmt.Println(labelStyle(fmt.Sprintf("%-6s %-40s %-24s %-30s %-30s %s", "ID", "Title", "Stage", "Created", "Updated", "Fingerprint")))
				fmt.Println(dimStyle(strings.Repeat("-", 160)))
				for _, item := range items {
					fmt.Printf("%-6d %-40s %-24s %-30s %-30s %s\n",
						item.ID,
						item.DiscTitle,
						item.Stage,
						item.CreatedAt,
						item.UpdatedAt,
						item.DiscFingerprint,
					)
					printTaskLines("       ", item.Tasks, flagVerbose)
					if item.ErrorMessage != "" {
						fmt.Printf("       %s %s\n", failStyle("Error:"), item.ErrorMessage)
					}
				}
			} else {
				fmt.Println(labelStyle(fmt.Sprintf("%-6s %-30s %-24s %-16s %-14s", "ID", "Title", "Stage", "Created", "Fingerprint")))
				fmt.Println(dimStyle(strings.Repeat("-", 92)))
				for _, item := range items {
					fmt.Printf("%-6d %-30s %-24s %-16s %-14s\n",
						item.ID,
						truncate(item.DiscTitle, 28),
						item.Stage,
						relativeAge(item.CreatedAt),
						shortFP(item.DiscFingerprint),
					)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVarP(&stages, "stage", "s", nil, "Filter by queue stage (repeatable)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output items as JSON")
	return cmd
}

func newQueueShowCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show detailed information for a queue item",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id, err := parseQueueID(args[0])
			if err != nil {
				return err
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

			if asJSON {
				return printJSON(item)
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
			printTaskLines("", item.Tasks, flagVerbose)
			if item.NeedsReview {
				fmt.Printf("%s %s\n", labelStyle("Review:     "), strings.Join(item.ReviewReasons, "; "))
			}
			if item.ErrorMessage != "" {
				fmt.Printf("%s %s\n", failStyle("Error:      "), item.ErrorMessage)
			}
			if len(item.Metadata) != 0 {
				fmt.Printf("%s %s\n", labelStyle("Metadata:   "), item.Metadata)
			}
			if flagVerbose && len(item.RipSpec) != 0 {
				fmt.Printf("%s %s\n", labelStyle("RipSpec:    "), prettyJSON(string(item.RipSpec)))
			}
			if flagVerbose && len(item.Encoding) != 0 {
				fmt.Printf("%s %s\n", labelStyle("Encoding:   "), prettyJSON(string(item.Encoding)))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the item as JSON")
	return cmd
}

func newQueueClearCmd() *cobra.Command {
	var flagAll, flagCompleted, flagYes bool
	cmd := &cobra.Command{
		Use:   "clear [id...]",
		Short: "Remove queue items",
		Example: `  spindle queue clear 3 5        # remove specific items
  spindle queue clear --completed
  spindle queue clear --all --yes`,
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

			if flagAll {
				if err := confirm("Remove ALL queue items?", flagYes); err != nil {
					return err
				}
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

			ids, err := parseQueueIDs(args)
			if err != nil {
				return err
			}
			failed := 0
			for _, id := range ids {
				if _, err := acc.Remove(id); err != nil {
					failed++
					fmt.Fprintf(os.Stderr, "%s could not remove item %d: %v\n", warnStyle("Warning:"), id, err)
				} else {
					fmt.Println(successStyle(fmt.Sprintf("Removed item %d", id)))
				}
			}
			if failed > 0 {
				return fmt.Errorf("failed to remove %d of %d item(s)", failed, len(ids))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&flagAll, "all", false, "Remove all items")
	cmd.Flags().BoolVar(&flagCompleted, "completed", false, "Remove only completed items")
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

func newQueueRetryCmd() *cobra.Command {
	var episode string
	cmd := &cobra.Command{
		Use:   "retry [id...]",
		Short: "Retry failed queue items",
		Example: `  spindle queue retry                    # retry all failed items
  spindle queue retry 5                  # retry item 5
  spindle queue retry 5 --episode s01e05 # retry one episode of item 5`,
		RunE: func(_ *cobra.Command, args []string) error {
			if episode != "" && len(args) != 1 {
				return fmt.Errorf("--episode requires exactly one item ID")
			}

			acc, err := openQueueAccess()
			if err != nil {
				return err
			}

			if episode != "" {
				id, err := parseQueueID(args[0])
				if err != nil {
					return err
				}
				result, err := acc.RetryEpisode(id, episode)
				if err != nil {
					return err
				}
				switch result {
				case queueops.RetryResultRetried:
					fmt.Println(successStyle(fmt.Sprintf("Retried episode %s on item %d", episode, id)))
				case queueops.RetryResultNotFound:
					return fmt.Errorf("item %d not found", id)
				case queueops.RetryResultNotFailed:
					return fmt.Errorf("item %d is not in failed state", id)
				case queueops.RetryResultEpisodeNotFound:
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

			ids, err := parseQueueIDs(args)
			if err != nil {
				return err
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

func newQueueCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <id...>",
		Short: "Cancel processing for specific queue items",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			acc, err := openQueueAccess()
			if err != nil {
				return err
			}

			ids, err := parseQueueIDs(args)
			if err != nil {
				return err
			}

			if _, err := acc.Stop(ids...); err != nil {
				return err
			}
			fmt.Println(successStyle(fmt.Sprintf("Canceled %d item(s); use 'spindle queue retry' to resume", len(ids))))
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
