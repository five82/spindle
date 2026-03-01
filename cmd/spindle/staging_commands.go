package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/api"
	"spindle/internal/logging"
	"spindle/internal/queueaccess"
	"spindle/internal/staging"
)

func newStagingCommand(ctx *commandContext) *cobra.Command {
	stagingCmd := &cobra.Command{
		Use:   "staging",
		Short: "Manage staging directories",
	}

	stagingCmd.AddCommand(newStagingListCommand(ctx))
	stagingCmd.AddCommand(newStagingCleanCommand(ctx))

	return stagingCmd
}

func newStagingListCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List staging directories",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ctx.ensureConfig()
			if err != nil {
				return err
			}

			stagingDir := strings.TrimSpace(cfg.Paths.StagingDir)
			if stagingDir == "" {
				if ctx.JSONMode() {
					return writeJSON(cmd, map[string]any{
						"staging_dir":      "",
						"directories":      []any{},
						"total_size_bytes": 0,
					})
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Staging directory not configured")
				return nil
			}

			dirs, err := staging.ListDirectories(stagingDir)
			if err != nil {
				return fmt.Errorf("list staging directories: %w", err)
			}

			if ctx.JSONMode() {
				if dirs == nil {
					dirs = []staging.DirInfo{}
				}
				var totalSize int64
				for _, dir := range dirs {
					totalSize += dir.Size
				}
				return writeJSON(cmd, map[string]any{
					"staging_dir":      stagingDir,
					"directories":      dirs,
					"total_size_bytes": totalSize,
				})
			}

			if len(dirs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No staging directories found")
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Staging directory: %s\n\n", stagingDir)

			var totalSize int64
			rows := make([][]string, 0, len(dirs))
			for _, dir := range dirs {
				age := time.Since(dir.ModTime).Truncate(time.Minute)
				ageStr := formatDuration(age)
				sizeStr := logging.FormatBytes(dir.Size)
				totalSize += dir.Size
				rows = append(rows, []string{dir.Name[:12], ageStr, sizeStr})
			}

			table := renderTable(
				[]string{"Fingerprint", "Age", "Size"},
				rows,
				[]columnAlignment{alignLeft, alignRight, alignRight},
			)
			fmt.Fprint(out, table)
			fmt.Fprintf(out, "\nTotal: %d directories, %s\n", len(dirs), logging.FormatBytes(totalSize))
			return nil
		},
	}
}

func newStagingCleanCommand(ctx *commandContext) *cobra.Command {
	var cleanAll bool

	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove orphaned staging directories",
		Long: `Remove staging directories not associated with any queue item.

By default, only removes directories that are not associated with any current
queue item (orphaned directories from cleared or deleted queue entries).

Use --all to remove all staging directories regardless of queue status.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ctx.ensureConfig()
			if err != nil {
				return err
			}
			return ctx.withQueueStore(func(qa queueaccess.StoreAccess) error {
				req := api.CleanStagingRequest{
					StagingDir: cfg.Paths.StagingDir,
					CleanAll:   cleanAll,
				}
				if !cleanAll {
					req.Fingerprints = qa
				}

				result, err := api.CleanStagingDirectories(cmd.Context(), req)
				if err != nil {
					return err
				}
				if !result.Configured {
					if ctx.JSONMode() {
						return writeJSON(cmd, map[string]any{"removed": 0, "errors": []any{}})
					}
					fmt.Fprintln(cmd.OutOrStdout(), "Staging directory not configured")
					return nil
				}
				if ctx.JSONMode() {
					return writeStagingCleanJSON(cmd, result.Cleanup)
				}
				return printStagingCleanResult(cmd, result.Cleanup, result.Scope)
			})
		},
	}

	cmd.Flags().BoolVar(&cleanAll, "all", false, "Remove all staging directories (including active)")

	return cmd
}

func printStagingCleanResult(cmd *cobra.Command, result staging.CleanStaleResult, label string) error {
	out := cmd.OutOrStdout()
	if len(result.Removed) == 0 && len(result.Errors) == 0 {
		fmt.Fprintf(out, "No %s directories to clean\n", label)
		return nil
	}
	if len(result.Errors) > 0 {
		fmt.Fprintf(out, "Removed %d %s directories, %d errors\n", len(result.Removed), label, len(result.Errors))
		for _, e := range result.Errors {
			fmt.Fprintf(out, "  Error: %s: %v\n", e.Path, e.Error)
		}
		return nil
	}
	fmt.Fprintf(out, "Removed %d %s directories\n", len(result.Removed), label)
	return nil
}

func formatDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	days := int(d.Hours() / 24)
	return fmt.Sprintf("%dd", days)
}

func writeStagingCleanJSON(cmd *cobra.Command, result staging.CleanStaleResult) error {
	errs := make([]string, 0, len(result.Errors))
	for _, e := range result.Errors {
		errs = append(errs, fmt.Sprintf("%s: %v", e.Path, e.Error))
	}
	return writeJSON(cmd, map[string]any{
		"removed": len(result.Removed),
		"errors":  errs,
	})
}
