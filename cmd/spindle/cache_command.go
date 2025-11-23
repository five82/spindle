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
	}

	cacheCmd.AddCommand(newCacheStatsCommand(ctx))
	cacheCmd.AddCommand(newCachePruneCommand(ctx))

	return cacheCmd
}

func newCacheStatsCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Show rip cache usage",
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
	for _, entry := range entries {
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
		fmt.Fprintf(out, "  - %s%s — %s (updated %s)\n",
			label,
			extra,
			humanBytes(entry.SizeBytes),
			updated,
		)
	}
}

func newCachePruneCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Prune the rip cache now",
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, warn, err := cacheManager(ctx)
			if warn != "" {
				fmt.Fprintln(cmd.OutOrStdout(), warn)
			}
			if err != nil || manager == nil {
				return err
			}
			before, err := manager.Stats(cmd.Context())
			if err != nil {
				return err
			}
			if err := manager.Prune(cmd.Context(), ""); err != nil {
				return err
			}
			after, err := manager.Stats(cmd.Context())
			if err != nil {
				return err
			}
			freed := before.TotalBytes - after.TotalBytes
			if freed <= 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No cache entries pruned")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Pruned %s (now %s / %s)\n", humanBytes(freed), humanBytes(after.TotalBytes), humanBytes(after.MaxBytes))
			return nil
		},
	}
}

func cacheManager(ctx *commandContext) (*ripcache.Manager, string, error) {
	cfg, err := ctx.ensureConfig()
	if err != nil {
		return nil, "", err
	}
	if cfg == nil || !cfg.RipCacheEnabled {
		return nil, "Rip cache is disabled (set rip_cache_enabled = true in config.toml)", nil
	}
	if strings.TrimSpace(cfg.RipCacheDir) == "" {
		return nil, "Rip cache dir is not configured", nil
	}
	logger, err := logging.New(logging.Options{Level: "info", Format: "console"})
	if err != nil {
		return nil, "", fmt.Errorf("init logger: %w", err)
	}
	logger = logger.With(logging.String("component", "cli-cache"))
	if err := os.MkdirAll(cfg.RipCacheDir, 0o755); err != nil {
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
