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

	"github.com/five82/spindle/internal/apply"
	"github.com/five82/spindle/internal/discidcache"
	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/fingerprint"
	"github.com/five82/spindle/internal/identify"
	"github.com/five82/spindle/internal/keydb"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/opensubtitles"
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
		tmdbID   int
		season   int
		episode  int
	)
	cmd := &cobra.Command{
		Use:   "subtitle <encoded-file>...",
		Short: "Download, sync, and verify an OpenSubtitles display subtitle",
		Long: "Download the identified title's OpenSubtitles track, clean it, retime it against a WhisperX " +
			"transcript of the file with ffsubsync, verify the match, and mux it (or --external for a sidecar). " +
			"TMDB identity comes from a [tmdbid-ID] marker in the path (with SxxEyy for TV library layouts) " +
			"or from --tmdb-id/--season/--episode. When no download passes verification, generate subtitles " +
			"with the whisperx-subtitles agent skill instead. Multiple files share one batched WhisperX run.",
		GroupID: groupDisc,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			out := commandOutput(flagQuiet)
			files := make([]string, 0, len(args))
			for _, file := range args {
				if _, err := os.Stat(file); err != nil {
					return fmt.Errorf("file not found: %s", file)
				}
				if absFile, err := filepath.Abs(file); err == nil {
					file = absFile
				}
				files = append(files, file)
			}
			ctx := context.Background()

			if workDir == "" {
				tmpDir, err := os.MkdirTemp("", "spindle-gensubtitle-*")
				if err != nil {
					return fmt.Errorf("create work dir: %w", err)
				}
				workDir = tmpDir
				defer func() { _ = os.RemoveAll(tmpDir) }()
			}
			sidecarMode := external || !cfg.Subtitles.MuxIntoMKV

			var cmdLogger *slog.Logger
			if !flagVerbose || flagQuiet {
				cmdLogger = slog.New(slog.NewTextHandler(io.Discard, nil))
			}

			identities := make([]subtitle.PathIdentity, len(files))
			for i, file := range files {
				identity, err := resolveSubtitleIdentity(file, tmdbID, season, episode)
				if err != nil {
					return fmt.Errorf("%s: %w", filepath.Base(file), err)
				}
				identities[i] = identity
			}

			svc := transcription.New(transcription.Params{
				Model:       cfg.Subtitles.WhisperXModel,
				CUDAEnabled: cfg.Subtitles.WhisperXCUDAEnabled,
				VADMethod:   cfg.Subtitles.WhisperXVADMethod,
				HFToken:     cfg.Subtitles.WhisperXHFToken,
			}, cmdLogger)
			osClient := opensubtitles.New(opensubtitles.Params{
				APIKey:    cfg.Subtitles.OpenSubtitlesAPIKey,
				UserAgent: cfg.Subtitles.OpenSubtitlesUserAgent,
				UserToken: cfg.Subtitles.OpenSubtitlesUserToken,
			}, cmdLogger)
			handler := subtitle.New(cfg, svc, osClient)

			// Batch transcription pays uvx startup and model load once for
			// the whole file list.
			transcripts := make([]*transcription.TranscribeResult, len(files))
			if len(files) > 1 {
				printCommandOutput(out, "Transcribing %d files (batched)...\n", len(files))
				reqs := make([]transcription.TranscribeRequest, len(files))
				for i, file := range files {
					selected, err := svc.SelectPrimaryAudioTrack(ctx, file, "en")
					if err != nil {
						return fmt.Errorf("select primary audio (%s): %w", filepath.Base(file), err)
					}
					reqs[i] = transcription.TranscribeRequest{
						InputPath:  file,
						AudioIndex: selected.Index,
						Language:   selected.Language,
						OutputDir:  filepath.Join(workDir, fmt.Sprintf("file%02d", i)),
						Purpose:    "subtitle_sync_reference",
					}
				}
				results, err := svc.TranscribeBatch(ctx, reqs)
				if err != nil {
					return fmt.Errorf("transcription: %w", err)
				}
				copy(transcripts, results)
			}

			failures := 0
			for i, file := range files {
				if err := adoptStandaloneSubtitle(ctx, handler, standaloneAdoptParams{
					file:        file,
					identity:    identities[i],
					transcript:  transcripts[i],
					workDir:     filepath.Join(workDir, fmt.Sprintf("file%02d", i)),
					outputDir:   output,
					sidecarMode: sidecarMode,
					out:         out,
				}); err != nil {
					failures++
					fmt.Fprintf(os.Stderr, "%s %s: %v\n", warnStyle("No subtitle:"), filepath.Base(file), err)
				}
			}
			if failures > 0 {
				return fmt.Errorf("%d of %d file(s) got no subtitles; generate them with the whisperx-subtitles agent skill", failures, len(files))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output directory")
	cmd.Flags().StringVar(&workDir, "work-dir", "", "Working directory")
	cmd.Flags().BoolVar(&external, "external", false, "Create external SRT sidecar instead of muxing")
	cmd.Flags().IntVar(&tmdbID, "tmdb-id", 0, "TMDB ID (overrides the [tmdbid-ID] path marker)")
	cmd.Flags().IntVar(&season, "season", 0, "TV season number (with --tmdb-id)")
	cmd.Flags().IntVar(&episode, "episode", 0, "TV episode number (with --tmdb-id)")
	cmd.Flags().BoolVarP(&flagQuiet, "quiet", "q", false, "Suppress progress and success output")
	return cmd
}

// resolveSubtitleIdentity picks the TMDB identity for one file: explicit
// flags win, then the Jellyfin path markers.
func resolveSubtitleIdentity(file string, tmdbID, season, episode int) (subtitle.PathIdentity, error) {
	if tmdbID > 0 {
		return subtitle.PathIdentity{TMDBID: tmdbID, Season: season, Episode: episode, EpisodeEnd: episode}, nil
	}
	identity, found, err := subtitle.ParseLibraryPathIdentity(file)
	if err != nil {
		return subtitle.PathIdentity{}, err
	}
	if !found {
		return subtitle.PathIdentity{}, errors.New("no [tmdbid-ID] marker in path; pass --tmdb-id (and --season/--episode for TV)")
	}
	if identity.EpisodeEnd > identity.Episode {
		return subtitle.PathIdentity{}, errors.New("multi-episode file has no single-episode subtitle source")
	}
	return identity, nil
}

type standaloneAdoptParams struct {
	file        string
	identity    subtitle.PathIdentity
	transcript  *transcription.TranscribeResult // nil when not batch-transcribed
	workDir     string
	outputDir   string
	sidecarMode bool
	out         io.Writer
}

// adoptStandaloneSubtitle runs the adoption process for one file and places
// the result as a sidecar or muxed track.
func adoptStandaloneSubtitle(ctx context.Context, handler *subtitle.Handler, p standaloneAdoptParams) error {
	file := p.file
	printCommandOutput(p.out, "Preparing subtitles for %s...\n", filepath.Base(file))

	var transcribeStart time.Time
	result, err := handler.AdoptForFile(ctx, subtitle.AdoptFileRequest{
		VideoPath:  file,
		WorkDir:    p.workDir,
		TMDBID:     p.identity.TMDBID,
		Season:     p.identity.Season,
		Episode:    p.identity.Episode,
		Transcript: p.transcript,
		OnTranscribeStart: func() {
			printCommandOutput(p.out, "  Transcribing sync reference...")
			transcribeStart = time.Now()
		},
		OnTranscribeComplete: func(transcript *transcription.TranscribeResult) {
			printCommandOutput(p.out, "%s (%d segments, %s)\n", successStyle("done"), transcript.Segments, formatPhaseDuration(time.Since(transcribeStart)))
		},
	})
	if err != nil {
		return err
	}
	for _, rejectedCandidate := range result.RejectedCandidates {
		printCommandOutput(p.out, "  Rejected %s\n", rejectedCandidate)
	}
	printCommandOutput(p.out, "  Adopted %s (%d segments, validation %s)\n", result.Candidate, result.Segments, result.Validation)
	if flagVerbose {
		printCommandOutput(p.out, "  %s %s\n", labelStyle("Gate:    "), result.GateMetrics)
	}

	outputDir := p.outputDir
	if outputDir == "" {
		outputDir = filepath.Dir(file)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	if p.sidecarMode {
		finalSidecarPath := apply.DisplaySubtitlePath(filepath.Join(outputDir, filepath.Base(file)), result.Language)
		data, err := os.ReadFile(result.SubtitlePath)
		if err != nil {
			return fmt.Errorf("read adopted srt: %w", err)
		}
		if err := os.WriteFile(finalSidecarPath, data, 0o644); err != nil {
			return fmt.Errorf("write adopted srt: %w", err)
		}
		printCommandOutput(p.out, "Saved sidecar: %s\n", finalSidecarPath)
		return nil
	}

	if apply.MKVHasSubtitleTrack(ctx, file) {
		printCommandOutput(p.out, "Replacing existing subtitle tracks...")
	} else {
		printCommandOutput(p.out, "Muxing subtitle into MKV...")
	}
	track := apply.MuxTrack{Path: result.SubtitlePath, Language: result.Language}
	if _, err := apply.MuxSubtitleTrack(ctx, apply.MuxRequest{VideoPath: file, OutputPath: file, Track: track, ReplaceExisting: true}); err != nil {
		return err
	}
	printCommandOutput(p.out, "%s\n", successStyle("done"))
	return nil
}

func formatPhaseDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	mins := int(d.Minutes())
	secs := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%02ds", mins, secs)
}

func newTestNotifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "notify",
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
			fmt.Println(successStyle("Test notification sent"))
			return nil
		},
	}
}
