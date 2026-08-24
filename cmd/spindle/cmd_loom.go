package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/loom"
)

// newLoomCmd exposes the Loom scan the organizer stage performs internally,
// so manual orchestration can trigger a scan after placing files in the
// library by hand.
func newLoomCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "loom",
		Short:   "Loom server integration",
		GroupID: groupMaintenance,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "scan",
		Short: "Trigger a Loom library scan",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			client := loom.New(cfg.Loom.URL, buildLogger())
			if client == nil {
				return fmt.Errorf("loom is not configured (set loom.url)")
			}
			if err := client.Scan(context.Background()); err != nil {
				return err
			}
			fmt.Println(successStyle("Loom library scan triggered"))
			return nil
		},
	})
	return cmd
}
