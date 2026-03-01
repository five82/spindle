package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/api"
)

func newGenerateSubtitleCommand(ctx *commandContext) *cobra.Command {
	var outputDir string
	var workDir string
	var fetchForced bool
	var external bool

	cmd := &cobra.Command{
		Use:   "gensubtitle <encoded-file>",
		Short: "Create subtitles for an encoded media file (WhisperX transcription)",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("provide the path to the encoded media file. Example: spindle gensubtitle /path/to/video.mkv\nRun spindle gensubtitle --help for more details")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ctx.ensureConfig()
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}
			logger, err := ctx.newCLILogger(cfg, "", false)
			if err != nil {
				return err
			}

			result, err := api.GenerateSubtitlesForFile(cmd.Context(), api.GenerateSubtitlesRequest{
				Config:      cfg,
				Logger:      logger,
				SourcePath:  strings.TrimSpace(args[0]),
				OutputDir:   outputDir,
				WorkDir:     workDir,
				FetchForced: fetchForced,
				External:    external,
			})
			if err != nil {
				return err
			}

			if result.Muxed {
				fmt.Fprintf(cmd.OutOrStdout(), "Muxed %d subtitle track(s) into %s (source: %s, segments: %d, duration: %s)\n",
					result.MuxedTrackCount, filepath.Base(result.SourcePath), result.Source, result.SegmentCount, result.Duration.Round(time.Second))
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Generated subtitle: %s (source: %s, segments: %d, duration: %s)\n",
				result.SubtitlePath, result.Source, result.SegmentCount, result.Duration.Round(time.Second))
			if result.ForcedSubtitlePath != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Generated forced subtitle: %s\n", result.ForcedSubtitlePath)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "Output directory for the generated subtitle (default: alongside source file)")
	cmd.Flags().StringVar(&workDir, "work-dir", "", "Working directory for intermediate files (default: temporary directory under staging_dir)")
	cmd.Flags().BoolVar(&fetchForced, "fetch-forced", false, "Also search OpenSubtitles for forced (foreign-parts-only) subtitles")
	cmd.Flags().BoolVar(&external, "external", false, "Create external SRT sidecar instead of muxing into MKV")

	return cmd
}
