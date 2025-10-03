package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"spindle/internal/disc"
	"spindle/internal/identification"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/queue"
)

func newIdentifyCommand(ctx *commandContext) *cobra.Command {
	var device string
	var verbose bool

	cmd := &cobra.Command{
		Use:   "identify [device]",
		Short: "Identify a disc and show TMDB matching details",
		Long: `Identify a disc using MakeMKV and bd_info scanning, then search TMDB for matches.
This command is useful for troubleshooting disc identification issues without
affecting the processing queue. It shows detailed logging of the TMDB query
process including search parameters, results, and confidence scoring.

Examples:
  spindle identify                    # Use configured optical drive
  spindle identify /dev/sr0           # Use specific device
  spindle identify --verbose          # Show detailed debugging output`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load configuration
			cfg, err := ctx.ensureConfig()
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}

			// Override device if provided
			if len(args) > 0 {
				device = strings.TrimSpace(args[0])
			}
			if device == "" {
				device = cfg.OpticalDrive
			}
			if device == "" {
				return fmt.Errorf("no device specified and no optical_drive configured")
			}

			// Setup logging
			logLevel := "info"
			if verbose {
				logLevel = "debug"
			}
			logger, err := logging.New(logging.Options{
				Level:       logLevel,
				Format:      cfg.LogFormat,
				OutputPaths: []string{"stdout"},
				Development: verbose,
			})
			if err != nil {
				return fmt.Errorf("setup logging: %w", err)
			}
			defer func() { _ = logger.Sync() }()

			// Create components for identification
			tmdbClient, err := tmdb.New(cfg.TMDBAPIKey, cfg.TMDBBaseURL, cfg.TMDBLanguage)
			if err != nil {
				logger.Warn("tmdb client initialization failed", zap.Error(err))
				return fmt.Errorf("create TMDB client: %w", err)
			}

			// Create a scanner
			scanner := disc.NewScanner(cfg.MakemkvBinary())

			// Create identifier with our components
			identifier := identification.NewIdentifierWithDependencies(
				cfg, nil, logger, tmdbClient, scanner, nil,
			)

			// Create a mock queue item for identification
			item := &queue.Item{
				DiscTitle:  "",
				SourcePath: "",
				Status:     queue.StatusPending,
			}

			fmt.Fprintf(cmd.OutOrStdout(), "🔍 Identifying disc on device: %s\n\n", device)

			// Set up context with timeout
			identifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			// Run preparation
			if err := identifier.Prepare(identifyCtx, item); err != nil {
				return fmt.Errorf("prepare identification: %w", err)
			}

			// Run identification
			if err := identifier.Execute(identifyCtx, item); err != nil {
				return fmt.Errorf("execute identification: %w", err)
			}

			// Display results
			fmt.Fprintf(cmd.OutOrStdout(), "\n📊 Identification Results:\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  Disc Title: %s\n", item.DiscTitle)
			fmt.Fprintf(cmd.OutOrStdout(), "  Status: %s\n", item.Status)
			if item.ProgressMessage != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Message: %s\n", item.ProgressMessage)
			}
			if item.MetadataJSON != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Metadata: ✅ Available\n")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  Metadata: ❌ None found\n")
			}
			if item.NeedsReview {
				fmt.Fprintf(cmd.OutOrStdout(), "  Review Required: ⚠️  Yes - %s\n", item.ReviewReason)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  Review Required: ✅ No\n")
			}

			if item.MetadataJSON != "" && !item.NeedsReview {
				fmt.Fprintf(cmd.OutOrStdout(), "\n🎬 Identification successful! Disc would proceed to ripping stage.\n")
			} else if item.NeedsReview {
				fmt.Fprintf(cmd.OutOrStdout(), "\n⚠️  Identification requires manual review. Check the logs above for details.\n")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "\n❌ Identification failed. Check the logs above for details.\n")
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&device, "device", "d", "", "Optical device path (default: configured optical_drive)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose debug output")

	return cmd
}
