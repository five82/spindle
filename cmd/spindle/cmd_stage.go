package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/discidcache"
	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/fingerprint"
	"github.com/five82/spindle/internal/identify"
	"github.com/five82/spindle/internal/keydb"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/subtitle"
	"github.com/five82/spindle/internal/tmdb"
	"github.com/five82/spindle/internal/transcription"
)

func newIdentifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "identify [device]",
		Short:   "Identify a disc and show TMDB matching details",
		Example: "  spindle disc identify          # use the configured optical drive\n  spindle disc identify /dev/sr1",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var device string
			if len(args) > 0 {
				device = args[0]
			}
			if device == "" && cfg != nil {
				device = cfg.MakeMKV.OpticalDrive
			}
			if device == "" {
				return fmt.Errorf("no device specified and no optical drive configured")
			}
			ctx := context.Background()

			// Probe disc for mount point and label.
			event, _ := discmonitor.ProbeDisc(ctx, device)
			var discLabel string
			var lsblkMount string
			if event != nil {
				discLabel = event.Label
				lsblkMount = event.MountPath
			}

			logger := buildLogger()

			// Resolve mount point (same as daemon) for fingerprint generation.
			// This ensures spindle identify and the daemon produce identical results.
			var fp string
			mountPoint, cleanup, mountErr := discmonitor.ResolveMountPoint(ctx, device, lsblkMount, logger)
			if mountErr != nil {
				fmt.Fprintf(os.Stderr, "%s mount resolution failed, proceeding without fingerprint: %v\n", warnStyle("Warning:"), mountErr)
			} else {
				defer cleanup()
				var fpErr error
				fp, fpErr = fingerprint.Generate(mountPoint, logger)
				if fpErr != nil {
					fmt.Fprintf(os.Stderr, "%s fingerprint generation failed: %v\n", warnStyle("Warning:"), fpErr)
				}
			}

			// Open disc ID cache (optional).
			discIDStore, cacheErr := discidcache.Open(cfg.DiscIDCachePath(), nil)
			if cacheErr != nil {
				logger.Debug("disc ID cache unavailable", "error", cacheErr)
			}

			// Load KeyDB catalog (optional).
			var keydbCat *keydb.Catalog
			if cat, _, loadErr := keydb.LoadOrDownload(ctx, cfg.MakeMKV.KeyDBPath, cfg.MakeMKV.KeyDBDownloadURL,
				cfg.MakeMKV.KeyDBTimeout(), logger); loadErr == nil {
				keydbCat = cat
			}

			// Build TMDB client.
			tmdbClient := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language, nil)

			// Construct the identification handler (nil for notifier).
			handler := identify.New(cfg, tmdbClient, nil, discIDStore, keydbCat)

			// Build a temporary queue item for identification.
			item := &queue.Item{
				DiscTitle:       discLabel,
				DiscFingerprint: fp,
			}

			fmt.Printf("Scanning disc on %s...\n", device)
			result, err := handler.Identify(ctx, item, logger)
			if err != nil {
				return err
			}

			// === Disc Info ===
			fmt.Printf("\n%s\n", headerStyle("=== Disc Info ==="))
			if result.DiscInfo != nil {
				label := result.DiscInfo.Name
				if label == "" {
					label = discLabel
				}
				fmt.Printf("%s %s\n", labelStyle("Label:  "), label)
				fmt.Printf("%s %d\n", labelStyle("Titles: "), len(result.DiscInfo.Titles))
			}
			if fp != "" {
				fmt.Printf("%s %s\n", labelStyle("Fingerprint:"), dimStyle(fp))
			}
			if result.BDInfo != nil {
				fmt.Printf("%s %s\n", labelStyle("BDInfo: "), result.BDInfo.DiscName)
			}
			fmt.Printf("%s %s\n", labelStyle("Source: "), result.DiscSource)
			if result.DiscInfo != nil {
				for _, t := range result.DiscInfo.Titles {
					fmt.Printf("  Title %d: %s (%d:%02d:%02d, %d ch, %s)\n",
						t.ID, t.Name, t.Duration/3600, (t.Duration%3600)/60, t.Duration%60, t.Chapters, formatBytes(t.SizeBytes))
				}
			}

			// === TMDB Search ===
			fmt.Printf("\n%s\n", headerStyle("=== TMDB Search ==="))
			fmt.Printf("%s %s (source: %s)\n", labelStyle("Query:  "), result.QueryTitle, result.TitleSource)
			if result.QueryTitle != result.RawTitle {
				fmt.Printf("%s %s\n", labelStyle("Raw:    "), dimStyle(result.RawTitle))
			}
			if result.SearchYear > 0 {
				fmt.Printf("%s %d (source: %s)\n", labelStyle("Year:   "), result.SearchYear, result.YearSource)
			}

			// === TMDB Results ===
			fmt.Printf("\n%s\n", headerStyle("=== TMDB Results ==="))
			if result.Degraded {
				fmt.Println("No TMDB results met confidence threshold.")
				fmt.Println("Spindle will flag this item for manual review.")
			}

			if result.Best != nil {
				fmt.Printf("%s %s (%s) [%s, TMDB %d, votes %d]\n",
					labelStyle("Selected:"), result.Best.DisplayTitle(), result.Best.Year(), result.Best.MediaType, result.Best.ID, result.Best.VoteCount)
				fmt.Println("Spindle will use this result for metadata.")
				if result.Best.Overview != "" {
					overview := result.Best.Overview
					if !flagVerbose {
						overview = truncate(overview, 200)
					}
					fmt.Printf("  Overview: %s\n", overview)
				}
			}

			if len(result.AllResults) > 1 {
				limit := 5
				if flagVerbose {
					limit = len(result.AllResults)
				}
				fmt.Printf("\nOther candidates (%d):\n", len(result.AllResults)-1)
				shown := 0
				for i := range result.AllResults {
					r := &result.AllResults[i]
					if result.Best != nil && r.ID == result.Best.ID && r.MediaType == result.Best.MediaType {
						continue
					}
					if shown >= limit {
						fmt.Printf("  ... and %d more\n", len(result.AllResults)-1-shown)
						break
					}
					if flagVerbose {
						fmt.Printf("  - %s (%s) [%s, TMDB %d, votes %d, avg %.1f]\n",
							r.DisplayTitle(), r.Year(), r.MediaType, r.ID, r.VoteCount, r.VoteAverage)
					} else {
						fmt.Printf("  - %s (%s) [%s, TMDB %d]\n",
							r.DisplayTitle(), r.Year(), r.MediaType, r.ID)
					}
					shown++
				}
			}

			return nil
		},
	}
	return cmd
}

func newGensubtitleCmd() *cobra.Command {
	var (
		output   string
		workDir  string
		external bool
	)
	cmd := &cobra.Command{
		Use:   "subtitle <encoded-file>",
		Short: "Generate a WhisperX display subtitle for an encoded file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			file := args[0]
			if _, err := os.Stat(file); err != nil {
				return fmt.Errorf("file not found: %s", file)
			}
			ctx := context.Background()

			cleanupWorkDir := false
			if workDir == "" {
				var err error
				workDir, err = os.MkdirTemp("", "spindle-gensubtitle-*")
				if err != nil {
					return fmt.Errorf("create work dir: %w", err)
				}
				cleanupWorkDir = true
				defer func() {
					if cleanupWorkDir {
						_ = os.RemoveAll(workDir)
					}
				}()
			}

			if absFile, err := filepath.Abs(file); err == nil {
				file = absFile
			}
			if output == "" {
				output = filepath.Dir(file)
			}
			if err := os.MkdirAll(output, 0o755); err != nil {
				return fmt.Errorf("create output dir: %w", err)
			}
			sidecarMode := external || !cfg.Subtitles.MuxIntoMKV

			var cmdLogger *slog.Logger
			if !flagVerbose {
				cmdLogger = slog.New(slog.NewTextHandler(io.Discard, nil))
			}

			svc := transcription.New(transcription.Params{
				Model:       cfg.Subtitles.WhisperXModel,
				CUDAEnabled: cfg.Subtitles.WhisperXCUDAEnabled,
				VADMethod:   cfg.Subtitles.WhisperXVADMethod,
				HFToken:     cfg.Subtitles.WhisperXHFToken,
			}, cmdLogger)

			fmt.Printf("Preparing subtitles for %s...\n", filepath.Base(file))
			if flagVerbose {
				model, device, vad := svc.Config()
				fmt.Printf("  %s %s\n", labelStyle("Model:   "), model)
				fmt.Printf("  %s %s\n", labelStyle("Device:  "), device)
				fmt.Printf("  %s %s\n", labelStyle("VAD:     "), vad)
				fmt.Printf("  %s en\n", labelStyle("Language:"))
			}

			selectedLanguage := "en"
			var formatStart time.Time
			result, err := subtitle.GenerateDisplaySubtitle(ctx, subtitle.GenerateDisplaySubtitleRequest{
				VideoPath:       file,
				DisplayBasePath: filepath.Join(workDir, filepath.Base(file)),
				WorkDir:         workDir,
				Language:        "en",
				Transcriber:     svc,
				Logger:          cmdLogger,
				Progress: func(phase transcription.Phase, elapsed time.Duration) {
					switch {
					case phase == transcription.PhaseExtract && elapsed == 0:
						fmt.Println("  Extracting audio...")
					case phase == transcription.PhaseExtract && elapsed > 0:
						fmt.Printf("  Extracting audio %s (%s)\n", successStyle("done"), formatPhaseDuration(elapsed))
					case phase == transcription.PhaseTranscribe && elapsed == 0:
						fmt.Println("  Running WhisperX...")
					case phase == transcription.PhaseTranscribe && elapsed > 0:
						fmt.Printf("  Running WhisperX %s (%s)\n", successStyle("done"), formatPhaseDuration(elapsed))
					}
				},
				OnAudioSelected: func(selectedAudio transcription.SelectedAudio) {
					selectedLanguage = selectedAudio.Language
					if flagVerbose {
						fmt.Printf("  %s %s (stream 0:a:%d)\n", labelStyle("Audio:   "), selectedAudio.Label, selectedAudio.Index)
					}
				},
				OnTranscriptionComplete: func(transcript *transcription.TranscribeResult) {
					fmt.Printf("Canonical transcript ready: %d segments", transcript.Segments)
					if transcript.Duration > 0 {
						fmt.Printf(", %s", formatContentDuration(transcript.Duration))
					}
					fmt.Println()
				},
				OnFormattingStart: func() {
					fmt.Print("  Formatting subtitles...")
					formatStart = time.Now()
				},
				OnFormattingComplete: func(formatted subtitle.FormatResult) {
					fmt.Printf("%s (%d -> %d segments, split %d, wrapped %d, retimed %d, %s)\n", successStyle("done"), formatted.OriginalSegments, formatted.FilteredSegments, formatted.SplitCues, formatted.WrappedCues, formatted.RetimedCues, formatPhaseDuration(time.Since(formatStart)))
				},
			})
			if err != nil {
				return standaloneDisplaySubtitleError(err)
			}

			displayPath := result.Formatting.DisplayPath
			if sidecarMode {
				finalSidecarPath := subtitle.DisplaySubtitlePath(filepath.Join(output, filepath.Base(file)), selectedLanguage)
				data, err := os.ReadFile(displayPath)
				if err != nil {
					return fmt.Errorf("read formatted srt: %w", err)
				}
				if err := os.WriteFile(finalSidecarPath, data, 0o644); err != nil {
					return fmt.Errorf("write formatted srt: %w", err)
				}
				fmt.Printf("Saved sidecar: %s\n", finalSidecarPath)
				return nil
			}

			if subtitle.MKVHasSubtitleTrack(ctx, file) {
				fmt.Print("Replacing existing subtitle tracks...")
			} else {
				fmt.Print("Muxing subtitle into MKV...")
			}
			track := subtitle.MuxTrack{Path: displayPath, Language: selectedLanguage}
			if _, err := subtitle.MuxSubtitleTrack(ctx, subtitle.MuxRequest{VideoPath: file, OutputPath: file, Track: track, ReplaceExisting: true}); err != nil {
				return err
			}
			fmt.Println(successStyle("done"))
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output directory")
	cmd.Flags().StringVar(&workDir, "work-dir", "", "Working directory")
	cmd.Flags().BoolVar(&external, "external", false, "Create external SRT sidecar instead of muxing")
	return cmd
}

func standaloneDisplaySubtitleError(err error) error {
	var displayErr *subtitle.DisplaySubtitleError
	if !errors.As(err, &displayErr) {
		return err
	}
	switch displayErr.Op {
	case "select audio":
		return fmt.Errorf("select primary audio: %w", displayErr.Err)
	case "transcribe":
		return fmt.Errorf("transcription: %w", displayErr.Err)
	case "format subtitle":
		return fmt.Errorf("format subtitles: %w", displayErr.Err)
	default:
		return err
	}
}

// formatPhaseDuration formats a time.Duration for phase timing display.
// Uses "1.2s" for < 1 minute, "1m30s" for >= 1 minute.
func formatPhaseDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	mins := int(d.Minutes())
	secs := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%02ds", mins, secs)
}

// formatContentDuration formats a duration in seconds as "1h38m12s".
func formatContentDuration(secs float64) string {
	return time.Duration(secs * float64(time.Second)).Truncate(time.Second).String()
}

func newTestNotifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "notify",
		Short: "Send a test notification",
		RunE: func(_ *cobra.Command, _ []string) error {
			n := notify.New(cfg.Notifications.NtfyTopic, cfg.Notifications.RequestTimeout, nil)
			if n == nil {
				return fmt.Errorf("notifications not configured (no ntfy topic)")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := n.Send(ctx, notify.EventTest, "Spindle Test", "Test notification from Spindle"); err != nil {
				return fmt.Errorf("send notification: %w", err)
			}
			fmt.Println(successStyle("Test notification sent"))
			return nil
		},
	}
}
