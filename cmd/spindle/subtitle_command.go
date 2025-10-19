package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/logging"
	"spindle/internal/subtitles"
)

func newGenerateSubtitleCommand(ctx *commandContext) *cobra.Command {
	var outputDir string
	var workDir string

	cmd := &cobra.Command{
		Use:   "gensubtitle <encoded-file>",
		Short: "Generate AI subtitles for an encoded media file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := strings.TrimSpace(args[0])
			if source == "" {
				return fmt.Errorf("source file path is required")
			}
			source, _ = filepath.Abs(source)
			info, err := os.Stat(source)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("source file %q not found", source)
				}
				return fmt.Errorf("stat source: %w", err)
			}
			if info.IsDir() {
				return fmt.Errorf("source path %q is a directory", source)
			}

			cfg, err := ctx.ensureConfig()
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}
			if strings.TrimSpace(cfg.MistralAPIKey) == "" {
				return fmt.Errorf("mistral_api_key is not configured; set it in config.toml or export MISTRAL_API_KEY")
			}

			outDir := strings.TrimSpace(outputDir)
			if outDir == "" {
				outDir = filepath.Dir(source)
			}
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return fmt.Errorf("ensure output directory: %w", err)
			}

			workRoot := strings.TrimSpace(workDir)
			cleanupWorkDir := false
			if workRoot == "" {
				root := cfg.StagingDir
				if root == "" {
					root = os.TempDir()
				}
				tmp, err := os.MkdirTemp(root, "gensubtitle-")
				if err != nil {
					return fmt.Errorf("create work directory: %w", err)
				}
				workRoot = tmp
				cleanupWorkDir = true
			}
			if err := os.MkdirAll(workRoot, 0o755); err != nil {
				if cleanupWorkDir {
					_ = os.RemoveAll(workRoot)
				}
				return fmt.Errorf("ensure work directory: %w", err)
			}
			if cleanupWorkDir {
				defer os.RemoveAll(workRoot)
			}

			logger, err := logging.New(logging.Options{
				Level:       cfg.LogLevel,
				Format:      cfg.LogFormat,
				OutputPaths: []string{"stdout"},
				Development: false,
			})
			if err != nil {
				return fmt.Errorf("init subtitle logger: %w", err)
			}
			client := subtitles.NewMistralClient(cfg.MistralAPIKey)
			service := subtitles.NewService(cfg, client, logger)

			result, err := service.Generate(cmd.Context(), subtitles.GenerateRequest{
				SourcePath: source,
				WorkDir:    filepath.Join(workRoot, "work"),
				OutputDir:  outDir,
			})
			if err != nil {
				return fmt.Errorf("subtitle generation failed: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Generated subtitle: %s (segments: %d, duration: %s)\n",
				result.SubtitlePath, result.SegmentCount, result.Duration.Round(time.Second))
			return nil
		},
	}

	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "Output directory for the generated subtitle (default: alongside source file)")
	cmd.Flags().StringVar(&workDir, "work-dir", "", "Working directory for intermediate files (default: temporary directory under staging_dir)")

	return cmd
}
