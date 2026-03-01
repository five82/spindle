package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/disc"
	"spindle/internal/disc/fingerprint"
	"spindle/internal/identification"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripcache"
	"spindle/internal/ripping"
	"spindle/internal/stageexec"
)

func newCacheRipCommand(ctx *commandContext) *cobra.Command {
	var device string

	cmd := &cobra.Command{
		Use:   "rip [device]",
		Short: "Rip a disc into the rip cache",
		Long: `Rip the current disc and populate the rip cache without moving on to encoding.
This command runs the identification and ripping stages, then exits after the
cache entry is written. If a cache entry already exists for the disc fingerprint,
it is overwritten.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ctx.ensureConfig()
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}

			if len(args) > 0 {
				device = strings.TrimSpace(args[0])
			}
			if device == "" {
				device = strings.TrimSpace(cfg.MakeMKV.OpticalDrive)
			}
			if device == "" {
				return fmt.Errorf("no device specified and no optical_drive configured")
			}
			cfg.MakeMKV.OpticalDrive = device

			if _, err := ctx.dialClient(); err == nil {
				return fmt.Errorf("spindle daemon is running; stop it with: spindle stop")
			}

			logLevel := ctx.resolvedLogLevel(cfg)
			logger, err := logging.New(logging.Options{
				Level:       logLevel,
				Format:      cfg.Logging.Format,
				OutputPaths: []string{"stdout"},
				Development: ctx.logDevelopment(cfg),
			})
			if err != nil {
				return fmt.Errorf("setup logging: %w", err)
			}

			cacheManager := ripcache.NewManager(cfg, logger)
			if cacheManager == nil {
				return fmt.Errorf("rip cache is disabled or misconfigured (set rip_cache.enabled = true and configure rip_cache.dir/max_gib)")
			}

			notifier := notifications.NewService(cfg)

			discLabel, err := disc.ReadLabel(cmd.Context(), device, 10*time.Second)
			if err != nil {
				logger.Warn("failed to get disc label",
					logging.Error(err),
					logging.String(logging.FieldEventType, "disc_label_read_failed"),
					logging.String(logging.FieldErrorHint, "verify disc is inserted and readable"),
					logging.String(logging.FieldImpact, "disc fingerprint used without label"),
				)
				discLabel = ""
			}
			logger.Info("detected disc label", logging.String("label", discLabel))

			fpCtx, fpCancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer fpCancel()
			discFingerprint, err := fingerprint.ComputeTimeout(fpCtx, device, "", 2*time.Minute)
			if err != nil {
				return fmt.Errorf("compute disc fingerprint: %w", err)
			}
			discFingerprint = strings.TrimSpace(discFingerprint)
			if discFingerprint == "" {
				return fmt.Errorf("disc fingerprint missing; verify the disc is readable")
			}

			store, err := queue.Open(cfg)
			if err != nil {
				return fmt.Errorf("open queue store: %w", err)
			}
			defer store.Close()

			item, err := store.NewDisc(cmd.Context(), discLabel, discFingerprint)
			if err != nil {
				return fmt.Errorf("create queue item: %w", err)
			}

			baseCtx := logging.WithItemID(cmd.Context(), item.ID)

			tmdbClient, err := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language)
			if err != nil {
				logger.Warn("tmdb client initialization failed",
					logging.Error(err),
					logging.String(logging.FieldEventType, "tmdb_client_init_failed"),
					logging.String(logging.FieldErrorHint, "verify tmdb_api_key in config"),
					logging.String(logging.FieldImpact, "identification will fail"),
				)
				return fmt.Errorf("create TMDB client: %w", err)
			}
			scanner := disc.NewScanner(cfg.MakemkvBinary())
			identifier := identification.NewIdentifierWithDependencies(cfg, nil, logger, tmdbClient, scanner, notifier)
			ripper := ripping.NewRipper(cfg, store, logger, notifier)

			if err := stageexec.Run(baseCtx, stageexec.Options{
				Logger:     logger,
				Store:      store,
				Notifier:   notifier,
				Handler:    identifier,
				StageName:  "identifier",
				Processing: queue.StatusIdentifying,
				Done:       queue.StatusIdentified,
				Item:       item,
			}); err != nil {
				return err
			}
			if item.Status == queue.StatusFailed {
				if item.NeedsReview {
					return fmt.Errorf("identification requires review: %s", strings.TrimSpace(item.ReviewReason))
				}
				return fmt.Errorf("identification failed: %s", strings.TrimSpace(item.ErrorMessage))
			}

			cacheDir := cacheManager.Path(item)
			if strings.TrimSpace(cacheDir) == "" {
				return fmt.Errorf("rip cache path unavailable")
			}
			if err := os.RemoveAll(cacheDir); err != nil {
				return fmt.Errorf("remove existing rip cache entry: %w", err)
			}

			if err := stageexec.Run(baseCtx, stageexec.Options{
				Logger:     logger,
				Store:      store,
				Notifier:   notifier,
				Handler:    ripper,
				StageName:  "ripper",
				Processing: queue.StatusRipping,
				Done:       queue.StatusRipped,
				Item:       item,
			}); err != nil {
				return err
			}
			if item.Status == queue.StatusFailed {
				if item.NeedsReview {
					return fmt.Errorf("ripping requires review: %s", strings.TrimSpace(item.ReviewReason))
				}
				return fmt.Errorf("ripping failed: %s", strings.TrimSpace(item.ErrorMessage))
			}

			if _, ok, err := ripcache.LoadMetadata(cacheDir); err != nil {
				return fmt.Errorf("load rip cache metadata: %w", err)
			} else if !ok {
				return fmt.Errorf("rip cache metadata missing; check rip_cache_dir permissions and free space")
			}

			if removed, err := store.Remove(baseCtx, item.ID); err != nil {
				logger.Warn("failed to remove queue item after cache populate",
					logging.Error(err),
					logging.String(logging.FieldEventType, "queue_item_remove_failed"),
					logging.String(logging.FieldErrorHint, "run spindle queue clear to clean up"),
					logging.String(logging.FieldImpact, "orphaned queue item may remain"),
				)
			} else if removed {
				logger.Info("removed queue item after cache populate", logging.Int64(logging.FieldItemID, item.ID))
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✅ Rip cache populated: %s\n", cacheDir)
			return nil
		},
	}

	cmd.Flags().StringVarP(&device, "device", "d", "", "Optical device path (default: configured optical_drive)")

	return cmd
}
