package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"spindle/internal/api"
)

func newCacheProcessCommand(ctx *commandContext) *cobra.Command {
	var allowDuplicate bool

	cmd := &cobra.Command{
		Use:   "process <number>",
		Short: "Queue a cached rip for processing",
		Long: `Queue a cached rip for post-rip stages (ripping from cache, encoding, subtitles, and organizing).
Use 'spindle cache stats' to find entry numbers.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, warn, err := cacheManager(ctx)
			if warn != "" {
				fmt.Fprintln(cmd.OutOrStdout(), warn)
			}
			if err != nil || manager == nil {
				return err
			}

			var entryNum int
			if _, err := fmt.Sscanf(args[0], "%d", &entryNum); err != nil || entryNum < 1 {
				return fmt.Errorf("invalid entry number: %s (must be a positive integer)", args[0])
			}

			cfg, err := ctx.ensureConfig()
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}

			result, err := api.QueueCachedEntryForProcessing(cmd.Context(), api.QueueCachedEntryRequest{
				Config:         cfg,
				EntryNumber:    entryNum,
				AllowDuplicate: allowDuplicate,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Queued cache entry %d as item %d (%s)\n", entryNum, result.ItemID, result.DiscTitle)
			return nil
		},
	}

	cmd.Flags().BoolVar(&allowDuplicate, "allow-duplicate", false, "Allow multiple queue items with the same disc fingerprint")

	return cmd
}
