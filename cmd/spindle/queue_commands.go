package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newQueueCommand(ctx *commandContext) *cobra.Command {
	queueCmd := &cobra.Command{
		Use:   "queue",
		Short: "Inspect and manage the work queue",
	}

	queueCmd.AddCommand(newQueueStatusCommand(ctx))
	queueCmd.AddCommand(newQueueListCommand(ctx))
	queueCmd.AddCommand(newQueueShowCommand(ctx))
	queueCmd.AddCommand(newQueueClearCommand(ctx))
	queueCmd.AddCommand(newQueueClearFailedCommand(ctx))
	queueCmd.AddCommand(newQueueResetCommand(ctx))
	queueCmd.AddCommand(newQueueRetryCommand(ctx))
	queueCmd.AddCommand(newQueueHealthSubcommand(ctx))

	return queueCmd
}

func newQueueStatusCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show queue status summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.withQueueAPI(func(api queueAPI) error {
				stats, err := api.Stats(cmd.Context())
				if err != nil {
					return err
				}

				rows := buildQueueStatusRows(stats)
				if len(rows) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "Queue is empty")
					return nil
				}

				table := renderTable([]string{"Status", "Count"}, rows, []columnAlignment{alignLeft, alignRight})
				fmt.Fprint(cmd.OutOrStdout(), table)
				return nil
			})
		},
	}
}

func newQueueListCommand(ctx *commandContext) *cobra.Command {
	var listStatuses []string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List queue items",
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.withQueueAPI(func(api queueAPI) error {
				items, err := api.List(cmd.Context(), listStatuses)
				if err != nil {
					return err
				}
				if len(items) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "Queue is empty")
					return nil
				}
				table := renderTable(
					[]string{"ID", "Title", "Status", "Created", "Fingerprint"},
					buildQueueListRows(items),
					[]columnAlignment{alignRight, alignLeft, alignLeft, alignLeft, alignLeft},
				)
				fmt.Fprint(cmd.OutOrStdout(), table)
				return nil
			})
		},
	}

	cmd.Flags().StringSliceVarP(&listStatuses, "status", "s", nil, "Filter by queue status (repeatable)")
	return cmd
}

func newQueueShowCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show detailed information for a queue item",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(strings.TrimSpace(args[0]), 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("invalid item id %q", args[0])
			}
			return ctx.withQueueAPI(func(api queueAPI) error {
				details, err := api.Describe(cmd.Context(), id)
				if err != nil {
					return err
				}
				if details == nil || details.ID == 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "Queue item %d not found\n", id)
					return nil
				}
				printQueueItemDetails(cmd, *details)
				return nil
			})
		},
	}
}

func newQueueClearCommand(ctx *commandContext) *cobra.Command {
	var clearCompleted bool
	var clearFailed bool

	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Remove queue items",
		RunE: func(cmd *cobra.Command, args []string) error {
			if clearCompleted && clearFailed {
				return errors.New("specify only one of --completed or --failed")
			}
			return ctx.withQueueAPI(func(api queueAPI) error {
				out := cmd.OutOrStdout()

				var (
					removed int64
					err     error
				)
				switch {
				case clearCompleted:
					removed, err = api.ClearCompleted(cmd.Context())
				case clearFailed:
					removed, err = api.ClearFailed(cmd.Context())
				default:
					removed, err = api.ClearAll(cmd.Context())
				}
				if err != nil {
					return err
				}

				switch {
				case clearCompleted:
					fmt.Fprintf(out, "Cleared %d completed items\n", removed)
				case clearFailed:
					fmt.Fprintf(out, "Cleared %d failed items\n", removed)
				default:
					fmt.Fprintf(out, "Cleared %d queue items\n", removed)
				}
				return nil
			})
		},
	}

	cmd.Flags().BoolVar(&clearCompleted, "completed", false, "Remove only completed items")
	cmd.Flags().BoolVar(&clearFailed, "failed", false, "Remove only failed items")
	return cmd
}

func newQueueClearFailedCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "clear-failed",
		Short: "Remove failed queue items",
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.withQueueAPI(func(api queueAPI) error {
				removed, err := api.ClearFailed(cmd.Context())
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Cleared %d failed items\n", removed)
				return nil
			})
		},
	}
}

func newQueueResetCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "reset-stuck",
		Short: "Return in-flight items to pending",
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.withQueueAPI(func(api queueAPI) error {
				updated, err := api.ResetStuck(cmd.Context())
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Reset %d items\n", updated)
				return nil
			})
		},
	}
}

func newQueueRetryCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "retry [itemID...]",
		Short: "Retry failed queue items",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ids := make([]int64, 0, len(args))
			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid item id %q", arg)
				}
				ids = append(ids, id)
			}

			return ctx.withQueueAPI(func(api queueAPI) error {
				out := cmd.OutOrStdout()
				if len(ids) == 0 {
					updated, err := api.RetryAll(cmd.Context())
					if err != nil {
						return err
					}
					fmt.Fprintf(out, "Retried %d failed items\n", updated)
					return nil
				}

				result, err := api.RetryIDs(cmd.Context(), ids)
				if err != nil {
					return err
				}

				for _, item := range result.Items {
					switch item.Outcome {
					case queueRetryOutcomeNotFound:
						fmt.Fprintf(out, "Item %d not found\n", item.ID)
					case queueRetryOutcomeNotFailed:
						fmt.Fprintf(out, "Item %d is not in failed state\n", item.ID)
					case queueRetryOutcomeUpdated:
						fmt.Fprintf(out, "Item %d reset for retry\n", item.ID)
					}
				}
				return nil
			})
		},
	}
}

func printQueueItemDetails(cmd *cobra.Command, item queueItemDetailsView) {
	out := cmd.OutOrStdout()
	title := strings.TrimSpace(item.DiscTitle)
	if title == "" {
		title = "(untitled)"
	}
	fmt.Fprintf(out, "ID: %d\n", item.ID)
	fmt.Fprintf(out, "Title: %s\n", title)
	fmt.Fprintf(out, "Status: %s\n", formatStatusLabel(item.Status))
	if created := formatDisplayTime(item.CreatedAt); created != "" {
		fmt.Fprintf(out, "Created: %s\n", created)
	}
	if updated := formatDisplayTime(item.UpdatedAt); updated != "" {
		fmt.Fprintf(out, "Updated: %s\n", updated)
	}
	if trimmed := strings.TrimSpace(item.SourcePath); trimmed != "" {
		fmt.Fprintf(out, "Source: %s\n", trimmed)
	}
	if trimmed := formatFingerprint(item.DiscFingerprint); trimmed != "-" {
		fmt.Fprintf(out, "Disc Fingerprint: %s\n", trimmed)
	}
	progressStage := strings.TrimSpace(item.ProgressStage)
	if progressStage != "" || item.ProgressPercent > 0 {
		fmt.Fprintf(out, "Progress: %s (%.0f%%)\n", progressStage, item.ProgressPercent)
	}
	if msg := strings.TrimSpace(item.ProgressMessage); msg != "" {
		fmt.Fprintf(out, "Progress Message: %s\n", msg)
	}
	if item.NeedsReview {
		reason := strings.TrimSpace(item.ReviewReason)
		if reason == "" {
			reason = "(no reason provided)"
		}
		fmt.Fprintf(out, "Needs Review: yes (%s)\n", reason)
	} else if reason := strings.TrimSpace(item.ReviewReason); reason != "" {
		fmt.Fprintf(out, "Needs Review: no (previous reason %s)\n", reason)
	} else {
		fmt.Fprintln(out, "Needs Review: no")
	}
	if errMsg := strings.TrimSpace(item.ErrorMessage); errMsg != "" {
		fmt.Fprintf(out, "Last Error: %s\n", errMsg)
	}
	if value := strings.TrimSpace(item.RippedFile); value != "" {
		fmt.Fprintf(out, "Ripped File: %s\n", value)
	}
	if value := strings.TrimSpace(item.EncodedFile); value != "" {
		fmt.Fprintf(out, "Encoded File: %s\n", value)
	}
	if value := strings.TrimSpace(item.FinalFile); value != "" {
		fmt.Fprintf(out, "Final File: %s\n", value)
	}
	if value := strings.TrimSpace(item.BackgroundLogPath); value != "" {
		fmt.Fprintf(out, "Background Log: %s\n", value)
	}
	if strings.TrimSpace(item.MetadataJSON) != "" {
		fmt.Fprintln(out, "Metadata: present")
	} else {
		fmt.Fprintln(out, "Metadata: none")
	}

	summary, err := parseRipSpecSummary(item.RipSpecJSON)
	if err != nil {
		fmt.Fprintf(out, "\n⚠️  Unable to parse rip specification: %v\n", err)
		return
	}
	printRipSpecFingerprints(out, summary)
}

func newQueueHealthSubcommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Show queue health summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.withQueueAPI(func(api queueAPI) error {
				health, err := api.Health(cmd.Context())
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Total: %d\nPending: %d\nProcessing: %d\nFailed: %d\nReview: %d\nCompleted: %d\n",
					health.Total,
					health.Pending,
					health.Processing,
					health.Failed,
					health.Review,
					health.Completed,
				)
				return nil
			})
		},
	}
}
