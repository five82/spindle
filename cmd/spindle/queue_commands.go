package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"spindle/internal/queue"
	"spindle/internal/ripspec"
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
	queueCmd.AddCommand(newQueueResetCommand(ctx))
	queueCmd.AddCommand(newQueueRetryCommand(ctx))
	queueCmd.AddCommand(newQueueStopCommand(ctx))
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

				if ctx.JSONMode() {
					if stats == nil {
						stats = map[string]int{}
					}
					return writeJSON(cmd, stats)
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

				if ctx.JSONMode() {
					if items == nil {
						items = []queueItemView{}
					}
					return writeJSON(cmd, items)
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
					if ctx.JSONMode() {
						return writeJSON(cmd, map[string]any{"error": "not_found", "id": id})
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Queue item %d not found\n", id)
					return nil
				}
				if ctx.JSONMode() {
					return writeJSON(cmd, details)
				}
				printQueueItemDetails(cmd, *details)
				return nil
			})
		},
	}
}

func newQueueClearCommand(ctx *commandContext) *cobra.Command {
	var clearAll bool
	var clearCompleted bool
	var clearFailed bool

	cmd := &cobra.Command{
		Use:   "clear [id...]",
		Short: "Remove queue items",
		Long: `Remove queue items from the queue.

Requires either item IDs or a flag to specify what to clear.

Examples:
  spindle queue clear 10           # Remove item 10
  spindle queue clear 10 11 12     # Remove items 10, 11, and 12
  spindle queue clear --all        # Remove all items
  spindle queue clear --completed  # Remove only completed items
  spindle queue clear --failed     # Remove only failed items`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Count how many filter flags are set
			flagCount := 0
			if clearAll {
				flagCount++
			}
			if clearCompleted {
				flagCount++
			}
			if clearFailed {
				flagCount++
			}
			if flagCount > 1 {
				return errors.New("specify only one of --all, --completed, or --failed")
			}

			// Parse IDs if provided
			ids := make([]int64, 0, len(args))
			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil || id <= 0 {
					return fmt.Errorf("invalid item id %q", arg)
				}
				ids = append(ids, id)
			}

			// If IDs provided, cannot use filter flags
			if len(ids) > 0 && flagCount > 0 {
				return errors.New("cannot use --all, --completed, or --failed with specific item IDs")
			}

			// Require either IDs or a flag
			if len(ids) == 0 && flagCount == 0 {
				return errors.New("specify item IDs or use --all, --completed, or --failed")
			}

			return ctx.withQueueAPI(func(api queueAPI) error {
				out := cmd.OutOrStdout()

				// Handle specific IDs
				if len(ids) > 0 {
					result, err := api.RemoveIDs(cmd.Context(), ids)
					if err != nil {
						return err
					}
					if ctx.JSONMode() {
						type jsonItem struct {
							ID      int64  `json:"id"`
							Outcome string `json:"outcome"`
						}
						items := make([]jsonItem, 0, len(result.Items))
						for _, item := range result.Items {
							outcome := "removed"
							if item.Outcome == queueRemoveOutcomeNotFound {
								outcome = "not_found"
							}
							items = append(items, jsonItem{ID: item.ID, Outcome: outcome})
						}
						return writeJSON(cmd, map[string]any{"items": items})
					}
					for _, item := range result.Items {
						switch item.Outcome {
						case queueRemoveOutcomeNotFound:
							fmt.Fprintf(out, "Item %d not found\n", item.ID)
						case queueRemoveOutcomeRemoved:
							fmt.Fprintf(out, "Item %d removed\n", item.ID)
						}
					}
					return nil
				}

				// Handle bulk clear
				var (
					removed int64
					err     error
				)
				switch {
				case clearCompleted:
					removed, err = api.ClearCompleted(cmd.Context())
				case clearFailed:
					removed, err = api.ClearFailed(cmd.Context())
				case clearAll:
					removed, err = api.ClearAll(cmd.Context())
				}
				if err != nil {
					return err
				}

				if ctx.JSONMode() {
					return writeJSON(cmd, map[string]any{"cleared": removed})
				}

				switch {
				case clearCompleted:
					fmt.Fprintf(out, "Cleared %d completed items\n", removed)
				case clearFailed:
					fmt.Fprintf(out, "Cleared %d failed items\n", removed)
				case clearAll:
					fmt.Fprintf(out, "Cleared %d queue items\n", removed)
				}
				return nil
			})
		},
	}

	cmd.Flags().BoolVar(&clearAll, "all", false, "Remove all items")
	cmd.Flags().BoolVar(&clearCompleted, "completed", false, "Remove only completed items")
	cmd.Flags().BoolVar(&clearFailed, "failed", false, "Remove only failed items")
	return cmd
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
				if ctx.JSONMode() {
					return writeJSON(cmd, map[string]any{"reset": updated})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Reset %d items\n", updated)
				return nil
			})
		},
	}
}

func newQueueRetryCommand(ctx *commandContext) *cobra.Command {
	var episodeKey string

	cmd := &cobra.Command{
		Use:   "retry [itemID...]",
		Short: "Retry failed queue items",
		Long: `Retry failed queue items.

Without --episode, retries the entire item from the beginning.
With --episode, clears only the specified episode's failed status so it can be
re-processed while leaving other episodes' progress intact.

Examples:
  spindle queue retry 123              # Retry entire item
  spindle queue retry 123 --episode s01e05  # Retry only episode S01E05`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ids := make([]int64, 0, len(args))
			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid item id %q", arg)
				}
				ids = append(ids, id)
			}

			// Per-episode retry requires direct store access
			if episodeKey != "" {
				if len(ids) != 1 {
					return errors.New("--episode requires exactly one item ID")
				}
				return ctx.withQueueStore(func(store queueStoreAPI) error {
					out := cmd.OutOrStdout()
					result, err := store.RetryEpisode(cmd.Context(), ids[0], episodeKey)
					if err != nil {
						return err
					}
					if ctx.JSONMode() {
						outcome := retryOutcomeString(result.Outcome)
						return writeJSON(cmd, map[string]any{
							"id":         ids[0],
							"episode":    episodeKey,
							"outcome":    outcome,
							"new_status": result.NewStatus,
						})
					}
					switch result.Outcome {
					case queueRetryOutcomeNotFound:
						fmt.Fprintf(out, "Item %d not found\n", ids[0])
					case queueRetryOutcomeNotFailed:
						fmt.Fprintf(out, "Item %d is not in a retryable state\n", ids[0])
					case queueRetryOutcomeEpisodeNotFound:
						fmt.Fprintf(out, "Episode %s not found in item %d\n", episodeKey, ids[0])
					case queueRetryOutcomeUpdated:
						fmt.Fprintf(out, "Episode %s in item %d cleared for retry (item reset to %s)\n",
							strings.ToUpper(episodeKey), ids[0], result.NewStatus)
					}
					return nil
				})
			}

			return ctx.withQueueAPI(func(api queueAPI) error {
				out := cmd.OutOrStdout()
				if len(ids) == 0 {
					updated, err := api.RetryAll(cmd.Context())
					if err != nil {
						return err
					}
					if ctx.JSONMode() {
						return writeJSON(cmd, map[string]any{"retried": updated})
					}
					fmt.Fprintf(out, "Retried %d failed items\n", updated)
					return nil
				}

				result, err := api.RetryIDs(cmd.Context(), ids)
				if err != nil {
					return err
				}

				if ctx.JSONMode() {
					type jsonItem struct {
						ID      int64  `json:"id"`
						Outcome string `json:"outcome"`
					}
					items := make([]jsonItem, 0, len(result.Items))
					for _, item := range result.Items {
						items = append(items, jsonItem{ID: item.ID, Outcome: retryOutcomeString(item.Outcome)})
					}
					return writeJSON(cmd, map[string]any{"items": items})
				}

				for _, item := range result.Items {
					switch item.Outcome {
					case queueRetryOutcomeNotFound:
						fmt.Fprintf(out, "Item %d not found\n", item.ID)
					case queueRetryOutcomeNotFailed:
						fmt.Fprintf(out, "Item %d is not in a retryable state (only failed items can be retried)\n", item.ID)
					case queueRetryOutcomeUpdated:
						fmt.Fprintf(out, "Item %d reset for retry\n", item.ID)
					}
				}
				return nil
			})
		},
	}

	cmd.Flags().StringVarP(&episodeKey, "episode", "e", "", "Retry only a specific episode (e.g., s01e05)")
	return cmd
}

func newQueueStopCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "stop <id...>",
		Short: "Stop processing for specific queue items",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids := make([]int64, 0, len(args))
			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil || id <= 0 {
					return fmt.Errorf("invalid item id %q", arg)
				}
				ids = append(ids, id)
			}

			return ctx.withQueueAPI(func(api queueAPI) error {
				out := cmd.OutOrStdout()
				result, err := api.StopIDs(cmd.Context(), ids)
				if err != nil {
					return err
				}
				if ctx.JSONMode() {
					type jsonItem struct {
						ID          int64  `json:"id"`
						Outcome     string `json:"outcome"`
						PriorStatus string `json:"prior_status,omitempty"`
					}
					items := make([]jsonItem, 0, len(result.Items))
					for _, item := range result.Items {
						items = append(items, jsonItem{
							ID:          item.ID,
							Outcome:     stopOutcomeString(item.Outcome),
							PriorStatus: item.PriorStatus,
						})
					}
					return writeJSON(cmd, map[string]any{"items": items})
				}
				for _, item := range result.Items {
					switch item.Outcome {
					case queueStopOutcomeNotFound:
						fmt.Fprintf(out, "Item %d not found\n", item.ID)
					case queueStopOutcomeAlreadyCompleted:
						fmt.Fprintf(out, "Item %d is already completed\n", item.ID)
					case queueStopOutcomeAlreadyFailed:
						fmt.Fprintf(out, "Item %d is already failed\n", item.ID)
					case queueStopOutcomeUpdated:
						message := fmt.Sprintf("Item %d stop requested", item.ID)
						if parsed, ok := queue.ParseStatus(item.PriorStatus); ok && queue.IsProcessingStatus(parsed) {
							statusLabel := formatStatusLabel(item.PriorStatus)
							message = fmt.Sprintf("Item %d stop requested (currently %s; will halt after current stage)", item.ID, statusLabel)
						}
						fmt.Fprintln(out, message)
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
	if value := strings.TrimSpace(item.ItemLogPath); value != "" {
		fmt.Fprintf(out, "Item Log: %s\n", value)
	}
	if strings.TrimSpace(item.MetadataJSON) != "" {
		fmt.Fprintln(out, "Metadata: present")
	} else {
		fmt.Fprintln(out, "Metadata: none")
	}
	if len(item.Episodes) > 0 {
		fmt.Fprintln(out, "\nEpisodes:")
		printEpisodeDetails(out, item)
	}

	summary, err := ripspec.Parse(item.RipSpecJSON)
	if err != nil {
		fmt.Fprintf(out, "\n⚠️  Unable to parse rip specification: %v\n", err)
		return
	}
	printRipSpecFingerprints(out, summary)
}

func printEpisodeDetails(out io.Writer, item queueItemDetailsView) {
	totals := item.EpisodeTotals
	if totals.Planned == 0 {
		totals = tallyEpisodeTotals(item.Episodes)
	}
	fmt.Fprintf(out, "  Planned: %d | Ripped: %d | Encoded: %d | Final: %d\n", totals.Planned, totals.Ripped, totals.Encoded, totals.Final)
	mapping := "pending verification"
	if item.EpisodesSynced {
		mapping = "synced with TMDB"
	}
	fmt.Fprintf(out, "  Mapping: %s\n", mapping)
	for _, ep := range item.Episodes {
		label := formatEpisodeLabel(ep)
		stage := strings.ToUpper(strings.TrimSpace(ep.Stage))
		if stage == "" {
			stage = "PLANNED"
		}
		title := strings.TrimSpace(ep.Title)
		if title == "" {
			title = "Unlabeled"
		}
		marker := " "
		if ep.Active {
			marker = "*"
		}
		fmt.Fprintf(out, "  %s%s  %-8s  %s\n", marker, label, stage, title)
		if path := primaryEpisodePath(ep); path != "" {
			fmt.Fprintf(out, "      File: %s\n", path)
		}
		if info := episodeSubtitleInfo(ep); info != "" {
			fmt.Fprintf(out, "      Subtitles: %s\n", info)
		}
		if ep.Active {
			if msg := strings.TrimSpace(ep.ProgressMessage); msg != "" {
				fmt.Fprintf(out, "      Active: %s\n", msg)
			} else if ep.ProgressPercent > 0 {
				fmt.Fprintf(out, "      Active: %.0f%%\n", ep.ProgressPercent)
			}
		}
	}
}

func primaryEpisodePath(ep queueEpisodeView) string {
	if strings.TrimSpace(ep.FinalPath) != "" {
		return strings.TrimSpace(ep.FinalPath)
	}
	if strings.TrimSpace(ep.EncodedPath) != "" {
		return strings.TrimSpace(ep.EncodedPath)
	}
	return strings.TrimSpace(ep.RippedPath)
}

func episodeSubtitleInfo(ep queueEpisodeView) string {
	lang := strings.ToUpper(strings.TrimSpace(ep.SubtitleLanguage))
	source := strings.TrimSpace(ep.SubtitleSource)
	score := ep.MatchScore
	parts := make([]string, 0, 3)
	if lang != "" {
		parts = append(parts, lang)
	}
	if source != "" {
		parts = append(parts, source)
	}
	if score > 0 {
		parts = append(parts, fmt.Sprintf("score %.2f", score))
	}
	return strings.Join(parts, " · ")
}

func formatEpisodeLabel(ep queueEpisodeView) string {
	if ep.Season > 0 && ep.Episode > 0 {
		return fmt.Sprintf("S%02dE%02d", ep.Season, ep.Episode)
	}
	if strings.TrimSpace(ep.Key) != "" {
		return strings.ToUpper(strings.TrimSpace(ep.Key))
	}
	return "EP"
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
				if ctx.JSONMode() {
					return writeJSON(cmd, health)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Total: %d\nPending: %d\nProcessing: %d\nFailed: %d\nCompleted: %d\n",
					health.Total,
					health.Pending,
					health.Processing,
					health.Failed,
					health.Completed,
				)
				return nil
			})
		},
	}
}

func retryOutcomeString(o queueRetryOutcome) string {
	switch o {
	case queueRetryOutcomeUpdated:
		return "retried"
	case queueRetryOutcomeNotFound:
		return "not_found"
	case queueRetryOutcomeNotFailed:
		return "not_failed"
	case queueRetryOutcomeEpisodeNotFound:
		return "episode_not_found"
	default:
		return "unknown"
	}
}

func stopOutcomeString(o queueStopOutcome) string {
	switch o {
	case queueStopOutcomeUpdated:
		return "stopped"
	case queueStopOutcomeNotFound:
		return "not_found"
	case queueStopOutcomeAlreadyCompleted:
		return "already_completed"
	case queueStopOutcomeAlreadyFailed:
		return "already_failed"
	default:
		return "unknown"
	}
}
