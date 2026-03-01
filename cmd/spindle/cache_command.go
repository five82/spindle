package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"spindle/internal/api"
	"spindle/internal/logging"
	"spindle/internal/ripcache"
)

func newCacheCommand(ctx *commandContext) *cobra.Command {
	cacheCmd := &cobra.Command{
		Use:   "cache",
		Short: "Inspect and manage the rip cache",
		Long: `Inspect and manage the rip cache.

The rip cache stores MakeMKV output between ripping and encoding stages.
Spindle automatically manages cache size and free space during normal operation.

Commands:
  rip        - Rip a disc into the rip cache
  stats      - Show all cached entries with their sizes and ages
  process    - Queue a cached entry for post-rip processing
  remove     - Remove a specific entry by number (see 'stats' for numbers)
  clear      - Remove all cached entries
  crop       - Run crop detection on a cached file (troubleshooting)
  commentary - Run commentary detection on a cached file (troubleshooting)`,
	}

	cacheCmd.AddCommand(newCacheRipCommand(ctx))
	cacheCmd.AddCommand(newCacheStatsCommand(ctx))
	cacheCmd.AddCommand(newCacheProcessCommand(ctx))
	cacheCmd.AddCommand(newCacheRemoveCommand(ctx))
	cacheCmd.AddCommand(newCacheClearCommand(ctx))
	cacheCmd.AddCommand(newCacheCropCommand(ctx))
	cacheCmd.AddCommand(newCacheCommentaryCommand(ctx))

	return cacheCmd
}

func newCacheStatsCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Show all cached entries with sizes and ages",
		Long:  "Display detailed information about each cached rip, including size and last update time. Each entry is numbered for use with 'spindle cache remove'.",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, warn, err := cacheManager(ctx)
			if warn != "" && !ctx.JSONMode() {
				fmt.Fprintln(cmd.OutOrStdout(), warn)
			}
			if err != nil || manager == nil {
				return err
			}

			stats, err := manager.Stats(cmd.Context())
			if err != nil {
				return err
			}
			if ctx.JSONMode() {
				return writeJSON(cmd, stats)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Entries: %d\n", stats.Entries)
			fmt.Fprintf(out, "Size:   %s / %s\n", logging.FormatBytes(stats.TotalBytes), logging.FormatBytes(stats.MaxBytes))
			fmt.Fprintf(out, "Disk:   %s free (%.1f%%)\n", logging.FormatBytes(int64(stats.FreeBytes)), stats.FreeRatio*100)
			printCacheEntries(out, stats.EntrySummaries)
			return nil
		},
	}
}

func printCacheEntries(out io.Writer, entries []ripcache.EntrySummary) {
	if len(entries) == 0 {
		fmt.Fprintln(out, "Cached titles: none")
		return
	}
	const stampLayout = "2006-01-02 15:04"
	fmt.Fprintln(out, "Cached titles:")
	for i, entry := range entries {
		label := strings.TrimSpace(entry.PrimaryFile)
		if label == "" {
			label = filepath.Base(entry.Directory)
		}
		if label == "" {
			label = entry.Directory
		}
		if label == "" {
			label = "(unknown)"
		}
		extra := ""
		if entry.VideoFileCount > 1 {
			extra = fmt.Sprintf(" (+%d more)", entry.VideoFileCount-1)
		}
		updated := "unknown"
		if !entry.ModifiedAt.IsZero() {
			updated = entry.ModifiedAt.Local().Format(stampLayout)
		}
		fmt.Fprintf(out, "  %d. %s%s — %s (updated %s)\n",
			i+1,
			label,
			extra,
			logging.FormatBytes(entry.SizeBytes),
			updated,
		)
	}
}

func newCacheRemoveCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <number>",
		Short: "Remove a specific cache entry by number",
		Long: `Remove a specific cache entry by its number from 'spindle cache stats'.

Example:
  spindle cache stats      # Shows numbered list of cached rips
  spindle cache remove 2   # Removes entry #2 from the list`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, warn, err := cacheManager(ctx)
			if warn != "" {
				fmt.Fprintln(cmd.OutOrStdout(), warn)
			}
			if err != nil || manager == nil {
				return err
			}

			entryNum, err := parseEntryNumber(args[0])
			if err != nil {
				return err
			}

			entry, err := manager.RemoveEntryByNumber(cmd.Context(), entryNum)
			if err != nil {
				return err
			}

			if ctx.JSONMode() {
				return writeJSON(cmd, map[string]any{
					"removed":    true,
					"entry":      entryNum,
					"size_bytes": entry.SizeBytes,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed cache entry %d (%s)\n", entryNum, logging.FormatBytes(entry.SizeBytes))
			return nil
		},
	}
}

func newCacheClearCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove all cache entries",
		Long:  "Delete all cached rips to free disk space. The cache will be automatically repopulated as new discs are ripped.",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, warn, err := cacheManager(ctx)
			if warn != "" && !ctx.JSONMode() {
				fmt.Fprintln(cmd.OutOrStdout(), warn)
			}
			if err != nil || manager == nil {
				return err
			}

			removed, freedBytes, err := manager.Clear(cmd.Context())
			if err != nil {
				return err
			}
			if removed == 0 {
				if ctx.JSONMode() {
					return writeJSON(cmd, map[string]any{"removed": 0, "freed_bytes": 0})
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Cache is already empty")
				return nil
			}

			if ctx.JSONMode() {
				return writeJSON(cmd, map[string]any{"removed": removed, "freed_bytes": freedBytes})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %d cache entries (%s freed)\n", removed, logging.FormatBytes(freedBytes))
			return nil
		},
	}
}

func resolveCacheTarget(cmd *cobra.Command, ctx *commandContext, arg string, out io.Writer) (string, string, error) {
	cfg, err := ctx.ensureConfig()
	if err != nil {
		return "", "", err
	}
	target, label, warn, err := api.ResolveCacheTarget(cmd.Context(), api.ResolveCacheTargetRequest{
		Config: cfg,
		Arg:    arg,
	})
	if warn != "" {
		fmt.Fprintln(out, warn)
	}
	return target, label, err
}

func cacheManager(ctx *commandContext) (*ripcache.Manager, string, error) {
	cfg, err := ctx.ensureConfig()
	if err != nil {
		return nil, "", err
	}
	logger, err := ctx.newCLILogger(cfg, "cli-cache", true)
	if err != nil {
		return nil, "", err
	}

	return api.OpenRipCacheManagerForCLI(api.OpenCacheResourceRequest{
		Config: cfg,
		Logger: logger,
	})
}
