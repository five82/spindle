package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"log/slog"
	"unicode"

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
	"spindle/internal/services"
)

type stageHandler interface {
	Prepare(context.Context, *queue.Item) error
	Execute(context.Context, *queue.Item) error
}

type loggerAware interface {
	SetLogger(*slog.Logger)
}

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

			discLabel, err := getDiscLabel(device)
			if err != nil {
				logger.Warn("failed to get disc label", logging.Error(err))
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

			baseCtx := services.WithItemID(cmd.Context(), item.ID)

			tmdbClient, err := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language)
			if err != nil {
				logger.Warn("tmdb client initialization failed", logging.Error(err))
				return fmt.Errorf("create TMDB client: %w", err)
			}
			scanner := disc.NewScanner(cfg.MakemkvBinary())
			identifier := identification.NewIdentifierWithDependencies(cfg, nil, logger, tmdbClient, scanner, notifications.NewService(cfg))
			ripper := ripping.NewRipper(cfg, store, logger)

			if err := runStage(baseCtx, logger, store, identifier, "identifier", queue.StatusIdentifying, queue.StatusIdentified, item); err != nil {
				return err
			}
			if item.NeedsReview || item.Status == queue.StatusReview {
				return fmt.Errorf("identification requires review: %s", strings.TrimSpace(item.ReviewReason))
			}
			if item.Status == queue.StatusFailed {
				return fmt.Errorf("identification failed: %s", strings.TrimSpace(item.ErrorMessage))
			}

			cacheDir := cacheManager.Path(item)
			if strings.TrimSpace(cacheDir) == "" {
				return fmt.Errorf("rip cache path unavailable")
			}
			if err := os.RemoveAll(cacheDir); err != nil {
				return fmt.Errorf("remove existing rip cache entry: %w", err)
			}

			if err := runStage(baseCtx, logger, store, ripper, "ripper", queue.StatusRipping, queue.StatusRipped, item); err != nil {
				return err
			}
			if item.Status == queue.StatusFailed {
				return fmt.Errorf("ripping failed: %s", strings.TrimSpace(item.ErrorMessage))
			}
			if item.Status == queue.StatusReview {
				return fmt.Errorf("ripping requires review: %s", strings.TrimSpace(item.ReviewReason))
			}

			if _, ok, err := ripcache.LoadMetadata(cacheDir); err != nil {
				return fmt.Errorf("load rip cache metadata: %w", err)
			} else if !ok {
				return fmt.Errorf("rip cache metadata missing; check rip_cache_dir permissions and free space")
			}

			if removed, err := store.Remove(baseCtx, item.ID); err != nil {
				logger.Warn("failed to remove queue item after cache populate", logging.Error(err))
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

func runStage(ctx context.Context, logger *slog.Logger, store *queue.Store, handler stageHandler, name string, processing, done queue.Status, item *queue.Item) error {
	if handler == nil {
		return fmt.Errorf("stage handler unavailable: %s", name)
	}
	stageCtx := services.WithStage(ctx, name)
	stageLogger := logging.WithContext(stageCtx, logger)
	if aware, ok := handler.(loggerAware); ok {
		aware.SetLogger(stageLogger)
	}

	stageLogger.Info(
		"stage started",
		logging.String(logging.FieldEventType, "stage_start"),
		logging.String("processing_status", string(processing)),
		logging.String("disc_title", strings.TrimSpace(item.DiscTitle)),
		logging.String("source_file", strings.TrimSpace(item.SourcePath)),
	)

	setItemProcessingState(item, processing)
	if err := store.Update(stageCtx, item); err != nil {
		return fmt.Errorf("persist processing transition: %w", err)
	}

	if err := handler.Prepare(stageCtx, item); err != nil {
		return handleStageFailure(stageCtx, stageLogger, store, item, err)
	}
	if err := store.Update(stageCtx, item); err != nil {
		return fmt.Errorf("persist stage preparation: %w", err)
	}

	if err := handler.Execute(stageCtx, item); err != nil {
		return handleStageFailure(stageCtx, stageLogger, store, item, err)
	}

	if item.Status == processing || item.Status == "" {
		item.Status = done
	}
	item.LastHeartbeat = nil
	if err := store.Update(stageCtx, item); err != nil {
		return fmt.Errorf("persist stage result: %w", err)
	}

	stageLogger.Info(
		"stage completed",
		logging.String(logging.FieldEventType, "stage_complete"),
		logging.String("next_status", string(item.Status)),
		logging.String("progress_stage", strings.TrimSpace(item.ProgressStage)),
		logging.String("progress_message", strings.TrimSpace(item.ProgressMessage)),
	)

	return nil
}

func handleStageFailure(ctx context.Context, logger *slog.Logger, store *queue.Store, item *queue.Item, stageErr error) error {
	status := queue.StatusFailed
	message := "stage failed"
	if stageErr != nil {
		status = services.FailureStatus(stageErr)
		details := services.Details(stageErr)
		message = strings.TrimSpace(details.Message)
		if message == "" {
			message = strings.TrimSpace(stageErr.Error())
		}
	}
	item.Status = status
	item.ErrorMessage = message
	if status == queue.StatusReview {
		item.ProgressStage = "Needs review"
	} else {
		item.ProgressStage = "Failed"
	}
	item.ProgressMessage = message
	item.ProgressPercent = 0
	item.LastHeartbeat = nil

	logger.Error(
		"stage failed",
		logging.String(logging.FieldEventType, "stage_failure"),
		logging.String("resolved_status", string(status)),
		logging.String("error_message", strings.TrimSpace(message)),
		logging.Error(stageErr),
	)
	if err := store.Update(ctx, item); err != nil {
		logger.Error("failed to persist stage failure", logging.Error(err))
	}
	return stageErr
}

func setItemProcessingState(item *queue.Item, processing queue.Status) {
	now := time.Now().UTC()
	item.Status = processing
	if item.ProgressStage == "" {
		item.ProgressStage = deriveStageLabel(processing)
	}
	if item.ProgressMessage == "" {
		item.ProgressMessage = fmt.Sprintf("%s started", deriveStageLabel(processing))
	}
	item.ProgressPercent = 0
	item.ErrorMessage = ""
	item.LastHeartbeat = &now
}

func deriveStageLabel(status queue.Status) string {
	if status == "" {
		return ""
	}
	parts := strings.Fields(strings.ReplaceAll(string(status), "_", " "))
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(strings.ToLower(part))
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}
