package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

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
  commentary - Run commentary detection on a cached file (troubleshooting)`,
	}

	cacheCmd.AddCommand(newCacheRipCommand(ctx))
	cacheCmd.AddCommand(newCacheStatsCommand(ctx))
	cacheCmd.AddCommand(newCacheProcessCommand(ctx))
	cacheCmd.AddCommand(newCacheRemoveCommand(ctx))
	cacheCmd.AddCommand(newCacheClearCommand(ctx))
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
			if warn != "" {
				fmt.Fprintln(cmd.OutOrStdout(), warn)
			}
			if err != nil || manager == nil {
				return err
			}

			stats, err := manager.Stats(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Entries: %d\n", stats.Entries)
			fmt.Fprintf(out, "Size:   %s / %s\n", humanBytes(stats.TotalBytes), humanBytes(stats.MaxBytes))
			fmt.Fprintf(out, "Disk:   %s free (%.1f%%)\n", humanBytes(int64(stats.FreeBytes)), stats.FreeRatio*100)
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
			humanBytes(entry.SizeBytes),
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

			var entryNum int
			if _, err := fmt.Sscanf(args[0], "%d", &entryNum); err != nil || entryNum < 1 {
				return fmt.Errorf("invalid entry number: %s (must be a positive integer)", args[0])
			}

			stats, err := manager.Stats(cmd.Context())
			if err != nil {
				return err
			}

			if entryNum > len(stats.EntrySummaries) {
				return fmt.Errorf("entry number %d out of range (only %d entries exist)", entryNum, len(stats.EntrySummaries))
			}

			entry := stats.EntrySummaries[entryNum-1]
			if err := os.RemoveAll(entry.Directory); err != nil {
				return fmt.Errorf("remove cache entry: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Removed cache entry %d (%s)\n", entryNum, humanBytes(entry.SizeBytes))
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
			if warn != "" {
				fmt.Fprintln(cmd.OutOrStdout(), warn)
			}
			if err != nil || manager == nil {
				return err
			}

			stats, err := manager.Stats(cmd.Context())
			if err != nil {
				return err
			}

			if stats.Entries == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Cache is already empty")
				return nil
			}

			for _, entry := range stats.EntrySummaries {
				if err := os.RemoveAll(entry.Directory); err != nil {
					return fmt.Errorf("remove cache entry %s: %w", entry.Directory, err)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Removed %d cache entries (%s freed)\n", stats.Entries, humanBytes(stats.TotalBytes))
			return nil
		},
	}
}

func cacheManager(ctx *commandContext) (*ripcache.Manager, string, error) {
	cfg, err := ctx.ensureConfig()
	if err != nil {
		return nil, "", err
	}
	if cfg == nil || !cfg.RipCache.Enabled {
		return nil, "Rip cache is disabled (set rip_cache.enabled = true in config.toml)", nil
	}
	if strings.TrimSpace(cfg.RipCache.Dir) == "" {
		return nil, "Rip cache dir is not configured", nil
	}
	logLevel := ctx.resolvedLogLevel(cfg)
	logger, err := logging.New(logging.Options{
		Level:       logLevel,
		Format:      "console",
		Development: ctx.logDevelopment(cfg),
	})
	if err != nil {
		return nil, "", fmt.Errorf("init logger: %w", err)
	}
	logger = logger.With(logging.String("component", "cli-cache"))
	if err := os.MkdirAll(cfg.RipCache.Dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("ensure cache dir: %w", err)
	}
	return ripcache.NewManager(cfg, logger), "", nil
}

func humanBytes(v int64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%d B", v)
	}
	div := int64(unit)
	exp := 0
	for n := v / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	value := float64(v) / float64(div)
	return fmt.Sprintf("%.1f %ciB", value, "KMGTPEZY"[exp])
}
