package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/auditgather"
	"github.com/five82/spindle/internal/queue"
)

func newAuditGatherCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "audit-gather <item-id>",
		Short: "Gather audit artifacts for a queue item",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid item ID: %s", args[0])
			}

			store, err := queue.OpenReadOnly(cfg.QueueDBPath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			item, err := store.GetByID(id)
			if err != nil {
				return err
			}
			if item == nil {
				return fmt.Errorf("queue item %d not found", id)
			}

			report, err := auditgather.Gather(cmd.Context(), cfg, item)
			if err != nil {
				return err
			}

			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		},
	}
}
