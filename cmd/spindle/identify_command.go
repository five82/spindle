package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"spindle/internal/api"
	"spindle/internal/logging"
	"spindle/internal/ripspec"
)

func newIdentifyCommand(ctx *commandContext) *cobra.Command {
	var device string

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
			cfg, err := ctx.ensureConfig()
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}

			if len(args) > 0 {
				device = strings.TrimSpace(args[0])
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

			result, err := api.IdentifyDisc(cmd.Context(), api.IdentifyDiscRequest{
				Config: cfg,
				Device: device,
				Logger: logger,
			})
			if err != nil {
				return err
			}
			if result.Item == nil {
				return fmt.Errorf("identification returned no result item")
			}

			fmt.Fprintf(cmd.OutOrStdout(), "🔍 Identifying disc on device: %s\n", result.Device)
			if result.DiscLabel != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "📀 Disc Label: %s\n\n", result.DiscLabel)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "\n")
			}

			assessment := api.AssessIdentifyDisc(result.Item)
			fmt.Fprintf(cmd.OutOrStdout(), "\n📊 Identification Results:\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  Disc Title: %s\n", result.Item.DiscTitle)
			fmt.Fprintf(cmd.OutOrStdout(), "  TMDB Title: %s\n", assessment.TMDBTitle)
			fmt.Fprintf(cmd.OutOrStdout(), "  Year: %s\n", assessment.Year)
			if assessment.Edition != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Edition: %s\n", assessment.Edition)
			}
			if result.Item.ProgressMessage != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Message: %s\n", result.Item.ProgressMessage)
			}
			if assessment.MetadataPresent {
				fmt.Fprintf(cmd.OutOrStdout(), "  Metadata: ✅ Available\n")
				if assessment.LibraryFilename != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  Library Filename: %s\n", assessment.LibraryFilename)
				}
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  Metadata: ❌ None found\n")
			}
			if assessment.ReviewRequired {
				fmt.Fprintf(cmd.OutOrStdout(), "  Review Required: ⚠️  Yes - %s\n", assessment.ReviewReason)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  Review Required: ✅ No\n")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", assessment.OutcomeMessage)

			if summary, err := ripspec.Parse(result.Item.RipSpecData); err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "\n⚠️  Unable to parse rip specification for title fingerprints: %v\n", err)
			} else {
				printRipSpecFingerprints(cmd.OutOrStdout(), summary)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&device, "device", "d", "", "Optical device path (default: configured optical_drive)")

	return cmd
}
