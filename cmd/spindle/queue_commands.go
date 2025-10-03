package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/ipc"
	"spindle/internal/queue"
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
			return ctx.withStore(func(client *ipc.Client, store *queue.Store) error {
				var stats map[queue.Status]int
				var err error

				if client != nil {
					// Use IPC if daemon is running
					status, err := client.Status()
					if err != nil {
						return err
					}
					// Convert IPC stats to queue stats
					stats = make(map[queue.Status]int)
					for status, count := range status.QueueStats {
						stats[queue.Status(status)] = count
					}
				} else {
					// Use direct store access
					stats, err = store.Stats(cmd.Context())
					if err != nil {
						return err
					}
				}

				// Convert queue.Status keys to strings for buildQueueStatusRows
				stringStats := make(map[string]int, len(stats))
				for status, count := range stats {
					stringStats[string(status)] = count
				}
				rows := buildQueueStatusRows(stringStats)
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
			return ctx.withStore(func(client *ipc.Client, store *queue.Store) error {
				if client != nil {
					// Use IPC if daemon is running
					resp, err := client.QueueList(listStatuses)
					if err != nil {
						return err
					}
					if len(resp.Items) == 0 {
						fmt.Fprintln(cmd.OutOrStdout(), "Queue is empty")
						return nil
					}
					table := renderTable(
						[]string{"ID", "Title", "Status", "Created", "Fingerprint"},
						buildQueueListRows(resp.Items),
						[]columnAlignment{alignRight, alignLeft, alignLeft, alignLeft, alignLeft},
					)
					fmt.Fprint(cmd.OutOrStdout(), table)
					return nil
				} else {
					// Use direct store access
					var statuses []queue.Status
					for _, statusStr := range listStatuses {
						statuses = append(statuses, queue.Status(statusStr))
					}

					items, err := store.List(cmd.Context(), statuses...)
					if err != nil {
						return err
					}
					if len(items) == 0 {
						fmt.Fprintln(cmd.OutOrStdout(), "Queue is empty")
						return nil
					}

					// Convert queue items to IPC items for display
					ipcItems := make([]ipc.QueueItem, len(items))
					for i, item := range items {
						ipcItems[i] = ipc.QueueItem{
							ID:              item.ID,
							SourcePath:      item.SourcePath,
							DiscTitle:       item.DiscTitle,
							Status:          string(item.Status),
							CreatedAt:       item.CreatedAt.Format(time.RFC3339),
							DiscFingerprint: item.DiscFingerprint,
						}
					}

					table := renderTable(
						[]string{"ID", "Title", "Status", "Created", "Fingerprint"},
						buildQueueListRows(ipcItems),
						[]columnAlignment{alignRight, alignLeft, alignLeft, alignLeft, alignLeft},
					)
					fmt.Fprint(cmd.OutOrStdout(), table)
					return nil
				}
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
			return ctx.withStore(func(client *ipc.Client, store *queue.Store) error {
				out := cmd.OutOrStdout()
				if clearForce {
					fmt.Fprintln(out, "Clearing queue without confirmation (--force)")
				}

				if client != nil {
					// Use IPC if daemon is running
					switch {
					case clearCompleted:
						resp, err := client.QueueClearCompleted()
						if err != nil {
							return err
						}
						fmt.Fprintf(out, "Cleared %d completed items\n", resp.Removed)
					case clearFailed:
						resp, err := client.QueueClearFailed()
						if err != nil {
							return err
						}
						fmt.Fprintf(out, "Cleared %d failed items\n", resp.Removed)
					default:
						resp, err := client.QueueClear()
						if err != nil {
							return err
						}
						fmt.Fprintf(out, "Cleared %d queue items\n", resp.Removed)
					}
				} else {
					// Use direct store access
					var removed int64
					var err error
					switch {
					case clearCompleted:
						removed, err = store.ClearCompleted(cmd.Context())
						if err != nil {
							return err
						}
						fmt.Fprintf(out, "Cleared %d completed items\n", removed)
					case clearFailed:
						removed, err = store.ClearFailed(cmd.Context())
						if err != nil {
							return err
						}
						fmt.Fprintf(out, "Cleared %d failed items\n", removed)
					default:
						removed, err = store.Clear(cmd.Context())
						if err != nil {
							return err
						}
						fmt.Fprintf(out, "Cleared %d queue items\n", removed)
					}
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
			return ctx.withStore(func(client *ipc.Client, store *queue.Store) error {
				if client != nil {
					// Use IPC if daemon is running
					resp, err := client.QueueClearFailed()
					if err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Cleared %d failed items\n", resp.Removed)
				} else {
					// Use direct store access
					removed, err := store.ClearFailed(cmd.Context())
					if err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Cleared %d failed items\n", removed)
				}
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
			return ctx.withStore(func(client *ipc.Client, store *queue.Store) error {
				if client != nil {
					// Use IPC if daemon is running
					resp, err := client.QueueReset()
					if err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Reset %d items\n", resp.Updated)
				} else {
					// Use direct store access
					updated, err := store.ResetStuckProcessing(cmd.Context())
					if err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Reset %d items\n", updated)
				}
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

			return ctx.withStore(func(client *ipc.Client, store *queue.Store) error {
				out := cmd.OutOrStdout()
				if client != nil {
					// Use IPC if daemon is running
					if len(ids) == 0 {
						resp, err := client.QueueRetry(nil)
						if err != nil {
							return err
						}
						fmt.Fprintf(out, "Retried %d failed items\n", resp.Updated)
						return nil
					}

					resp, err := client.QueueList(nil)
					if err != nil {
						return err
					}
					itemsByID := make(map[int64]ipc.QueueItem, len(resp.Items))
					for _, item := range resp.Items {
						itemsByID[item.ID] = item
					}

					for _, id := range ids {
						item, ok := itemsByID[id]
						if !ok {
							fmt.Fprintf(out, "Item %d not found\n", id)
							continue
						}
						if strings.ToLower(strings.TrimSpace(item.Status)) != "failed" {
							fmt.Fprintf(out, "Item %d is not in failed state\n", id)
							continue
						}
						retryResp, retryErr := client.QueueRetry([]int64{id})
						if retryErr != nil {
							return retryErr
						}
						if retryResp.Updated > 0 {
							fmt.Fprintf(out, "Item %d reset for retry\n", id)
						} else {
							fmt.Fprintf(out, "Item %d is not in failed state\n", id)
						}
					}
					return nil
				} else {
					// Use direct store access
					if len(ids) == 0 {
						updated, err := store.RetryFailed(cmd.Context())
						if err != nil {
							return err
						}
						fmt.Fprintf(out, "Retried %d failed items\n", updated)
						return nil
					}

					// Get all items to check status
					items, err := store.List(cmd.Context())
					if err != nil {
						return err
					}
					itemsByID := make(map[int64]*queue.Item, len(items))
					for _, item := range items {
						itemsByID[item.ID] = item
					}

					for _, id := range ids {
						item, ok := itemsByID[id]
						if !ok {
							fmt.Fprintf(out, "Item %d not found\n", id)
							continue
						}
						if item.Status != queue.StatusFailed {
							fmt.Fprintf(out, "Item %d is not in failed state\n", id)
							continue
						}
						updated, err := store.RetryFailed(cmd.Context(), id)
						if err != nil {
							return err
						}
						if updated > 0 {
							fmt.Fprintf(out, "Item %d reset for retry\n", id)
						} else {
							fmt.Fprintf(out, "Item %d is not in failed state\n", id)
						}
					}
					return nil
				}
			})
		},
	}
}

func newQueueHealthSubcommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Show queue health summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.withStore(func(client *ipc.Client, store *queue.Store) error {
				if client != nil {
					// Use IPC if daemon is running
					health, err := client.QueueHealth()
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
				} else {
					// Use direct store access
					health, err := store.Health(cmd.Context())
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
				}
				return nil
			})
		},
	}
}
