package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/staging"
)

func newStagingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "staging",
		Short: "Manage staging directories",
	}
	cmd.AddCommand(newStagingListCmd(), newStagingCleanCmd())
	return cmd
}

func newStagingListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List staging directories",
		RunE: func(_ *cobra.Command, _ []string) error {
			dirs, err := staging.ListDirectories(cfg.Paths.StagingDir)
			if err != nil {
				return err
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
}

func newStagingCleanCmd() *cobra.Command {
	var flagAll bool
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove orphaned staging directories",
		RunE: func(_ *cobra.Command, _ []string) error {
			var activeFingerprints map[string]struct{}
			if !flagAll {
				store, err := queue.OpenReadOnly(cfg.QueueDBPath())
				if err == nil {
					activeFingerprints, _ = store.ActiveFingerprints()
					_ = store.Close()
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
			fmt.Printf("Removed %d staging directories\n", result.Removed)
			for _, e := range result.Errors {
				fmt.Fprintf(os.Stderr, "Warning: %v\n", e)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&flagAll, "all", false, "Remove all staging directories")
	return cmd
}
