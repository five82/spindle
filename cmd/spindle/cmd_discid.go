package main

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/discidcache"
)

func newDiscIDCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "discid",
		Short: "Manage the disc ID cache",
	}
	cmd.AddCommand(newDiscIDListCmd(), newDiscIDRemoveCmd(), newDiscIDClearCmd())
	return cmd
}

func newDiscIDListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List cached disc ID mappings",
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := discidcache.Open(cfg.DiscIDCachePath())
			if err != nil {
				return err
			}
			entries := store.List()
			if len(entries) == 0 {
				fmt.Println("No disc ID cache entries")
				return nil
			}

			for i, le := range entries {
				fp := le.Fingerprint
				if len(fp) > 12 {
					fp = fp[:12]
				}
				e := le.Entry
				fmt.Printf("  %d. %s (TMDB %d, %s", i+1, e.Title, e.TMDBID, e.MediaType)
				if e.Season > 0 {
					fmt.Printf(", S%02d", e.Season)
				}
				fmt.Printf(") [%s]\n", fp)
			}
			fmt.Printf("\n%d entries\n", len(entries))
			return nil
		},
	}
}

func newDiscIDRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <number>",
		Short: "Remove a specific disc ID cache entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			num, err := strconv.Atoi(args[0])
			if err != nil || num < 1 {
				return fmt.Errorf("invalid entry number: %s", args[0])
			}

			store, err := discidcache.Open(cfg.DiscIDCachePath())
			if err != nil {
				return err
			}
			entries := store.List()
			if num > len(entries) {
				return fmt.Errorf("entry %d not found (have %d entries)", num, len(entries))
			}

			le := entries[num-1]
			if err := store.Remove(le.Fingerprint); err != nil {
				return err
			}
			fmt.Printf("Removed: %s\n", le.Entry.Title)
			return nil
		},
	}
}

func newDiscIDClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove all disc ID cache entries",
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := discidcache.Open(cfg.DiscIDCachePath())
			if err != nil {
				return err
			}
			if err := store.Clear(); err != nil {
				return err
			}
			fmt.Println("All disc ID cache entries removed")
			return nil
		},
	}
}
