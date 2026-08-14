package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/jellyfin"
)

// newJellyfinCmd exposes the Jellyfin library refresh the organizer stage
// performs internally, so manual orchestration can trigger a scan after
// placing files in the library by hand.
func newJellyfinCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "jellyfin",
		Short:   "Jellyfin server integration",
		GroupID: groupMaintenance,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "refresh",
		Short: "Trigger a Jellyfin library refresh",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			client := jellyfin.New(cfg.Jellyfin.URL, cfg.Jellyfin.APIKey, buildLogger())
			if client == nil {
				return fmt.Errorf("jellyfin is not configured (set jellyfin.url and jellyfin.api_key)")
			}
			if err := client.Refresh(context.Background()); err != nil {
				return err
			}
			fmt.Println(successStyle("Jellyfin library refresh triggered"))
			return nil
		},
	})
	return cmd
}
