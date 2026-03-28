package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/daemonctl"
	"github.com/five82/spindle/internal/discidcache"
	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/fingerprint"
	"github.com/five82/spindle/internal/identify"
	"github.com/five82/spindle/internal/keydb"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripper"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stageexec"
	"github.com/five82/spindle/internal/tmdb"
)

func newCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage the rip cache",
	}
	cmd.AddCommand(
		newCacheRipCmd(),
		newCacheStatsCmd(),
		newCacheProcessCmd(),
		newCacheRemoveCmd(),
		newCacheClearCmd(),
	)
	return cmd
}

func newCacheRipCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "rip [device]",
		Short: "Rip a disc into the rip cache",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			lp, sp := lockPath(), socketPath()
			if daemonctl.IsRunning(lp, sp) {
				return fmt.Errorf("cannot rip while daemon is running")
			}
			if len(args) > 0 {
				device = args[0]
			}
			if device == "" && cfg != nil {
				device = cfg.MakeMKV.OpticalDrive
			}
			if device == "" {
				return fmt.Errorf("no device specified")
			}
			ctx := context.Background()

			// Probe disc for mount point.
			event, err := discmonitor.ProbeDisc(ctx, device)
			if err != nil {
				return fmt.Errorf("probe disc: %w", err)
			}
			if event.MountPath == "" {
				return fmt.Errorf("disc not mounted; mount %s first", device)
			}

			// Generate fingerprint.
			fp, err := fingerprint.Generate(event.MountPath, nil)
			if err != nil {
				return fmt.Errorf("generate fingerprint: %w", err)
			}

			// Check if already cached.
			ripCacheStore := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
			if ripCacheStore.HasCache(fp) {
				fmt.Printf("Disc already cached (fingerprint: %s)\n", truncate(fp, 12))
				return nil
			}

			logger := buildLogger()

			// Open temporary queue store for stage coordination.
			qStore, err := queue.Open(cfg.QueueDBPath())
			if err != nil {
				return fmt.Errorf("open queue: %w", err)
			}
			defer func() { _ = qStore.Close() }()

			// Create queue item.
			discLabel := event.Label
			if discLabel == "" {
				discLabel = "Unknown Disc"
			}
			item, err := qStore.NewDisc(discLabel, fp)
			if err != nil {
				return fmt.Errorf("create queue item: %w", err)
			}
			defer func() { _ = qStore.Remove(item.ID) }()

			// Set up dependencies for identification.
			tmdbClient := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language, nil)

			discIDStore, cacheErr := discidcache.Open(cfg.DiscIDCachePath(), nil)
			if cacheErr != nil {
				logger.Debug("disc ID cache unavailable", "error", cacheErr)
			}

			var keydbCat *keydb.Catalog
			if cat, _, loadErr := keydb.LoadFromFile(cfg.MakeMKV.KeyDBPath, logger); loadErr == nil {
				keydbCat = cat
			}

			// Run identification stage.
			fmt.Printf("Identifying disc on %s...\n", device)
			identifyHandler := identify.New(cfg, qStore, tmdbClient, nil, nil, discIDStore, keydbCat)
			if err := stageexec.Run(ctx, item, stageexec.Options{
				Store:   qStore,
				Handler: identifyHandler,
				Logger:  logger,
			}); err != nil {
				return fmt.Errorf("identification: %w", err)
			}

			// Advance to ripping stage.
			item.Stage = queue.StageRipping
			if err := qStore.Update(item); err != nil {
				return fmt.Errorf("advance stage: %w", err)
			}

			// Run ripping stage.
			fmt.Printf("Ripping disc...\n")
			ripperHandler := ripper.New(cfg, qStore, nil, ripCacheStore, nil)
			if err := stageexec.Run(ctx, item, stageexec.Options{
				Store:   qStore,
				Handler: ripperHandler,
				Logger:  logger,
			}); err != nil {
				return fmt.Errorf("ripping: %w", err)
			}

			// Prune cache if needed.
			if pruneErr := ripCacheStore.Prune(); pruneErr != nil {
				fmt.Fprintf(os.Stderr, "%s cache prune failed: %v\n", warnStyle("Warning:"), pruneErr)
			}

			fmt.Printf("\n%s\n", successStyle(fmt.Sprintf("Cached disc: %s", item.DiscTitle)))
			fmt.Printf("%s %s\n", labelStyle("Fingerprint:"), dimStyle(fp))
			return nil
		},
	}
	cmd.Flags().StringVarP(&device, "device", "d", "", "Optical device path")
	return cmd
}

func newCacheStatsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Show cached entries",
		RunE: func(_ *cobra.Command, _ []string) error {
			store := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
			entries, err := store.List()
			if err != nil {
				return err
			}

			if flagVerbose {
				fmt.Printf("Cache dir: %s\n", cfg.RipCacheDir())
				fmt.Printf("Max size:  %d GiB\n", cfg.RipCache.MaxGiB)
			}

			if len(entries) == 0 {
				fmt.Println("No cached entries")
				return nil
			}

			var totalBytes int64
			for i, e := range entries {
				totalBytes += e.TotalBytes
				titleWord := "titles"
				if e.TitleCount == 1 {
					titleWord = "title"
				}
				if flagVerbose {
					fmt.Printf("  %d. %s (%d %s, %s, cached %s)\n",
						i+1, e.DiscTitle, e.TitleCount, titleWord,
						formatBytes(e.TotalBytes), e.CachedAt.Format(time.RFC3339))
					fmt.Printf("     Fingerprint: %s\n", e.Fingerprint)
				} else {
					age := time.Since(e.CachedAt).Truncate(time.Minute)
					fmt.Printf("  %d. %s (%d %s, %s, %s ago)\n",
						i+1, e.DiscTitle, e.TitleCount, titleWord,
						formatBytes(e.TotalBytes), age)
				}
			}
			fmt.Printf("\n%d entries, %s total\n", len(entries), formatBytes(totalBytes))
			return nil
		},
	}
}

func newCacheProcessCmd() *cobra.Command {
	var allowDuplicate bool
	cmd := &cobra.Command{
		Use:   "process <number>",
		Short: "Queue a cached rip for processing",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			num, err := strconv.Atoi(args[0])
			if err != nil || num < 1 {
				return fmt.Errorf("invalid entry number: %s", args[0])
			}

			rcStore := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
			entries, err := rcStore.List()
			if err != nil {
				return err
			}
			if num > len(entries) {
				return fmt.Errorf("entry %d not found (have %d entries)", num, len(entries))
			}

			entry := entries[num-1]

			// Open queue database.
			qStore, err := queue.Open(cfg.QueueDBPath())
			if err != nil {
				return err
			}
			defer func() { _ = qStore.Close() }()

			// Check for duplicate fingerprint.
			if !allowDuplicate {
				existing, err := qStore.FindByFingerprint(entry.Fingerprint)
				if err != nil {
					return fmt.Errorf("check duplicate: %w", err)
				}
				if existing != nil {
					return fmt.Errorf("fingerprint already queued (item %d, stage %s); use --allow-duplicate to override",
						existing.ID, existing.Stage)
				}
			}

			// Create queue item.
			item, err := qStore.NewDisc(entry.DiscTitle, entry.Fingerprint)
			if err != nil {
				return fmt.Errorf("create queue item: %w", err)
			}

			// Load identification from cache metadata (preferred over disc ID cache).
			if entry.RipSpecData != "" {
				item.RipSpecData = entry.RipSpecData
				item.MetadataJSON = entry.MetadataJSON
				item.Stage = queue.StageRipping
				_ = qStore.Update(item)
			} else {
				// Fallback: reconstruct from disc ID cache (legacy entries).
				discIDStore, openErr := discidcache.Open(cfg.DiscIDCachePath(), nil)
				if openErr == nil {
					if idEntry := discIDStore.Lookup(entry.Fingerprint); idEntry != nil {
						env := ripspec.Envelope{
							Version:     ripspec.CurrentVersion,
							Fingerprint: entry.Fingerprint,
							Metadata: ripspec.Metadata{
								ID:        idEntry.TMDBID,
								Title:     idEntry.Title,
								MediaType: idEntry.MediaType,
								Year:      idEntry.Year,
								Movie:     idEntry.MediaType == "movie",
							},
						}
						data, encErr := env.Encode()
						if encErr == nil {
							item.RipSpecData = data
							item.Stage = queue.StageRipping
							metaJSON, _ := json.Marshal(queue.Metadata{
								ID:        idEntry.TMDBID,
								Title:     idEntry.Title,
								MediaType: idEntry.MediaType,
								Year:      idEntry.Year,
								Movie:     idEntry.MediaType == "movie",
							})
							item.MetadataJSON = string(metaJSON)
							_ = qStore.Update(item)
						}
					}
				}
			}

			fpDisplay := entry.Fingerprint
			if len(fpDisplay) > 12 {
				fpDisplay = fpDisplay[:12]
			}
			fmt.Printf("Queued: %s (item %d, fingerprint: %s)\n",
				entry.DiscTitle, item.ID, fpDisplay)
			return nil
		},
	}
	cmd.Flags().BoolVar(&allowDuplicate, "allow-duplicate", false, "Allow multiple queue items with same fingerprint")
	return cmd
}

func newCacheRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <number>",
		Short: "Remove a specific cache entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			num, err := strconv.Atoi(args[0])
			if err != nil || num < 1 {
				return fmt.Errorf("invalid entry number: %s", args[0])
			}

			store := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
			entries, err := store.List()
			if err != nil {
				return err
			}
			if num > len(entries) {
				return fmt.Errorf("entry %d not found (have %d entries)", num, len(entries))
			}

			entry := entries[num-1]
			if err := store.Remove(entry.Fingerprint); err != nil {
				return err
			}
			fmt.Println(successStyle(fmt.Sprintf("Removed cache entry: %s", entry.DiscTitle)))
			return nil
		},
	}
}

func newCacheClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove all cache entries",
		RunE: func(_ *cobra.Command, _ []string) error {
			store := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
			if err := store.Clear(); err != nil {
				return err
			}
			fmt.Println(successStyle("All cache entries removed"))
			return nil
		},
	}
}
