package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

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
				sizeStr := formatSize(dir.Size)
				totalSize += dir.Size
				rows = append(rows, []string{dir.Name[:12], ageStr, sizeStr})
			}

			table := renderTable(
				[]string{"Fingerprint", "Age", "Size"},
				rows,
				[]columnAlignment{alignLeft, alignRight, alignRight},
			)
			fmt.Fprint(out, table)
			fmt.Fprintf(out, "\nTotal: %d directories, %s\n", len(dirs), formatSize(totalSize))
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

			stagingDir := strings.TrimSpace(cfg.Paths.StagingDir)
			if stagingDir == "" {
				if ctx.JSONMode() {
					return writeJSON(cmd, map[string]any{"removed": 0, "errors": []any{}})
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Staging directory not configured")
				return nil
			}

			if ctx.JSONMode() {
				if cleanAll {
					result := staging.CleanStale(cmd.Context(), stagingDir, 0, nil)
					return writeStagingCleanJSON(cmd, result)
				}
				return ctx.withQueueStore(func(api queueStoreAPI) error {
					items, err := api.List(cmd.Context(), nil)
					if err != nil {
						return fmt.Errorf("list queue items: %w", err)
					}
					activeFingerprints := make(map[string]struct{})
					for _, item := range items {
						fp := strings.ToUpper(strings.TrimSpace(item.DiscFingerprint))
						if fp != "" {
							activeFingerprints[fp] = struct{}{}
						}
					}
					result := staging.CleanOrphaned(cmd.Context(), stagingDir, activeFingerprints, nil)
					return writeStagingCleanJSON(cmd, result)
				})
			}

			out := cmd.OutOrStdout()

			if cleanAll {
				return cleanAllStaging(cmd.Context(), stagingDir, out)
			}

			return ctx.withQueueStore(func(api queueStoreAPI) error {
				return cleanOrphanedStaging(cmd.Context(), stagingDir, api, out)
			})
		},
	}

	cmd.Flags().BoolVar(&cleanAll, "all", false, "Remove all staging directories (including active)")

	return cmd
}

func cleanAllStaging(ctx context.Context, stagingDir string, out interface{ Write([]byte) (int, error) }) error {
	dirs, err := staging.ListDirectories(stagingDir)
	if err != nil {
		return fmt.Errorf("list staging directories: %w", err)
	}

	if len(dirs) == 0 {
		fmt.Fprintln(out, "No staging directories to clean")
		return nil
	}

	// Use a very short duration to match everything
	result := staging.CleanStale(ctx, stagingDir, 0, nil)

	var totalSize int64
	for _, dir := range dirs {
		totalSize += dir.Size
	}

	if len(result.Errors) > 0 {
		fmt.Fprintf(out, "Removed %d directories, %d errors\n", len(result.Removed), len(result.Errors))
		for _, e := range result.Errors {
			fmt.Fprintf(out, "  Error: %s: %v\n", e.Path, e.Error)
		}
	} else {
		fmt.Fprintf(out, "Removed %d directories (%s reclaimed)\n", len(result.Removed), formatSize(totalSize))
	}
	return nil
}

func cleanOrphanedStaging(ctx context.Context, stagingDir string, api queueStoreAPI, out interface{ Write([]byte) (int, error) }) error {
	// Get all fingerprints from queue items
	items, err := api.List(ctx, nil)
	if err != nil {
		return fmt.Errorf("list queue items: %w", err)
	}

	activeFingerprints := make(map[string]struct{})
	for _, item := range items {
		fp := strings.ToUpper(strings.TrimSpace(item.DiscFingerprint))
		if fp != "" {
			activeFingerprints[fp] = struct{}{}
		}
	}

	// List staging directories to calculate sizes before cleanup
	dirs, err := staging.ListDirectories(stagingDir)
	if err != nil {
		return fmt.Errorf("list staging directories: %w", err)
	}

	// Calculate sizes for orphaned directories
	orphanSizes := make(map[string]int64)
	for _, dir := range dirs {
		dirName := strings.ToUpper(dir.Name)
		if _, active := activeFingerprints[dirName]; !active {
			// Skip queue-{ID} format directories
			if !strings.HasPrefix(strings.ToLower(dir.Name), "queue-") {
				orphanSizes[dir.Path] = dir.Size
			}
		}
	}

	result := staging.CleanOrphaned(ctx, stagingDir, activeFingerprints, nil)

	if len(result.Removed) == 0 && len(result.Errors) == 0 {
		fmt.Fprintln(out, "No orphaned staging directories to clean")
		return nil
	}

	var totalReclaimed int64
	for _, path := range result.Removed {
		totalReclaimed += orphanSizes[path]
	}

	if len(result.Errors) > 0 {
		fmt.Fprintf(out, "Removed %d orphaned directories, %d errors\n", len(result.Removed), len(result.Errors))
		for _, e := range result.Errors {
			fmt.Fprintf(out, "  Error: %s: %v\n", e.Path, e.Error)
		}
	} else {
		fmt.Fprintf(out, "Removed %d orphaned directories (%s reclaimed)\n", len(result.Removed), formatSize(totalReclaimed))
	}
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

func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
