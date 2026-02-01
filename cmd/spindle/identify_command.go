package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/disc"
	"spindle/internal/disc/fingerprint"
	"spindle/internal/identification"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/queue"
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
				device = cfg.MakeMKV.OpticalDrive
			}
			if device == "" {
				return fmt.Errorf("no device specified and no optical_drive configured")
			}
			cfg.MakeMKV.OpticalDrive = device

			// Setup logging
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

			// Create components for identification
			tmdbClient, err := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language)
			if err != nil {
				logger.Warn("tmdb client initialization failed",
					logging.Error(err),
					logging.String(logging.FieldEventType, "tmdb_client_init_failed"),
					logging.String(logging.FieldErrorHint, "verify tmdb_api_key in config"),
					logging.String(logging.FieldImpact, "identification cannot proceed"),
				)
				return fmt.Errorf("create TMDB client: %w", err)
			}

			// Create a scanner
			scanner := disc.NewScanner(cfg.MakemkvBinary())

			// Create identifier with our components
			identifier := identification.NewIdentifierWithDependencies(
				cfg, nil, logger, tmdbClient, scanner, nil,
			)

			// Get disc label like the daemon does
			logger.Debug("getting disc label", logging.String("device", device))
			discLabel, err := getDiscLabel(device)
			if err != nil {
				logger.Warn("failed to get disc label",
					logging.Error(err),
					logging.String(logging.FieldEventType, "disc_label_read_failed"),
					logging.String(logging.FieldErrorHint, "verify disc is inserted and readable"),
					logging.String(logging.FieldImpact, "identification may use fallback title"),
				)
				discLabel = ""
			} else {
				logger.Debug("disc label retrieved", logging.String("device", device), logging.String("label", discLabel))
			}
			logger.Info("detected disc label", logging.String("label", discLabel))

			// Create a mock queue item for identification with the same disc label as daemon
			item := &queue.Item{
				DiscTitle:  discLabel,
				SourcePath: "",
				Status:     queue.StatusPending,
			}

			fmt.Fprintf(cmd.OutOrStdout(), "🔍 Identifying disc on device: %s\n", device)
			if discLabel != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "📀 Disc Label: %s\n\n", discLabel)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "\n")
			}

			// Set up context with timeout
			identifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			// Pre-compute fingerprint so validation can pass even if MakeMKV omits it
			fingerprintTimeout := 2 * time.Minute
			computedFingerprint, fpErr := fingerprint.ComputeTimeout(identifyCtx, device, "", fingerprintTimeout)
			if fpErr != nil {
				logger.Warn("fingerprint computation failed",
					logging.Error(fpErr),
					logging.String(logging.FieldEventType, "fingerprint_compute_failed"),
					logging.String(logging.FieldErrorHint, "verify disc is readable and not copy-protected"),
					logging.String(logging.FieldImpact, "rip cache lookup will not work"),
				)
			} else if strings.TrimSpace(computedFingerprint) != "" {
				item.DiscFingerprint = strings.TrimSpace(computedFingerprint)
				logger.Info("fingerprint computed", logging.String("fingerprint", item.DiscFingerprint))
			}

			// Run preparation
			if err := identifier.Prepare(identifyCtx, item); err != nil {
				return fmt.Errorf("prepare identification: %w", err)
			}

			// Run identification
			if err := identifier.Execute(identifyCtx, item); err != nil {
				return fmt.Errorf("execute identification: %w", err)
			}

			// Display results
			year := extractYearFromMetadata(item.MetadataJSON)
			tmdbTitle := extractTitleFromMetadata(item.MetadataJSON)
			edition := extractEditionFromMetadata(item.MetadataJSON)
			fmt.Fprintf(cmd.OutOrStdout(), "\n📊 Identification Results:\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  Disc Title: %s\n", item.DiscTitle)
			fmt.Fprintf(cmd.OutOrStdout(), "  TMDB Title: %s\n", tmdbTitle)
			fmt.Fprintf(cmd.OutOrStdout(), "  Year: %s\n", year)
			if edition != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Edition: %s\n", edition)
			}
			if item.ProgressMessage != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Message: %s\n", item.ProgressMessage)
			}
			if item.MetadataJSON != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Metadata: ✅ Available\n")
				if filename := extractFilenameFromMetadata(item.MetadataJSON); filename != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  Library Filename: %s.mkv\n", filename)
				} else if year != "Unknown" && tmdbTitle != "Unknown" {
					fmt.Fprintf(cmd.OutOrStdout(), "  Library Filename: %s (%s).mkv\n", tmdbTitle, year)
				}
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

			if summary, err := parseRipSpecSummary(item.RipSpecData); err != nil {
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

// extractYearFromMetadata extracts the year from TMDB metadata
func extractYearFromMetadata(metadataJSON string) string {
	if metadataJSON == "" {
		return "Unknown"
	}

	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return "Unknown"
	}

	releaseDate, ok := metadata["release_date"].(string)
	if !ok || releaseDate == "" {
		return "Unknown"
	}

	// Extract year from YYYY-MM-DD format
	yearPattern := regexp.MustCompile(`^\d{4}`)
	if match := yearPattern.FindString(releaseDate); match != "" {
		return match
	}

	return "Unknown"
}

// extractTitleFromMetadata extracts the title from TMDB metadata
func extractTitleFromMetadata(metadataJSON string) string {
	if metadataJSON == "" {
		return "Unknown"
	}

	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return "Unknown"
	}

	title, ok := metadata["title"].(string)
	if !ok || title == "" {
		return "Unknown"
	}

	return title
}

// extractFilenameFromMetadata extracts the computed filename from metadata
func extractFilenameFromMetadata(metadataJSON string) string {
	if metadataJSON == "" {
		return ""
	}

	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return ""
	}

	filename, ok := metadata["filename"].(string)
	if !ok {
		return ""
	}

	return filename
}

// extractEditionFromMetadata extracts the edition label from metadata
func extractEditionFromMetadata(metadataJSON string) string {
	if metadataJSON == "" {
		return ""
	}

	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return ""
	}

	edition, ok := metadata["edition"].(string)
	if !ok {
		return ""
	}

	return edition
}

// getDiscLabel gets the disc label using lsblk, same as the daemon
func getDiscLabel(device string) (string, error) {
	if device == "" {
		return "", fmt.Errorf("no device specified")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, "lsblk", "-P", "-o", "LABEL,FSTYPE", device).Output()
	if err != nil {
		return "", fmt.Errorf("failed to run lsblk: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse lsblk output format: LABEL="label" FSTYPE="filesystem"
		parts := strings.Fields(line)
		var label string
		var fstype string

		for _, part := range parts {
			if strings.HasPrefix(part, "LABEL=") {
				label = strings.Trim(part[6:], `"`)
			} else if strings.HasPrefix(part, "FSTYPE=") {
				fstype = strings.Trim(part[7:], `"`)
			}
		}

		// Return the first non-empty label with a filesystem type
		if label != "" && fstype != "" {
			return label, nil
		}
	}

	return "", fmt.Errorf("no disc label found")
}
