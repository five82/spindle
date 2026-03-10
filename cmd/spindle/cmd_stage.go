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
			queryTitle := label
			if queryTitle == "" {
				queryTitle = "Unknown Disc"
			}

			tmdbClient := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language)
			results, err := tmdbClient.SearchMulti(ctx, queryTitle)
			if err != nil {
				return fmt.Errorf("tmdb search: %w", err)
			}

			fmt.Println("\n=== TMDB Results ===")
			if len(results) == 0 {
				fmt.Println("No TMDB results found")
				return nil
			}

			best, confidence := tmdb.SelectBestResult(results, queryTitle, "", 5)
			if best != nil {
				fmt.Printf("Best match: %s (%s)\n", best.DisplayTitle(), best.Year())
				fmt.Printf("  Type:       %s\n", best.MediaType)
				fmt.Printf("  TMDB ID:    %d\n", best.ID)
				fmt.Printf("  Confidence: %.2f\n", confidence)
				if best.Overview != "" {
					overview := best.Overview
					if len(overview) > 200 {
						overview = overview[:200] + "..."
					}
					fmt.Printf("  Overview:   %s\n", overview)
				}
			}

			if len(results) > 1 {
				fmt.Printf("\nAll results (%d):\n", len(results))
				for i, r := range results {
					if i >= 5 {
						fmt.Printf("  ... and %d more\n", len(results)-5)
						break
					}
					fmt.Printf("  %d. %s (%s) [%s, TMDB %d]\n",
						i+1, r.DisplayTitle(), r.Year(), r.MediaType, r.ID)
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
			result, err := svc.Transcribe(ctx, transcription.TranscribeRequest{
				InputPath:  file,
				AudioIndex: 0,
				Language:   "en",
				OutputDir:  workDir,
			})
			if err != nil {
				return fmt.Errorf("transcription: %w", err)
			}

			fmt.Printf("Transcription complete: %d segments", result.Segments)
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
				fmt.Printf("Subtitle saved: %s\n", destPath)
			} else {
				// Mux into MKV.
				base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
				outPath := filepath.Join(output, base+".subtitled.mkv")
				tmpPath := outPath + ".tmp"

				fmt.Printf("Muxing subtitles into %s...\n", filepath.Base(outPath))
				muxCmd := exec.CommandContext(ctx, "mkvmerge",
					"-o", tmpPath,
					file,
					"--language", "0:eng",
					"--track-name", "0:English",
					"--default-track", "0:yes",
					result.SRTPath,
				)
				if muxOut, err := muxCmd.CombinedOutput(); err != nil {
					_ = os.Remove(tmpPath)
					return fmt.Errorf("mkvmerge: %w: %s", err, muxOut)
				}
				if err := os.Rename(tmpPath, outPath); err != nil {
					return fmt.Errorf("rename: %w", err)
				}
				fmt.Printf("Subtitled file: %s\n", outPath)
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
