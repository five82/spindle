package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

func newQueueCommand(ctx *commandContext) *cobra.Command {
	queueCmd := &cobra.Command{
		Use:   "queue",
		Short: "Inspect and manage the work queue",
	}

	queueCmd.AddCommand(newQueueStatusCommand(ctx))
	queueCmd.AddCommand(newQueueListCommand(ctx))
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

func newQueueClearCommand(ctx *commandContext) *cobra.Command {
	var clearCompleted bool
	var clearFailed bool
	var clearForce bool

	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Remove queue items",
		RunE: func(cmd *cobra.Command, args []string) error {
			if clearCompleted && clearFailed {
				return errors.New("specify only one of --completed or --failed")
			}
			return ctx.withQueueAPI(func(api queueAPI) error {
				out := cmd.OutOrStdout()
				if clearForce {
					fmt.Fprintln(out, "Clearing queue without confirmation (--force)")
				}

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
	cmd.Flags().BoolVar(&clearForce, "force", false, "No-op flag for compatibility; removal always proceeds")
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
