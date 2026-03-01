package main

import (
	"encoding/json"
	"fmt"
	"regexp"
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

			year := extractYearFromMetadata(result.Item.MetadataJSON)
			tmdbTitle := extractTitleFromMetadata(result.Item.MetadataJSON)
			edition := extractEditionFromMetadata(result.Item.MetadataJSON)
			fmt.Fprintf(cmd.OutOrStdout(), "\n📊 Identification Results:\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  Disc Title: %s\n", result.Item.DiscTitle)
			fmt.Fprintf(cmd.OutOrStdout(), "  TMDB Title: %s\n", tmdbTitle)
			fmt.Fprintf(cmd.OutOrStdout(), "  Year: %s\n", year)
			if edition != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Edition: %s\n", edition)
			}
			if result.Item.ProgressMessage != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Message: %s\n", result.Item.ProgressMessage)
			}
			if result.Item.MetadataJSON != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Metadata: ✅ Available\n")
				if filename := extractFilenameFromMetadata(result.Item.MetadataJSON); filename != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  Library Filename: %s.mkv\n", filename)
				} else if year != "Unknown" && tmdbTitle != "Unknown" {
					fmt.Fprintf(cmd.OutOrStdout(), "  Library Filename: %s (%s).mkv\n", tmdbTitle, year)
				}
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  Metadata: ❌ None found\n")
			}
			if result.Item.NeedsReview {
				fmt.Fprintf(cmd.OutOrStdout(), "  Review Required: ⚠️  Yes - %s\n", result.Item.ReviewReason)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  Review Required: ✅ No\n")
			}

			if result.Item.MetadataJSON != "" && !result.Item.NeedsReview {
				fmt.Fprintf(cmd.OutOrStdout(), "\n🎬 Identification successful! Disc would proceed to ripping stage.\n")
			} else if result.Item.NeedsReview {
				fmt.Fprintf(cmd.OutOrStdout(), "\n⚠️  Identification requires manual review. Check the logs above for details.\n")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "\n❌ Identification failed. Check the logs above for details.\n")
			}

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

// metadataField extracts a string field from JSON metadata.
// Returns the field value, or fallback if the JSON is empty, invalid, or the field is missing.
func metadataField(metadataJSON, field, fallback string) string {
	if metadataJSON == "" {
		return fallback
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return fallback
	}
	value, ok := metadata[field].(string)
	if !ok || value == "" {
		return fallback
	}
	return value
}

// yearPattern matches a 4-digit year at the start of a string.
var yearPattern = regexp.MustCompile(`^\d{4}`)

// extractYearFromMetadata extracts the year from the release_date field.
func extractYearFromMetadata(metadataJSON string) string {
	releaseDate := metadataField(metadataJSON, "release_date", "")
	if releaseDate == "" {
		return "Unknown"
	}
	if match := yearPattern.FindString(releaseDate); match != "" {
		return match
	}
	return "Unknown"
}

// extractTitleFromMetadata extracts the title field.
func extractTitleFromMetadata(metadataJSON string) string {
	return metadataField(metadataJSON, "title", "Unknown")
}

// extractFilenameFromMetadata extracts the filename field.
func extractFilenameFromMetadata(metadataJSON string) string {
	return metadataField(metadataJSON, "filename", "")
}

// extractEditionFromMetadata extracts the edition field.
func extractEditionFromMetadata(metadataJSON string) string {
	return metadataField(metadataJSON, "edition", "")
}
