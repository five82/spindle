package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/identification"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/subtitles"
)

func newGenerateSubtitleCommand(ctx *commandContext) *cobra.Command {
	var outputDir string
	var workDir string
	var forceAI bool
	var openSubtitlesOnly bool
	var fetchForced bool

	cmd := &cobra.Command{
		Use:   "gensubtitle <encoded-file>",
		Short: "Create subtitles for an encoded media file (OpenSubtitles/WhisperX)",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("provide the path to the encoded media file. Example: spindle gensubtitle /path/to/video.mkv\nRun spindle gensubtitle --help for more details")
			}
			return nil
		},
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
				root := cfg.Paths.StagingDir
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

			logLevel := ctx.resolvedLogLevel(cfg)
			logger, err := logging.New(logging.Options{
				Level:       logLevel,
				Format:      cfg.Logging.Format,
				OutputPaths: []string{"stdout"},
				Development: ctx.logDevelopment(cfg),
			})
			if err != nil {
				return fmt.Errorf("init subtitle logger: %w", err)
			}
			service := subtitles.NewService(cfg, logger)

			baseName := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
			inferredTitle, inferredYear := splitTitleAndYear(baseName)
			if inferredTitle == "" {
				inferredTitle = baseName
			}
			ctxMeta := subtitles.SubtitleContext{Title: inferredTitle, MediaType: "movie", Year: inferredYear}
			if lang := strings.TrimSpace(cfg.TMDB.Language); lang != "" {
				ctxMeta.Language = strings.ToLower(strings.SplitN(lang, "-", 2)[0])
			}

			if forceAI {
				if logger != nil {
					logger.Info("forceai flag enabled; skipping opensubtitles lookup and tmdb identification")
				}
			} else {
				openSubsReady, disabledReason := openSubtitlesReady(cfg)
				if openSubsReady {
					if match := lookupTMDBMetadata(cmd.Context(), cfg, logger, inferredTitle, inferredYear); match != nil {
						ctxMeta.TMDBID = match.TMDBID
						ctxMeta.MediaType = match.MediaType
						if match.Title != "" {
							ctxMeta.Title = match.Title
						}
						if match.Year != "" {
							ctxMeta.Year = match.Year
						}
						if logger != nil {
							logger.Info("tmdb metadata attached",
								logging.Int64("tmdb_id", match.TMDBID),
								logging.String("title", ctxMeta.Title),
								logging.String("year", ctxMeta.Year),
								logging.String("media_type", ctxMeta.MediaType),
							)
						}
					} else if logger != nil {
						logger.Info("tmdb lookup skipped: no confident match", logging.String("title", inferredTitle))
					}
				} else if logger != nil {
					logger.Info("opensubtitles download disabled", logging.String("reason", disabledReason))
				}
			}
			languages := append([]string(nil), cfg.Subtitles.OpenSubtitlesLanguages...)
			result, err := service.Generate(cmd.Context(), subtitles.GenerateRequest{
				SourcePath:        source,
				WorkDir:           filepath.Join(workRoot, "work"),
				OutputDir:         outDir,
				BaseName:          baseName,
				ForceAI:           forceAI,
				OpenSubtitlesOnly: openSubtitlesOnly,
				FetchForced:       fetchForced,
				Context:           ctxMeta,
				Languages:         languages,
			})
			if err != nil {
				return fmt.Errorf("subtitle generation failed: %w", err)
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
	cmd.Flags().BoolVar(&forceAI, "forceai", false, "Force WhisperX transcription and skip OpenSubtitles downloads")
	cmd.Flags().BoolVar(&openSubtitlesOnly, "opensubtitles-only", false, "Require OpenSubtitles match; fail instead of falling back to WhisperX (for troubleshooting)")
	cmd.Flags().BoolVar(&fetchForced, "fetch-forced", false, "Also search OpenSubtitles for forced (foreign-parts-only) subtitles")

	return cmd
}

func lookupTMDBMetadata(ctx context.Context, cfg *config.Config, logger *slog.Logger, title, year string) *identification.LookupMatch {
	if cfg == nil || strings.TrimSpace(cfg.TMDB.APIKey) == "" {
		return nil
	}
	client, err := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language)
	if err != nil {
		if logger != nil {
			logger.Warn("tmdb client init failed",
				logging.Error(err),
				logging.String(logging.FieldEventType, "tmdb_client_init_failed"),
				logging.String(logging.FieldErrorHint, "verify tmdb_api_key in config"),
				logging.String(logging.FieldImpact, "subtitle context will lack TMDB metadata"),
			)
		}
		return nil
	}
	opts := tmdb.SearchOptions{}
	if year != "" {
		if parsed, parseErr := strconv.Atoi(year); parseErr == nil {
			opts.Year = parsed
		}
	}
	match, err := identification.LookupTMDBByTitle(ctx, client, logger, title, opts)
	if err != nil {
		if logger != nil {
			logger.Warn("tmdb lookup failed",
				logging.Error(err),
				logging.String("title", title),
				logging.String(logging.FieldEventType, "tmdb_lookup_failed"),
				logging.String(logging.FieldErrorHint, "verify title format or TMDB availability"),
				logging.String(logging.FieldImpact, "subtitle context will lack TMDB metadata"),
			)
		}
		return nil
	}
	if match == nil && logger != nil {
		logger.Info("tmdb lookup returned no confident match", logging.String("title", title))
	}
	return match
}

func splitTitleAndYear(base string) (string, string) {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return "", ""
	}
	if idx := strings.LastIndex(trimmed, "("); idx != -1 && strings.HasSuffix(trimmed, ")") {
		candidate := strings.TrimSpace(trimmed[idx+1 : len(trimmed)-1])
		if len(candidate) == 4 {
			allDigits := true
			for _, r := range candidate {
				if !unicode.IsDigit(r) {
					allDigits = false
					break
				}
			}
			if allDigits {
				title := strings.TrimSpace(trimmed[:idx])
				return title, candidate
			}
		}
	}
	return trimmed, ""
}

func openSubtitlesReady(cfg *config.Config) (bool, string) {
	if cfg == nil {
		return false, "configuration unavailable"
	}
	if !cfg.Subtitles.OpenSubtitlesEnabled {
		return false, "opensubtitles_enabled is false"
	}
	if strings.TrimSpace(cfg.Subtitles.OpenSubtitlesAPIKey) == "" {
		return false, "opensubtitles_api_key not set"
	}
	return true, ""
}
