package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/auditgather"
)

func newQueueAuditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "audit <id>",
		Short: "Audit a queue item: digest to stdout, full JSON report written to a temp file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			report, err := auditgather.Gather(cmd.Context(), cfg, item)
			if err != nil {
				return err
			}

			jsonPath := filepath.Join(os.TempDir(), fmt.Sprintf("spindle-audit-%d.json", id))
			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal audit report: %w", err)
			}
			if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
				return fmt.Errorf("write audit report: %w", err)
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), auditgather.RenderDigest(report, jsonPath))
			return err
		},
	}
}
