package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"spindle/internal/auditgather"
	"spindle/internal/queue"
)

func newAuditGatherCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "audit-gather <item-id>",
		Short: "Gather audit artifacts for a queue item",
		Long: `Collects all artifacts needed to audit a queue item and emits them as
structured JSON. This includes queue metadata, parsed log entries,
rip cache contents, ripspec envelope, encoding details, and ffprobe
output for encoded files.

Designed to be consumed by the itemaudit skill so it can focus on
analysis rather than artifact discovery.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(strings.TrimSpace(args[0]), 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("invalid item id %q", args[0])
			}

			cfg, err := ctx.ensureConfig()
			if err != nil {
				return err
			}

			// Direct store access to get the raw queue.Item (needed for
			// auditgather.Gather which operates on internal types).
			store, err := queue.Open(cfg)
			if err != nil {
				return fmt.Errorf("open queue store: %w", err)
			}
			defer store.Close()

			item, err := store.GetByID(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("fetch item: %w", err)
			}
			if item == nil {
				return fmt.Errorf("queue item %d not found", id)
			}

			report, err := auditgather.Gather(cmd.Context(), cfg, item)
			if err != nil {
				return fmt.Errorf("gather audit data: %w", err)
			}

			return writeJSON(cmd, report)
		},
	}
}
