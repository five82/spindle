package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"spindle/internal/api"
	"spindle/internal/discidcache"
)

func newDiscIDCommand(ctx *commandContext) *cobra.Command {
	discidCmd := &cobra.Command{
		Use:   "discid",
		Short: "Inspect and manage the disc ID cache",
		Long: `Inspect and manage the disc ID cache.

The disc ID cache stores mappings from Blu-ray disc IDs to TMDB IDs,
allowing previously identified discs to skip KeyDB lookup and TMDB search.

Commands:
  list     - List all cached disc ID mappings
  remove   - Remove a specific entry by number (see 'list' for numbers)
  clear    - Remove all cached entries`,
	}

	discidCmd.AddCommand(newDiscIDListCommand(ctx))
	discidCmd.AddCommand(newDiscIDRemoveCommand(ctx))
	discidCmd.AddCommand(newDiscIDClearCommand(ctx))

	return discidCmd
}

func newDiscIDListCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all cached disc ID mappings",
		Long:  "Display all disc ID to TMDB ID mappings in the cache, sorted by most recently cached.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cache, warn, err := discIDCacheManager(ctx)
			if warn != "" && !ctx.JSONMode() {
				fmt.Fprintln(cmd.OutOrStdout(), warn)
			}
			if err != nil || cache == nil {
				return err
			}

			entries := cache.List()

			if ctx.JSONMode() {
				if entries == nil {
					entries = []discidcache.Entry{}
				}
				return writeJSON(cmd, entries)
			}

			out := cmd.OutOrStdout()

			if len(entries) == 0 {
				fmt.Fprintln(out, "Disc ID cache: empty")
				return nil
			}

			fmt.Fprintf(out, "Disc ID cache: %d entries\n\n", len(entries))

			const stampLayout = "2006-01-02"
			for i, entry := range entries {
				// Format: title with year or season
				titleLine := entry.Title
				if entry.MediaType == "tv" && entry.SeasonNumber > 0 {
					titleLine = fmt.Sprintf("%s - Season %d", entry.Title, entry.SeasonNumber)
				}
				if entry.Edition != "" {
					titleLine = fmt.Sprintf("%s - %s", titleLine, entry.Edition)
				}
				if entry.Year != "" && entry.MediaType == "movie" {
					titleLine = fmt.Sprintf("%s (%s)", titleLine, entry.Year)
				}

				// Format TMDB info
				tmdbInfo := fmt.Sprintf("TMDB: %d (%s)", entry.TMDBID, entry.MediaType)
				if entry.MediaType == "tv" && entry.SeasonNumber > 0 {
					tmdbInfo = fmt.Sprintf("TMDB: %d (%s, S%d)", entry.TMDBID, entry.MediaType, entry.SeasonNumber)
				}

				// Truncate disc ID for display (show first 8 and last 4 characters)
				discIDDisplay := entry.DiscID
				if len(discIDDisplay) > 16 {
					discIDDisplay = entry.DiscID[:8] + "..." + entry.DiscID[len(entry.DiscID)-4:]
				}

				// Format cached date
				cachedAt := "unknown"
				if !entry.CachedAt.IsZero() {
					cachedAt = entry.CachedAt.Local().Format(stampLayout)
				}

				fmt.Fprintf(out, "  %d. %s\n", i+1, titleLine)
				fmt.Fprintf(out, "     %s | Disc: %s | Cached: %s\n\n", tmdbInfo, discIDDisplay, cachedAt)
			}

			return nil
		},
	}
}

func newDiscIDRemoveCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <number>",
		Short: "Remove a specific cache entry by number",
		Long: `Remove a specific cache entry by its number from 'spindle discid list'.

Example:
  spindle discid list        # Shows numbered list of cached mappings
  spindle discid remove 2    # Removes entry #2 from the list`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cache, warn, err := discIDCacheManager(ctx)
			if warn != "" {
				fmt.Fprintln(cmd.OutOrStdout(), warn)
			}
			if err != nil || cache == nil {
				return err
			}

			var entryNum int
			if _, err := fmt.Sscanf(args[0], "%d", &entryNum); err != nil || entryNum < 1 {
				return fmt.Errorf("invalid entry number: %s (must be a positive integer)", args[0])
			}

			entry, err := api.RemoveDiscIDEntryByNumber(cache, entryNum)
			if err != nil {
				return err
			}

			if ctx.JSONMode() {
				return writeJSON(cmd, map[string]any{
					"removed": true,
					"entry":   entryNum,
					"title":   entry.Title,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed disc ID cache entry %d (%s)\n", entryNum, entry.Title)
			return nil
		},
	}
}

func newDiscIDClearCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove all cache entries",
		Long:  "Delete all disc ID to TMDB ID mappings. The cache will be repopulated as discs are identified.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cache, warn, err := discIDCacheManager(ctx)
			if warn != "" && !ctx.JSONMode() {
				fmt.Fprintln(cmd.OutOrStdout(), warn)
			}
			if err != nil || cache == nil {
				return err
			}

			count := cache.Count()
			if count == 0 {
				if ctx.JSONMode() {
					return writeJSON(cmd, map[string]any{"removed": 0})
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Disc ID cache is already empty")
				return nil
			}

			if err := cache.Clear(); err != nil {
				return fmt.Errorf("clear cache: %w", err)
			}

			if ctx.JSONMode() {
				return writeJSON(cmd, map[string]any{"removed": count})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %d disc ID cache entries\n", count)
			return nil
		},
	}
}

func discIDCacheManager(ctx *commandContext) (*discidcache.Cache, string, error) {
	cfg, err := ctx.ensureConfig()
	if err != nil {
		return nil, "", err
	}

	logger, err := ctx.newCLILogger(cfg, "cli-discid", true)
	if err != nil {
		return nil, "", err
	}

	return api.OpenDiscIDCacheForCLI(api.OpenCacheResourceRequest{
		Config: cfg,
		Logger: logger,
	})
}
