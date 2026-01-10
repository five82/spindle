package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"spindle/internal/queue"
	"spindle/internal/ripcache"
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

			stats, err := manager.Stats(cmd.Context())
			if err != nil {
				return err
			}
			if entryNum > len(stats.EntrySummaries) {
				return fmt.Errorf("entry number %d out of range (only %d entries exist)", entryNum, len(stats.EntrySummaries))
			}

			entry := stats.EntrySummaries[entryNum-1]
			meta, ok, err := ripcache.LoadMetadata(entry.Directory)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("cache entry %d is missing metadata; re-rip to repopulate identification data", entryNum)
			}

			fingerprint := strings.TrimSpace(meta.DiscFingerprint)
			if fingerprint == "" {
				return fmt.Errorf("cache entry %d metadata is missing disc fingerprint", entryNum)
			}
			if strings.TrimSpace(meta.RipSpecData) == "" {
				return fmt.Errorf("cache entry %d metadata is missing rip spec data", entryNum)
			}
			if strings.TrimSpace(meta.MetadataJSON) == "" {
				return fmt.Errorf("cache entry %d metadata is missing TMDB metadata", entryNum)
			}

			discTitle := strings.TrimSpace(meta.DiscTitle)
			if discTitle == "" {
				parsed := queue.MetadataFromJSON(meta.MetadataJSON, "")
				discTitle = strings.TrimSpace(parsed.Title())
			}
			if discTitle == "" {
				discTitle = "Cached Disc"
			}

			cfg, err := ctx.ensureConfig()
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}

			store, err := queue.Open(cfg)
			if err != nil {
				return fmt.Errorf("open queue store: %w", err)
			}
			defer store.Close()

			if existing, err := store.FindByFingerprint(cmd.Context(), fingerprint); err != nil {
				return fmt.Errorf("check existing queue item: %w", err)
			} else if existing != nil && !allowDuplicate {
				return fmt.Errorf("disc fingerprint already queued as item %d (status %s); use --allow-duplicate to add another", existing.ID, existing.Status)
			}

			item, err := store.NewDisc(cmd.Context(), discTitle, fingerprint)
			if err != nil {
				return fmt.Errorf("create queue item: %w", err)
			}

			item.RipSpecData = meta.RipSpecData
			item.MetadataJSON = meta.MetadataJSON
			item.NeedsReview = meta.NeedsReview
			item.ReviewReason = meta.ReviewReason
			if meta.NeedsReview {
				item.Status = queue.StatusFailed
				item.ProgressStage = "Failed"
				item.ProgressPercent = 100
				item.ProgressMessage = strings.TrimSpace(meta.ReviewReason)
				item.ErrorMessage = strings.TrimSpace(meta.ReviewReason)
			} else {
				item.Status = queue.StatusIdentified
				item.ProgressStage = "Identified"
				item.ProgressPercent = 100
				item.ProgressMessage = "Identified from rip cache"
			}

			if err := store.Update(cmd.Context(), item); err != nil {
				return fmt.Errorf("update queue item: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Queued cache entry %d as item %d (%s)\n", entryNum, item.ID, discTitle)
			return nil
		},
	}

	cmd.Flags().BoolVar(&allowDuplicate, "allow-duplicate", false, "Allow multiple queue items with the same disc fingerprint")

	return cmd
}
