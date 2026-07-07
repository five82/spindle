package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/staging"
)

func newStagingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "staging",
		Short:   "Manage staging directories",
		GroupID: groupMaintenance,
	}
	cmd.AddCommand(newStagingListCmd(), newStagingCleanCmd())
	return cmd
}

func newStagingListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List staging directories",
		RunE: func(_ *cobra.Command, _ []string) error {
			dirs, err := staging.ListDirectories(cfg.Paths.StagingDir)
			if err != nil {
				return err
			}

			if asJSON {
				data, err := json.MarshalIndent(dirs, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
				return nil
			}

			if len(dirs) == 0 {
				fmt.Println("No staging directories")
				return nil
			}

			var totalBytes int64
			for _, d := range dirs {
				totalBytes += d.SizeBytes
				fp := d.Name
				if len(fp) > 12 {
					fp = fp[:12]
				}
				age := time.Since(d.ModTime).Truncate(time.Minute)
				fmt.Printf("  %s  %s  %s ago\n", fp, formatBytes(d.SizeBytes), age)
			}
			fmt.Printf("\n%d directories, %s total\n", len(dirs), formatBytes(totalBytes))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output directories as JSON")
	return cmd
}

func newStagingCleanCmd() *cobra.Command {
	var flagAll, flagYes bool
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove orphaned staging directories",
		RunE: func(_ *cobra.Command, _ []string) error {
			if flagAll {
				if err := confirm("Remove ALL staging directories (including active items)?", flagYes); err != nil {
					return err
				}
			}
			var activeFingerprints map[string]struct{}
			if !flagAll {
				acc, err := openQueueAccess()
				if err != nil {
					return err
				}
				items, err := acc.List()
				if err != nil {
					return err
				}
				activeFingerprints = make(map[string]struct{})
				for _, item := range items {
					if item.DiscFingerprint != "" {
						activeFingerprints[item.DiscFingerprint] = struct{}{}
					}
				}
			}

			logger := slog.Default()
			result := staging.CleanStale(
				context.Background(),
				cfg.Paths.StagingDir,
				0, // no max age for manual clean
				activeFingerprints,
				logger,
			)
			fmt.Println(successStyle(fmt.Sprintf("Removed %d staging directories", result.Removed)))
			for _, e := range result.Errors {
				fmt.Fprintf(os.Stderr, "%s %v\n", warnStyle("Warning:"), e)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&flagAll, "all", false, "Remove all staging directories")
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}
