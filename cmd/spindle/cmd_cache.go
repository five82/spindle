package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/daemonctl"
	"github.com/five82/spindle/internal/discidcache"
	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/fingerprint"
	"github.com/five82/spindle/internal/makemkv"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
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
			cache := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
			if cache.HasCache(fp) {
				fmt.Printf("Disc already cached (fingerprint: %s)\n", truncate(fp, 12))
				return nil
			}

			// MakeMKV scan.
			fmt.Printf("Scanning disc on %s...\n", device)
			discInfo, err := makemkv.Scan(ctx, nil, device,
				time.Duration(cfg.MakeMKV.InfoTimeout)*time.Second,
				cfg.MakeMKV.MinTitleLength)
			if err != nil {
				return fmt.Errorf("makemkv scan: %w", err)
			}

			discTitle := discInfo.Name
			if discTitle == "" {
				discTitle = event.Label
			}
			if discTitle == "" {
				discTitle = "Unknown Disc"
			}

			fmt.Printf("Disc: %s (%d titles)\n", discTitle, len(discInfo.Titles))

			// TMDB identification (for disc ID cache).
			tmdbClient := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language, nil)
			results, searchErr := tmdbClient.SearchMulti(ctx, discTitle)
			if searchErr == nil && len(results) > 0 {
				best := tmdb.SelectBestResult(slog.Default(), results, discTitle, 0, 5)
				if best != nil {
					fmt.Printf("TMDB: %s (%s, ID %d)\n", best.DisplayTitle(), best.Year(), best.ID)
					discIDStore, openErr := discidcache.Open(cfg.DiscIDCachePath(), nil)
					if openErr == nil {
						entry := discidcache.Entry{
							TMDBID:    best.ID,
							MediaType: best.MediaType,
							Title:     best.DisplayTitle(),
							Year:      best.Year(),
						}
						_ = discIDStore.Set(fp, entry)
					}
				}
			}

			// Rip qualifying titles to a temp directory.
			tempDir, err := os.MkdirTemp("", "spindle-rip-*")
			if err != nil {
				return fmt.Errorf("create temp dir: %w", err)
			}
			defer func() { _ = os.RemoveAll(tempDir) }()

			var rippedCount int
			var totalBytes int64
			for i, title := range discInfo.Titles {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if title.Duration.Seconds() < float64(cfg.MakeMKV.MinTitleLength) {
					continue
				}

				fmt.Printf("Ripping title %d/%d (ID %d, %s)...\n",
					i+1, len(discInfo.Titles), title.ID, title.Duration)

				ripErr := makemkv.Rip(ctx, nil, device, title.ID, tempDir,
					time.Duration(cfg.MakeMKV.RipTimeout)*time.Second,
					cfg.MakeMKV.MinTitleLength,
					func(p makemkv.RipProgress) {
						fmt.Printf("\r  Progress: %.0f%%", p.Percent)
					},
				)
				if ripErr != nil {
					return fmt.Errorf("rip title %d: %w", title.ID, ripErr)
				}
				fmt.Println() // newline after progress
				rippedCount++
				totalBytes += title.SizeBytes
			}

			if rippedCount == 0 {
				fmt.Println("No qualifying titles to rip")
				return nil
			}

			// Register in rip cache.
			meta := ripcache.EntryMetadata{
				Fingerprint: fp,
				DiscTitle:   discTitle,
				CachedAt:    time.Now(),
				TitleCount:  rippedCount,
				TotalBytes:  totalBytes,
			}
			if err := cache.Register(fp, tempDir, meta); err != nil {
				return fmt.Errorf("cache registration: %w", err)
			}

			// Prune cache if needed.
			if pruneErr := cache.Prune(); pruneErr != nil {
				fmt.Fprintf(os.Stderr, "%s cache prune failed: %v\n", warnStyle("Warning:"), pruneErr)
			}

			fmt.Printf("\n%s\n", successStyle(fmt.Sprintf("Cached %d titles (%s) for %s",
				rippedCount, formatBytes(totalBytes), discTitle)))
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
				if flagVerbose {
					fmt.Printf("  %d. %s (%d titles, %s, cached %s)\n",
						i+1, e.DiscTitle, e.TitleCount,
						formatBytes(e.TotalBytes), e.CachedAt.Format(time.RFC3339))
					fmt.Printf("     Fingerprint: %s\n", e.Fingerprint)
				} else {
					age := time.Since(e.CachedAt).Truncate(time.Minute)
					fmt.Printf("  %d. %s (%d titles, %s, %s ago)\n",
						i+1, e.DiscTitle, e.TitleCount,
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

			// Build RipSpec from disc ID cache if available.
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
