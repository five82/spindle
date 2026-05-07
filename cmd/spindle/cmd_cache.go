package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/daemonctl"
	"github.com/five82/spindle/internal/discidcache"
	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/fingerprint"
	"github.com/five82/spindle/internal/identify"
	"github.com/five82/spindle/internal/keydb"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/queueaccess"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripper"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
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
	var selectTitle bool
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

			// Open a temporary queue store for one-shot stage coordination.
			tempQueueDir, err := os.MkdirTemp("", "spindle-cache-rip-queue-*")
			if err != nil {
				return fmt.Errorf("create temporary queue: %w", err)
			}
			defer func() { _ = os.RemoveAll(tempQueueDir) }()
			qStore, err := queue.Open(filepath.Join(tempQueueDir, "queue.db"))
			if err != nil {
				return fmt.Errorf("open temporary queue: %w", err)
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
			defer func() {
				if root, err := item.StagingRoot(cfg.Paths.StagingDir); err == nil {
					_ = os.RemoveAll(root)
				}
			}()

			executeOneShotStage := func(handler stage.Handler) error {
				itemLogger := logger.With("item_id", item.ID)
				itemLogger.Info("one-shot stage execution started",
					"decision_type", logs.DecisionStageExecution,
					"decision_result", "started",
					"decision_reason", fmt.Sprintf("one-shot execution of %s", item.Stage),
					"stage", item.Stage,
					"disc_title", item.DiscTitle,
				)

				res, err := stage.ExecuteWorkflowStage(ctx, item, stage.WorkflowOptions{
					Store:   qStore,
					Handler: handler,
					Logger:  logger,
					Stage:   item.Stage,
					OneShot: true,
				})
				if err != nil || res.UserStopped {
					return err
				}

				itemLogger.Info("one-shot stage execution completed",
					"decision_type", logs.DecisionStageExecution,
					"decision_result", "completed",
					"decision_reason", string(item.Stage),
					"stage", item.Stage,
				)
				return nil
			}

			// Set up dependencies for identification.
			tmdbClient := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language, nil)

			discIDStore, cacheErr := discidcache.Open(cfg.DiscIDCachePath(), nil)
			if cacheErr != nil {
				logger.Debug("disc ID cache unavailable", "error", cacheErr)
			}

			var keydbCat *keydb.Catalog
			if cat, _, loadErr := keydb.LoadOrDownload(ctx, cfg.MakeMKV.KeyDBPath, cfg.MakeMKV.KeyDBDownloadURL,
				cfg.MakeMKV.KeyDBTimeout(), logger); loadErr == nil {
				keydbCat = cat
			}

			// Run identification stage.
			fmt.Printf("Identifying disc on %s...\n", device)
			identifyHandler := identify.New(cfg, tmdbClient, nil, discIDStore, keydbCat)
			if err := executeOneShotStage(identifyHandler); err != nil {
				return fmt.Errorf("identification: %w", err)
			}

			// Interactive title selection when --title flag is set.
			titleOverride := ripper.NoTitleOverride
			if selectTitle {
				env, parseErr := ripspec.Parse(item.RipSpecData)
				if parseErr != nil {
					return fmt.Errorf("parse ripspec for title selection: %w", parseErr)
				}

				// Filter candidates by MinTitleLength (same as movie selection logic).
				var candidates []ripspec.Title
				for _, t := range env.Titles {
					if t.Duration >= cfg.MakeMKV.MinTitleLength {
						candidates = append(candidates, t)
					}
				}

				if len(candidates) == 0 {
					return fmt.Errorf("no titles above minimum duration (%ds)", cfg.MakeMKV.MinTitleLength)
				}

				if len(candidates) == 1 {
					titleOverride = candidates[0].ID
					dur := time.Duration(candidates[0].Duration) * time.Second
					fmt.Printf("Only one candidate title: %d (%s)\n", candidates[0].ID, dur.Truncate(time.Second))
				} else {
					fmt.Printf("\nCandidate titles:\n")
					for _, t := range candidates {
						dur := time.Duration(t.Duration) * time.Second
						line := fmt.Sprintf("  Title %d: %s", t.ID, dur.Truncate(time.Second))
						if t.Name != "" {
							line += fmt.Sprintf(" (%s)", t.Name)
						}
						fmt.Println(line)
					}

					// Build lookup set for validation.
					validIDs := make(map[int]bool, len(candidates))
					for _, t := range candidates {
						validIDs[t.ID] = true
					}

					fmt.Printf("\nEnter title ID to rip: ")
					scanner := bufio.NewScanner(os.Stdin)
					if !scanner.Scan() {
						return fmt.Errorf("no input received")
					}
					input := strings.TrimSpace(scanner.Text())
					chosen, err := strconv.Atoi(input)
					if err != nil {
						return fmt.Errorf("invalid title ID %q: %w", input, err)
					}
					if !validIDs[chosen] {
						var ids []int
						for _, t := range candidates {
							ids = append(ids, t.ID)
						}
						return fmt.Errorf("title %d is not a candidate; valid IDs: %v", chosen, ids)
					}
					titleOverride = chosen
				}
				fmt.Println()
			}

			// Advance to ripping stage.
			if err := qStore.MoveToStage(item, queue.StageRipping); err != nil {
				return fmt.Errorf("advance stage: %w", err)
			}

			// Run ripping stage.
			fmt.Printf("Ripping disc...\n")
			ripperHandler := ripper.New(cfg, nil, ripCacheStore, nil, titleOverride)
			if err := executeOneShotStage(ripperHandler); err != nil {
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
	cmd.Flags().BoolVar(&selectTitle, "title", false, "Interactively select which title to rip")
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

func cacheEntryByNumber(num int) (ripcache.EntryMetadata, error) {
	store := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
	entries, err := store.List()
	if err != nil {
		return ripcache.EntryMetadata{}, err
	}
	if num > len(entries) {
		return ripcache.EntryMetadata{}, fmt.Errorf("entry %d not found (have %d entries)", num, len(entries))
	}
	return entries[num-1], nil
}

func parseCacheEntryNumber(arg string) (int, error) {
	num, err := strconv.Atoi(arg)
	if err != nil || num < 1 {
		return 0, fmt.Errorf("invalid entry number: %s", arg)
	}
	return num, nil
}

func newCacheProcessCmd() *cobra.Command {
	var allowDuplicate bool
	cmd := &cobra.Command{
		Use:   "process <number>",
		Short: "Queue a cached rip for processing",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			num, err := parseCacheEntryNumber(args[0])
			if err != nil {
				return err
			}

			entry, err := cacheEntryByNumber(num)
			if err != nil {
				return err
			}

			logger := buildLogger()

			// Load identification from cache metadata.
			if entry.RipSpecData == "" {
				return fmt.Errorf("cache entry missing identification data; re-cache with 'spindle cache rip'")
			}

			acc, err := openQueueAccess()
			if err != nil {
				return err
			}
			item, err := acc.EnqueueCached(queueaccess.EnqueueCachedRequest{
				DiscTitle:      entry.DiscTitle,
				Fingerprint:    entry.Fingerprint,
				RipSpecData:    entry.RipSpecData,
				MetadataJSON:   entry.MetadataJSON,
				AllowDuplicate: allowDuplicate,
			})
			if err != nil {
				return err
			}

			notifier := notify.New(cfg.Notifications.NtfyTopic, cfg.Notifications.RequestTimeout, logger)
			_ = notify.SendLogged(context.Background(), notifier, logger, notify.EventItemQueued,
				"Queued: "+item.DisplayTitle(),
				"Accepted for processing from rip cache.",
				"item_id", item.ID,
			)

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
			num, err := parseCacheEntryNumber(args[0])
			if err != nil {
				return err
			}

			entry, err := cacheEntryByNumber(num)
			if err != nil {
				return err
			}

			store := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
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
