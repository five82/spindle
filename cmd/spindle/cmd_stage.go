package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/identify"
	"github.com/five82/spindle/internal/discidcache"
	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/fingerprint"
	"github.com/five82/spindle/internal/makemkv"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/tmdb"
	"github.com/five82/spindle/internal/transcription"
)

func newIdentifyCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "identify [device]",
		Short: "Identify a disc and show TMDB matching details",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				device = args[0]
			}
			if device == "" && cfg != nil {
				device = cfg.MakeMKV.OpticalDrive
			}
			if device == "" {
				return fmt.Errorf("no device specified")
			}
			ctx := context.Background()

			// Probe disc for mount point and label.
			event, _ := discmonitor.ProbeDisc(ctx, device)
			var discLabel, mountPath string
			if event != nil {
				discLabel = event.Label
				mountPath = event.MountPath
			}

			// Generate fingerprint if disc is mounted.
			var fp string
			if mountPath != "" {
				var err error
				fp, err = fingerprint.Generate(mountPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: fingerprint generation failed: %v\n", err)
				}
			}

			// Check disc ID cache for fast path.
			if fp != "" {
				store, err := discidcache.Open(cfg.DiscIDCachePath())
				if err == nil {
					if entry := store.Lookup(fp); entry != nil {
						fmt.Println("=== Disc ID Cache Hit ===")
						fmt.Printf("Title:       %s\n", entry.Title)
						fmt.Printf("TMDB ID:     %d\n", entry.TMDBID)
						fmt.Printf("Type:        %s\n", entry.MediaType)
						if entry.Year != "" {
							fmt.Printf("Year:        %s\n", entry.Year)
						}
						if entry.Season > 0 {
							fmt.Printf("Season:      %d\n", entry.Season)
						}
						fmt.Printf("Fingerprint: %s\n", fp)
						return nil
					}
				}
			}

			// MakeMKV scan.
			fmt.Printf("Scanning disc on %s...\n", device)
			discInfo, err := makemkv.Scan(ctx, device,
				time.Duration(cfg.MakeMKV.InfoTimeout)*time.Second)
			if err != nil {
				return fmt.Errorf("makemkv scan: %w", err)
			}

			label := discInfo.Name
			if label == "" {
				label = discLabel
			}

			fmt.Println("\n=== Disc Info ===")
			fmt.Printf("Label:   %s\n", label)
			fmt.Printf("Titles:  %d\n", len(discInfo.Titles))
			if fp != "" {
				fmt.Printf("Fingerprint: %s\n", fp)
			}
			for _, t := range discInfo.Titles {
				fmt.Printf("  Title %d: %s (%s, %d ch, %s)\n",
					t.ID, t.Name, t.Duration, t.Chapters, formatBytes(t.SizeBytes))
			}

			// TMDB search.
			rawTitle := label
			if rawTitle == "" {
				rawTitle = "Unknown Disc"
			}
			queryTitle := identify.CleanQueryTitle(rawTitle)

			fmt.Printf("\n=== TMDB Search ===\n")
			if queryTitle != rawTitle {
				fmt.Printf("Query:   %s (cleaned from %q)\n", queryTitle, rawTitle)
			} else {
				fmt.Printf("Query:   %s\n", queryTitle)
			}

			tmdbClient := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language)
			results, err := tmdbClient.SearchMulti(ctx, queryTitle)
			if err != nil {
				return fmt.Errorf("tmdb search: %w", err)
			}

			fmt.Println("\n=== TMDB Results ===")
			if len(results) == 0 {
				fmt.Println("No TMDB results found")
				fmt.Println("Spindle will flag this item for manual review.")
				return nil
			}

			best, confidence := tmdb.SelectBestResult(results, queryTitle, "", 5)
			if best != nil {
				fmt.Printf("Selected: %s (%s) [%s, TMDB %d, confidence %.2f]\n",
					best.DisplayTitle(), best.Year(), best.MediaType, best.ID, confidence)
				fmt.Println("Spindle will use this result for metadata.")
				if best.Overview != "" {
					overview := best.Overview
					if !flagVerbose && len(overview) > 200 {
						overview = overview[:200] + "..."
					}
					fmt.Printf("  Overview: %s\n", overview)
				}
			}

			if len(results) > 1 {
				limit := 5
				if flagVerbose {
					limit = len(results)
				}
				fmt.Printf("\nOther candidates (%d):\n", len(results)-1)
				shown := 0
				for i := range results {
					r := &results[i]
					if best != nil && r.ID == best.ID && r.MediaType == best.MediaType {
						continue
					}
					if shown >= limit {
						fmt.Printf("  ... and %d more\n", len(results)-1-shown)
						break
					}
					score := tmdb.ScoreResult(r, queryTitle, "", 5)
					if flagVerbose {
						fmt.Printf("  - %s (%s) [%s, TMDB %d, score %.2f, votes %d]\n",
							r.DisplayTitle(), r.Year(), r.MediaType, r.ID, score, r.VoteCount)
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
	cmd.Flags().StringVarP(&device, "device", "d", "", "Optical device path")
	return cmd
}

func newGensubtitleCmd() *cobra.Command {
	var (
		output      string
		workDir     string
		fetchForced bool
		external    bool
	)
	cmd := &cobra.Command{
		Use:   "gensubtitle <encoded-file>",
		Short: "Create subtitles for an encoded media file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			file := args[0]
			if _, err := os.Stat(file); err != nil {
				return fmt.Errorf("file not found: %s", file)
			}
			ctx := context.Background()

			// Set up work directory.
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

			// Resolve input to absolute path for consistent output.
			absFile, err := filepath.Abs(file)
			if err == nil {
				file = absFile
			}

			// Set up output directory.
			if output == "" {
				output = filepath.Dir(file)
			}

			// Create transcription service.
			svc := transcription.New(
				cfg.Subtitles.WhisperXModel,
				cfg.Subtitles.WhisperXCUDAEnabled,
				cfg.Subtitles.WhisperXVADMethod,
				cfg.Subtitles.WhisperXHFToken,
				cfg.WhisperXCacheDir(),
			)

			fmt.Printf("Transcribing %s...\n", filepath.Base(file))

			// Verbose: show WhisperX config before transcription.
			if flagVerbose {
				model, device, vad := svc.Config()
				fmt.Printf("  Model:      %s\n", model)
				fmt.Printf("  Device:     %s\n", device)
				fmt.Printf("  VAD:        %s\n", vad)
				fmt.Printf("  Language:   en\n")
			}

			// Progress callback for phase output.
			progress := func(phase string, elapsed time.Duration) {
				switch {
				case phase == "extract" && elapsed == 0:
					fmt.Print("  Extracting audio...")
				case phase == "extract" && elapsed > 0:
					fmt.Printf("done (%s)\n", formatPhaseDuration(elapsed))
				case phase == "transcribe" && elapsed == 0:
					fmt.Print("  Running WhisperX...")
				case phase == "transcribe" && elapsed > 0:
					fmt.Printf("done (%s)\n", formatPhaseDuration(elapsed))
				}
			}

			result, err := svc.Transcribe(ctx, transcription.TranscribeRequest{
				InputPath:  file,
				AudioIndex: 0,
				Language:   "en",
				OutputDir:  workDir,
			}, progress)
			if err != nil {
				return fmt.Errorf("transcription: %w", err)
			}

			fmt.Printf("Transcription complete: %d segments", result.Segments)
			if result.Duration > 0 {
				fmt.Printf(", %s", formatContentDuration(result.Duration))
			}
			if result.Cached {
				fmt.Print(" (cached)")
			}
			fmt.Println()

			// Handle forced subtitles.
			if fetchForced && cfg.Subtitles.OpenSubtitlesEnabled {
				osClient := opensubtitles.New(
					cfg.Subtitles.OpenSubtitlesAPIKey,
					cfg.Subtitles.OpenSubtitlesUserAgent,
					cfg.Subtitles.OpenSubtitlesUserToken,
					"",
				)
				if osClient != nil {
					fmt.Println("Forced subtitle search requires TMDB ID (use pipeline for full support)")
				}
			}

			if external || !cfg.Subtitles.MuxIntoMKV {
				// Copy SRT to output directory as sidecar.
				base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
				destPath := filepath.Join(output, base+".en.srt")
				data, err := os.ReadFile(result.SRTPath)
				if err != nil {
					return fmt.Errorf("read srt: %w", err)
				}
				if err := os.WriteFile(destPath, data, 0o644); err != nil {
					return fmt.Errorf("write srt: %w", err)
				}
				fmt.Printf("Saved sidecar: %s\n", destPath)
			} else {
				fmt.Print("Muxing subtitle into MKV...")

				// Mux into MKV, replacing the original file.
				// --no-subtitles strips any existing subtitle tracks before adding the new one.
				tmpPath := file + ".tmp.mkv"
				muxCmd := exec.CommandContext(ctx, "mkvmerge",
					"-o", tmpPath,
					"--no-subtitles", file,
					"--language", "0:eng",
					"--track-name", "0:English",
					"--default-track", "0:yes",
					result.SRTPath,
				)
				if muxOut, err := muxCmd.CombinedOutput(); err != nil {
					_ = os.Remove(tmpPath)
					return fmt.Errorf("mkvmerge: %w: %s", err, muxOut)
				}
				if err := os.Rename(tmpPath, file); err != nil {
					return fmt.Errorf("rename: %w", err)
				}
				fmt.Println("done")
			}

			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output directory")
	cmd.Flags().StringVar(&workDir, "work-dir", "", "Working directory")
	cmd.Flags().BoolVar(&fetchForced, "fetch-forced", false, "Also fetch forced subs from OpenSubtitles")
	cmd.Flags().BoolVar(&external, "external", false, "Create external SRT sidecar instead of muxing")
	return cmd
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
		Use:   "test-notify",
		Short: "Send a test notification",
		RunE: func(_ *cobra.Command, _ []string) error {
			n := notify.New(cfg.Notifications.NtfyTopic, cfg.Notifications.RequestTimeout)
			if n == nil {
				return fmt.Errorf("notifications not configured (no ntfy topic)")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := n.Send(ctx, notify.EventTest, "Spindle Test", "Test notification from Spindle"); err != nil {
				return fmt.Errorf("send notification: %w", err)
			}
			fmt.Println("Test notification sent")
			return nil
		},
	}
}
