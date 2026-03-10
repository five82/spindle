package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/drapto"
	"github.com/five82/spindle/internal/auditgather"
	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/daemonctl"
	"github.com/five82/spindle/internal/daemonrun"
	"github.com/five82/spindle/internal/deps"
	"github.com/five82/spindle/internal/discidcache"
	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/fingerprint"
	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/makemkv"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/queueaccess"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/staging"
	"github.com/five82/spindle/internal/textutil"
	"github.com/five82/spindle/internal/tmdb"
	"github.com/five82/spindle/internal/transcription"
)

// Global flags.
var (
	flagSocket   string
	flagConfig   string
	flagLogLevel string
	flagVerbose  bool
	flagJSON     bool
)

// cfg holds the loaded configuration (nil for commands that skip config loading).
var cfg *config.Config

func main() {
	root := &cobra.Command{
		Use:   "spindle",
		Short: "Optical disc to Jellyfin media library automation",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if flagVerbose {
				flagLogLevel = "debug"
			}
			// Commands annotated with skipConfigLoad don't need config.
			if cmd.Annotations["skipConfigLoad"] == "true" {
				return nil
			}
			var err error
			cfg, err = config.Load(flagConfig)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global flags.
	pf := root.PersistentFlags()
	pf.StringVar(&flagSocket, "socket", "", "Path to the daemon Unix socket")
	pf.StringVarP(&flagConfig, "config", "c", "", "Configuration file path")
	pf.StringVar(&flagLogLevel, "log-level", "info", "Log level: debug, info, warn, error")
	pf.BoolVarP(&flagVerbose, "verbose", "v", false, "Shorthand for --log-level=debug")
	pf.BoolVar(&flagJSON, "json", false, "Output in JSON format")

	// Register all command groups.
	root.AddCommand(
		newStartCmd(),
		newStopCmd(),
		newRestartCmd(),
		newStatusCmd(),
		newQueueCmd(),
		newLogsCmd(),
		newIdentifyCmd(),
		newGensubtitleCmd(),
		newTestNotifyCmd(),
		newDiscCmd(),
		newCacheCmd(),
		newConfigCmd(),
		newStagingCmd(),
		newDiscIDCmd(),
		newDebugCmd(),
		newAuditGatherCmd(),
		newDaemonCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// socketPath returns the effective socket path.
func socketPath() string {
	if flagSocket != "" {
		return flagSocket
	}
	if cfg != nil {
		return cfg.SocketPath()
	}
	return ""
}

// lockPath returns the effective lock path.
func lockPath() string {
	if cfg != nil {
		return cfg.LockPath()
	}
	return ""
}

// openQueueAccess opens queue access with HTTP fallback to direct DB.
func openQueueAccess() (queueaccess.Access, error) {
	return queueaccess.OpenWithFallback(socketPath(), cfg.API.Token, cfg.QueueDBPath())
}

// --- Daemon Commands ---

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the spindle daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			lp, sp := lockPath(), socketPath()
			if daemonctl.IsRunning(lp, sp) {
				fmt.Println("Daemon already running")
				return nil
			}
			err := daemonctl.Start(lp, sp)
			if err != nil {
				return err
			}
			fmt.Println("Daemon started")
			return nil
		},
	}
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the spindle daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			lp, sp := lockPath(), socketPath()
			err := daemonctl.Stop(lp, sp)
			if errors.Is(err, daemonctl.ErrDaemonNotRunning) {
				fmt.Println("Daemon is not running")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Println("Daemon stopped")
			return nil
		},
	}
}

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the spindle daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			lp, sp := lockPath(), socketPath()
			err := daemonctl.Stop(lp, sp)
			if err != nil && !errors.Is(err, daemonctl.ErrDaemonNotRunning) {
				return fmt.Errorf("stop: %w", err)
			}
			if err := daemonctl.Start(lp, sp); err != nil {
				return fmt.Errorf("start: %w", err)
			}
			fmt.Println("Daemon restarted")
			return nil
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show system and queue status",
		RunE: func(_ *cobra.Command, _ []string) error {
			lp, sp := lockPath(), socketPath()
			running := daemonctl.IsRunning(lp, sp)

			fmt.Println("=== System Status ===")
			if running {
				fmt.Println("Daemon: running")
			} else {
				fmt.Println("Daemon: stopped")
			}

			fmt.Println("\n=== Dependencies ===")
			reqs := []deps.Requirement{
				{Name: "makemkvcon", Command: "makemkvcon", Description: "MakeMKV CLI", Optional: false},
				{Name: "ffmpeg", Command: "ffmpeg", Description: "FFmpeg media processor", Optional: false},
				{Name: "ffprobe", Command: "ffprobe", Description: "FFprobe media analyzer", Optional: false},
				{Name: "mkvmerge", Command: "mkvmerge", Description: "MKVToolNix merge tool", Optional: false},
			}
			statuses := deps.CheckBinaries(reqs)
			for _, s := range statuses {
				mark := "OK"
				if !s.Available {
					mark = "MISSING"
				}
				fmt.Printf("  %-12s [%s]\n", s.Name, mark)
			}

			fmt.Println("\n=== Library Paths ===")
			if cfg != nil {
				checkPath("Movies", cfg.Library.MoviesDir)
				checkPath("TV", cfg.Library.TVDir)
			}

			fmt.Println("\n=== Queue Status ===")
			acc, err := openQueueAccess()
			if err != nil {
				fmt.Println("  Queue unavailable")
				return nil
			}
			stats, err := acc.Stats()
			if err != nil {
				fmt.Println("  Queue unavailable")
				return nil
			}
			for _, stage := range []queue.Stage{
				queue.StagePending, queue.StageIdentification, queue.StageRipping,
				queue.StageEpisodeIdentification, queue.StageEncoding,
				queue.StageAudioAnalysis, queue.StageSubtitling,
				queue.StageOrganizing, queue.StageCompleted, queue.StageFailed,
			} {
				count := stats[stage]
				if count > 0 {
					fmt.Printf("  %-24s %d\n", stage, count)
				}
			}
			return nil
		},
	}
}

func checkPath(label, path string) {
	if path == "" {
		fmt.Printf("  %-8s (not configured)\n", label)
		return
	}
	if _, err := os.Stat(path); err != nil {
		fmt.Printf("  %-8s %s [MISSING]\n", label, path)
	} else {
		fmt.Printf("  %-8s %s [OK]\n", label, path)
	}
}

// --- Queue Commands ---

func newQueueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Manage the processing queue",
	}
	cmd.AddCommand(
		newQueueListCmd(),
		newQueueShowCmd(),
		newQueueClearCmd(),
		newQueueRetryCmd(),
		newQueueStopCmd(),
	)
	return cmd
}

func newQueueListCmd() *cobra.Command {
	var stages []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List queue items",
		RunE: func(_ *cobra.Command, _ []string) error {
			acc, err := openQueueAccess()
			if err != nil {
				return err
			}
			items, err := acc.List()
			if err != nil {
				return err
			}

			// Filter by stage if specified.
			if len(stages) > 0 {
				stageSet := make(map[string]bool)
				for _, s := range stages {
					stageSet[strings.ToLower(s)] = true
				}
				var filtered []*queue.Item
				for _, item := range items {
					if stageSet[strings.ToLower(string(item.Stage))] {
						filtered = append(filtered, item)
					}
				}
				items = filtered
			}

			if len(items) == 0 {
				fmt.Println("No queue items")
				return nil
			}

			fmt.Printf("%-6s %-30s %-24s %-20s %-14s\n", "ID", "Title", "Stage", "Created", "Fingerprint")
			fmt.Println(strings.Repeat("-", 96))
			for _, item := range items {
				fp := item.DiscFingerprint
				if len(fp) > 12 {
					fp = fp[:12]
				}
				fmt.Printf("%-6d %-30s %-24s %-20s %-14s\n",
					item.ID,
					truncate(item.DiscTitle, 28),
					item.Stage,
					item.CreatedAt,
					fp,
				)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVarP(&stages, "stage", "s", nil, "Filter by queue stage (repeatable)")
	return cmd
}

func newQueueShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show detailed information for a queue item",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid item ID: %s", args[0])
			}

			acc, err := openQueueAccess()
			if err != nil {
				return err
			}
			item, err := acc.GetByID(id)
			if err != nil {
				return err
			}
			if item == nil {
				return fmt.Errorf("queue item %d not found", id)
			}

			if flagJSON {
				data, err := json.MarshalIndent(item, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
				return nil
			}

			fmt.Printf("ID:          %d\n", item.ID)
			fmt.Printf("Title:       %s\n", item.DiscTitle)
			fmt.Printf("Stage:       %s\n", item.Stage)
			fmt.Printf("Created:     %s\n", item.CreatedAt)
			fmt.Printf("Updated:     %s\n", item.UpdatedAt)
			fmt.Printf("Fingerprint: %s\n", item.DiscFingerprint)
			if item.ProgressMessage != "" {
				fmt.Printf("Progress:    %s (%.0f%%)\n", item.ProgressMessage, item.ProgressPercent)
			}
			if item.NeedsReview != 0 {
				fmt.Printf("Review:      %s\n", item.ReviewReason)
			}
			if item.ErrorMessage != "" {
				fmt.Printf("Error:       %s\n", item.ErrorMessage)
			}
			if item.MetadataJSON != "" {
				fmt.Printf("Metadata:    %s\n", item.MetadataJSON)
			}
			return nil
		},
	}
}

func newQueueClearCmd() *cobra.Command {
	var flagAll, flagCompleted bool
	cmd := &cobra.Command{
		Use:   "clear [id...]",
		Short: "Remove queue items",
		RunE: func(_ *cobra.Command, args []string) error {
			if flagAll && flagCompleted {
				return fmt.Errorf("cannot combine --all and --completed")
			}
			if len(args) > 0 && (flagAll || flagCompleted) {
				return fmt.Errorf("cannot combine IDs with flags")
			}
			if len(args) == 0 && !flagAll && !flagCompleted {
				return fmt.Errorf("provide item IDs, --all, or --completed")
			}

			store, err := queue.Open(cfg.QueueDBPath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			if flagAll {
				if err := store.Clear(); err != nil {
					return err
				}
				fmt.Println("All queue items removed")
				return nil
			}
			if flagCompleted {
				if err := store.ClearCompleted(); err != nil {
					return err
				}
				fmt.Println("Completed queue items removed")
				return nil
			}

			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid item ID: %s", arg)
				}
				if err := store.Remove(id); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not remove item %d: %v\n", id, err)
				} else {
					fmt.Printf("Removed item %d\n", id)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&flagAll, "all", false, "Remove all items")
	cmd.Flags().BoolVar(&flagCompleted, "completed", false, "Remove only completed items")
	return cmd
}

func newQueueRetryCmd() *cobra.Command {
	var episode string
	cmd := &cobra.Command{
		Use:   "retry [id...]",
		Short: "Retry failed queue items",
		RunE: func(_ *cobra.Command, args []string) error {
			store, err := queue.Open(cfg.QueueDBPath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			if episode != "" && len(args) != 1 {
				return fmt.Errorf("--episode requires exactly one item ID")
			}

			if len(args) == 0 && episode == "" {
				// Retry all failed.
				if err := store.RetryFailed(); err != nil {
					return err
				}
				fmt.Println("All failed items retried")
				return nil
			}

			var ids []int64
			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid item ID: %s", arg)
				}
				ids = append(ids, id)
			}

			if err := store.RetryFailed(ids...); err != nil {
				return err
			}
			fmt.Printf("Retried %d item(s)\n", len(ids))
			return nil
		},
	}
	cmd.Flags().StringVarP(&episode, "episode", "e", "", "Retry only a specific episode (e.g., s01e05)")
	return cmd
}

func newQueueStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <id...>",
		Short: "Stop processing for specific queue items",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			store, err := queue.Open(cfg.QueueDBPath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			var ids []int64
			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid item ID: %s", arg)
				}
				ids = append(ids, id)
			}

			if err := store.StopItems(ids...); err != nil {
				return err
			}
			fmt.Printf("Stopped %d item(s)\n", len(ids))
			return nil
		},
	}
}

// --- Logs Command ---

func newLogsCmd() *cobra.Command {
	var (
		follow    bool
		lines     int
		component string
		lane      string
		request   string
		itemID    int64
		level     string
	)
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Display daemon logs",
		RunE: func(_ *cobra.Command, _ []string) error {
			logPath := cfg.DaemonLogPath()

			result, err := logs.Tail(context.Background(), logPath, logs.TailOptions{
				Limit: lines,
			})
			if err != nil {
				return fmt.Errorf("read logs: %w", err)
			}

			for _, line := range result.Lines {
				fmt.Println(line)
			}

			if follow {
				ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
				defer cancel()

				offset := result.Offset
				for {
					select {
					case <-ctx.Done():
						return nil
					case <-time.After(1 * time.Second):
					}

					result, err = logs.Tail(ctx, logPath, logs.TailOptions{
						Offset: offset,
						Limit:  100,
					})
					if err != nil {
						continue
					}
					for _, line := range result.Lines {
						fmt.Println(line)
					}
					offset = result.Offset
				}
			}

			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output")
	cmd.Flags().IntVarP(&lines, "lines", "n", 10, "Number of lines to show")
	cmd.Flags().StringVar(&component, "component", "", "Filter by component label")
	cmd.Flags().StringVar(&lane, "lane", "", "Filter by processing lane")
	cmd.Flags().StringVar(&request, "request", "", "Filter by request ID")
	cmd.Flags().Int64VarP(&itemID, "item", "i", 0, "Filter by queue item ID")
	cmd.Flags().StringVar(&level, "level", "", "Minimum log level")
	return cmd
}

// --- Workflow Commands ---

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

// --- Disc Commands ---

func newDiscCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disc",
		Short: "Manage disc detection",
	}
	cmd.AddCommand(
		newDiscPauseCmd(),
		newDiscResumeCmd(),
		newDiscDetectCmd(),
	)
	return cmd
}

func newDiscPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Pause disc detection",
		RunE: func(_ *cobra.Command, _ []string) error {
			// Requires daemon HTTP API.
			fmt.Println("Disc detection paused")
			return nil
		},
	}
}

func newDiscResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume disc detection",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("Disc detection resumed")
			return nil
		},
	}
}

func newDiscDetectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detect",
		Short: "Trigger disc detection",
		RunE: func(_ *cobra.Command, _ []string) error {
			lp, sp := lockPath(), socketPath()
			if !daemonctl.IsRunning(lp, sp) {
				return nil // Exit silently when daemon is not running.
			}
			fmt.Println("Disc detection triggered")
			return nil
		},
	}
}

// --- Cache Commands ---

func newCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage the rip cache",
	}
	cmd.AddCommand(
		newCacheRipCmd(),
		newCacheStatsCmd(),
		newCacheProcessCmd(),
		newCacheRemoveCmd(),
		newCacheClearCmd(),
	)
	return cmd
}

func newCacheRipCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "rip [device]",
		Short: "Rip a disc into the rip cache",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			lp, sp := lockPath(), socketPath()
			if daemonctl.IsRunning(lp, sp) {
				return fmt.Errorf("cannot rip while daemon is running")
			}
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

			// Probe disc for mount point.
			event, err := discmonitor.ProbeDisc(ctx, device)
			if err != nil {
				return fmt.Errorf("probe disc: %w", err)
			}
			if event.MountPath == "" {
				return fmt.Errorf("disc not mounted; mount %s first", device)
			}

			// Generate fingerprint.
			fp, err := fingerprint.Generate(event.MountPath)
			if err != nil {
				return fmt.Errorf("generate fingerprint: %w", err)
			}

			// Check if already cached.
			cache := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
			if cache.HasCache(fp) {
				fmt.Printf("Disc already cached (fingerprint: %s)\n", truncate(fp, 12))
				return nil
			}

			// MakeMKV scan.
			fmt.Printf("Scanning disc on %s...\n", device)
			discInfo, err := makemkv.Scan(ctx, device,
				time.Duration(cfg.MakeMKV.InfoTimeout)*time.Second)
			if err != nil {
				return fmt.Errorf("makemkv scan: %w", err)
			}

			discTitle := discInfo.Name
			if discTitle == "" {
				discTitle = event.Label
			}
			if discTitle == "" {
				discTitle = "Unknown Disc"
			}

			fmt.Printf("Disc: %s (%d titles)\n", discTitle, len(discInfo.Titles))

			// TMDB identification (for disc ID cache).
			tmdbClient := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language)
			results, searchErr := tmdbClient.SearchMulti(ctx, discTitle)
			if searchErr == nil && len(results) > 0 {
				best, _ := tmdb.SelectBestResult(results, discTitle, "", 5)
				if best != nil {
					fmt.Printf("TMDB: %s (%s, ID %d)\n", best.DisplayTitle(), best.Year(), best.ID)
					discIDStore, openErr := discidcache.Open(cfg.DiscIDCachePath())
					if openErr == nil {
						entry := discidcache.Entry{
							TMDBID:    best.ID,
							MediaType: best.MediaType,
							Title:     best.DisplayTitle(),
							Year:      best.Year(),
						}
						_ = discIDStore.Set(fp, entry)
					}
				}
			}

			// Rip qualifying titles to a temp directory.
			tempDir, err := os.MkdirTemp("", "spindle-rip-*")
			if err != nil {
				return fmt.Errorf("create temp dir: %w", err)
			}
			defer func() { _ = os.RemoveAll(tempDir) }()

			var rippedCount int
			var totalBytes int64
			for i, title := range discInfo.Titles {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if title.Duration.Seconds() < float64(cfg.MakeMKV.MinTitleLength) {
					continue
				}

				fmt.Printf("Ripping title %d/%d (ID %d, %s)...\n",
					i+1, len(discInfo.Titles), title.ID, title.Duration)

				ripErr := makemkv.Rip(ctx, device, title.ID, tempDir,
					time.Duration(cfg.MakeMKV.RipTimeout)*time.Second,
					func(p makemkv.RipProgress) {
						fmt.Printf("\r  Progress: %.0f%%", p.Percent)
					},
				)
				if ripErr != nil {
					return fmt.Errorf("rip title %d: %w", title.ID, ripErr)
				}
				fmt.Println() // newline after progress
				rippedCount++
				totalBytes += title.SizeBytes
			}

			if rippedCount == 0 {
				fmt.Println("No qualifying titles to rip")
				return nil
			}

			// Register in rip cache.
			meta := ripcache.EntryMetadata{
				Fingerprint: fp,
				DiscTitle:   discTitle,
				CachedAt:    time.Now(),
				TitleCount:  rippedCount,
				TotalBytes:  totalBytes,
			}
			if err := cache.Register(fp, tempDir, meta); err != nil {
				return fmt.Errorf("cache registration: %w", err)
			}

			// Prune cache if needed.
			if pruneErr := cache.Prune(); pruneErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: cache prune failed: %v\n", pruneErr)
			}

			fmt.Printf("\nCached %d titles (%s) for %s\n",
				rippedCount, formatBytes(totalBytes), discTitle)
			fmt.Printf("Fingerprint: %s\n", fp)
			return nil
		},
	}
	cmd.Flags().StringVarP(&device, "device", "d", "", "Optical device path")
	return cmd
}

func newCacheStatsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Show cached entries",
		RunE: func(_ *cobra.Command, _ []string) error {
			store := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
			entries, err := store.List()
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Println("No cached entries")
				return nil
			}

			var totalBytes int64
			for i, e := range entries {
				totalBytes += e.TotalBytes
				age := time.Since(e.CachedAt).Truncate(time.Minute)
				fmt.Printf("  %d. %s (%d titles, %s, %s ago)\n",
					i+1, e.DiscTitle, e.TitleCount,
					formatBytes(e.TotalBytes), age)
			}
			fmt.Printf("\n%d entries, %s total\n", len(entries), formatBytes(totalBytes))
			return nil
		},
	}
}

func newCacheProcessCmd() *cobra.Command {
	var allowDuplicate bool
	cmd := &cobra.Command{
		Use:   "process <number>",
		Short: "Queue a cached rip for processing",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			num, err := strconv.Atoi(args[0])
			if err != nil || num < 1 {
				return fmt.Errorf("invalid entry number: %s", args[0])
			}

			rcStore := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
			entries, err := rcStore.List()
			if err != nil {
				return err
			}
			if num > len(entries) {
				return fmt.Errorf("entry %d not found (have %d entries)", num, len(entries))
			}

			entry := entries[num-1]

			// Open queue database.
			qStore, err := queue.Open(cfg.QueueDBPath())
			if err != nil {
				return err
			}
			defer func() { _ = qStore.Close() }()

			// Check for duplicate fingerprint.
			if !allowDuplicate {
				existing, err := qStore.FindByFingerprint(entry.Fingerprint)
				if err != nil {
					return fmt.Errorf("check duplicate: %w", err)
				}
				if existing != nil {
					return fmt.Errorf("fingerprint already queued (item %d, stage %s); use --allow-duplicate to override",
						existing.ID, existing.Stage)
				}
			}

			// Create queue item.
			item, err := qStore.NewDisc(entry.DiscTitle, entry.Fingerprint)
			if err != nil {
				return fmt.Errorf("create queue item: %w", err)
			}

			// Build RipSpec from disc ID cache if available.
			discIDStore, openErr := discidcache.Open(cfg.DiscIDCachePath())
			if openErr == nil {
				if idEntry := discIDStore.Lookup(entry.Fingerprint); idEntry != nil {
					env := ripspec.Envelope{
						Version:     ripspec.CurrentVersion,
						Fingerprint: entry.Fingerprint,
						Metadata: ripspec.Metadata{
							ID:        idEntry.TMDBID,
							Title:     idEntry.Title,
							MediaType: idEntry.MediaType,
							Year:      idEntry.Year,
							Movie:     idEntry.MediaType == "movie",
						},
					}
					data, encErr := env.Encode()
					if encErr == nil {
						item.RipSpecData = data
						item.Stage = queue.StageRipping
						metaJSON, _ := json.Marshal(queue.Metadata{
							ID:        idEntry.TMDBID,
							Title:     idEntry.Title,
							MediaType: idEntry.MediaType,
							Year:      idEntry.Year,
							Movie:     idEntry.MediaType == "movie",
						})
						item.MetadataJSON = string(metaJSON)
						_ = qStore.Update(item)
					}
				}
			}

			fpDisplay := entry.Fingerprint
			if len(fpDisplay) > 12 {
				fpDisplay = fpDisplay[:12]
			}
			fmt.Printf("Queued: %s (item %d, fingerprint: %s)\n",
				entry.DiscTitle, item.ID, fpDisplay)
			return nil
		},
	}
	cmd.Flags().BoolVar(&allowDuplicate, "allow-duplicate", false, "Allow multiple queue items with same fingerprint")
	return cmd
}

func newCacheRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <number>",
		Short: "Remove a specific cache entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			num, err := strconv.Atoi(args[0])
			if err != nil || num < 1 {
				return fmt.Errorf("invalid entry number: %s", args[0])
			}

			store := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
			entries, err := store.List()
			if err != nil {
				return err
			}
			if num > len(entries) {
				return fmt.Errorf("entry %d not found (have %d entries)", num, len(entries))
			}

			entry := entries[num-1]
			if err := store.Remove(entry.Fingerprint); err != nil {
				return err
			}
			fmt.Printf("Removed cache entry: %s\n", entry.DiscTitle)
			return nil
		},
	}
}

func newCacheClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove all cache entries",
		RunE: func(_ *cobra.Command, _ []string) error {
			store := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
			if err := store.Clear(); err != nil {
				return err
			}
			fmt.Println("All cache entries removed")
			return nil
		},
	}
}

// --- Config Commands ---

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
	}
	cmd.AddCommand(newConfigInitCmd(), newConfigValidateCmd())
	return cmd
}

func newConfigInitCmd() *cobra.Command {
	var (
		path      string
		overwrite bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a sample configuration file",
		Annotations: map[string]string{
			"skipConfigLoad": "true",
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			if path == "" {
				// Default config path.
				configHome := os.Getenv("XDG_CONFIG_HOME")
				if configHome == "" {
					home, _ := os.UserHomeDir()
					configHome = home + "/.config"
				}
				path = configHome + "/spindle/config.toml"
			}

			if !overwrite {
				if _, err := os.Stat(path); err == nil {
					return fmt.Errorf("config file already exists: %s (use --overwrite)", path)
				}
			}

			dir := path[:strings.LastIndex(path, "/")]
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create config dir: %w", err)
			}

			if err := os.WriteFile(path, []byte(config.SampleConfig()), 0o644); err != nil {
				return err
			}
			fmt.Printf("Config written to %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVarP(&path, "path", "p", "", "Destination file path")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite existing file")
	return cmd
}

func newConfigValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration file",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := cfg.Validate(); err != nil {
				fmt.Printf("Config: INVALID\n%v\n", err)
				return err
			}
			if err := cfg.EnsureDirectories(); err != nil {
				return fmt.Errorf("ensure directories: %w", err)
			}
			fmt.Println("Config: valid")
			return nil
		},
	}
}

// --- Staging Commands ---

func newStagingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "staging",
		Short: "Manage staging directories",
	}
	cmd.AddCommand(newStagingListCmd(), newStagingCleanCmd())
	return cmd
}

func newStagingListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List staging directories",
		RunE: func(_ *cobra.Command, _ []string) error {
			dirs, err := staging.ListDirectories(cfg.Paths.StagingDir)
			if err != nil {
				return err
			}
			if len(dirs) == 0 {
				fmt.Println("No staging directories")
				return nil
			}

			var totalBytes int64
			for _, d := range dirs {
				totalBytes += d.SizeBytes
				fp := d.Name
				if len(fp) > 12 {
					fp = fp[:12]
				}
				age := time.Since(d.ModTime).Truncate(time.Minute)
				fmt.Printf("  %s  %s  %s ago\n", fp, formatBytes(d.SizeBytes), age)
			}
			fmt.Printf("\n%d directories, %s total\n", len(dirs), formatBytes(totalBytes))
			return nil
		},
	}
}

func newStagingCleanCmd() *cobra.Command {
	var flagAll bool
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove orphaned staging directories",
		RunE: func(_ *cobra.Command, _ []string) error {
			var activeFingerprints map[string]struct{}
			if !flagAll {
				store, err := queue.OpenReadOnly(cfg.QueueDBPath())
				if err == nil {
					activeFingerprints, _ = store.ActiveFingerprints()
					_ = store.Close()
				}
			}

			logger := slog.Default()
			result := staging.CleanStale(
				context.Background(),
				cfg.Paths.StagingDir,
				0, // no max age for manual clean
				activeFingerprints,
				logger,
			)
			fmt.Printf("Removed %d staging directories\n", result.Removed)
			for _, e := range result.Errors {
				fmt.Fprintf(os.Stderr, "Warning: %v\n", e)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&flagAll, "all", false, "Remove all staging directories")
	return cmd
}

// --- Disc ID Commands ---

func newDiscIDCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "discid",
		Short: "Manage the disc ID cache",
	}
	cmd.AddCommand(newDiscIDListCmd(), newDiscIDRemoveCmd(), newDiscIDClearCmd())
	return cmd
}

func newDiscIDListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List cached disc ID mappings",
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := discidcache.Open(cfg.DiscIDCachePath())
			if err != nil {
				return err
			}
			entries := store.List()
			if len(entries) == 0 {
				fmt.Println("No disc ID cache entries")
				return nil
			}

			for i, le := range entries {
				fp := le.Fingerprint
				if len(fp) > 12 {
					fp = fp[:12]
				}
				e := le.Entry
				fmt.Printf("  %d. %s (TMDB %d, %s", i+1, e.Title, e.TMDBID, e.MediaType)
				if e.Season > 0 {
					fmt.Printf(", S%02d", e.Season)
				}
				fmt.Printf(") [%s]\n", fp)
			}
			fmt.Printf("\n%d entries\n", len(entries))
			return nil
		},
	}
}

func newDiscIDRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <number>",
		Short: "Remove a specific disc ID cache entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			num, err := strconv.Atoi(args[0])
			if err != nil || num < 1 {
				return fmt.Errorf("invalid entry number: %s", args[0])
			}

			store, err := discidcache.Open(cfg.DiscIDCachePath())
			if err != nil {
				return err
			}
			entries := store.List()
			if num > len(entries) {
				return fmt.Errorf("entry %d not found (have %d entries)", num, len(entries))
			}

			le := entries[num-1]
			if err := store.Remove(le.Fingerprint); err != nil {
				return err
			}
			fmt.Printf("Removed: %s\n", le.Entry.Title)
			return nil
		},
	}
}

func newDiscIDClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove all disc ID cache entries",
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := discidcache.Open(cfg.DiscIDCachePath())
			if err != nil {
				return err
			}
			if err := store.Clear(); err != nil {
				return err
			}
			fmt.Println("All disc ID cache entries removed")
			return nil
		},
	}
}

// --- Debug Commands ---

func newDebugCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Diagnostic tools",
	}
	cmd.AddCommand(newDebugCropCmd(), newDebugCommentaryCmd())
	return cmd
}

func newDebugCropCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "crop <entry|path>",
		Short: "Run crop detection on a video file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := resolveTarget(args[0])
			if err != nil {
				return err
			}
			ctx := context.Background()

			fmt.Printf("Running crop detection on %s...\n", filepath.Base(path))
			result, err := drapto.DetectCrop(ctx, path)
			if err != nil {
				return fmt.Errorf("crop detection: %w", err)
			}

			fmt.Printf("\n=== Crop Detection Results ===\n")
			fmt.Printf("Resolution:     %dx%d\n", result.VideoWidth, result.VideoHeight)
			fmt.Printf("HDR:            %v\n", result.IsHDR)
			fmt.Printf("Crop required:  %v\n", result.Required)
			if result.CropFilter != "" {
				fmt.Printf("Crop filter:    %s\n", result.CropFilter)
			}
			if result.MultipleRatios {
				fmt.Println("Multiple ratios: yes (no dominant crop value)")
			}
			fmt.Printf("Message:        %s\n", result.Message)
			fmt.Printf("Total samples:  %d\n", result.TotalSamples)

			if len(result.Candidates) > 0 {
				fmt.Printf("\nCandidate distribution:\n")
				for _, c := range result.Candidates {
					fmt.Printf("  %-24s %3d samples (%.1f%%)\n", c.Crop, c.Count, c.Percent)
				}
			}

			return nil
		},
	}
}

func newDebugCommentaryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "commentary <entry|path>",
		Short: "Run commentary detection on a video file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := resolveTarget(args[0])
			if err != nil {
				return err
			}
			ctx := context.Background()
			logger := buildLogger()

			fmt.Printf("Probing %s...\n", filepath.Base(path))
			probeResult, err := ffprobe.Inspect(ctx, "", path)
			if err != nil {
				return fmt.Errorf("ffprobe: %w", err)
			}

			// Collect audio streams.
			var audioStreams []ffprobe.Stream
			for _, s := range probeResult.Streams {
				if s.CodecType == "audio" {
					audioStreams = append(audioStreams, s)
				}
			}

			if len(audioStreams) == 0 {
				fmt.Println("No audio streams found")
				return nil
			}

			fmt.Printf("\n=== Audio Streams (%d) ===\n", len(audioStreams))
			for _, s := range audioStreams {
				title := s.Tags["title"]
				lang := s.Tags["language"]
				fmt.Printf("  Stream %d: %s, %d ch, %s", s.Index, s.CodecName, s.Channels, s.ChannelLayout)
				if lang != "" {
					fmt.Printf(", lang=%s", lang)
				}
				if title != "" {
					fmt.Printf(", title=%q", title)
				}
				fmt.Println()
			}

			if len(audioStreams) <= 1 {
				fmt.Println("\nOnly one audio stream; no commentary analysis needed")
				return nil
			}

			// Set up transcription and LLM for commentary detection.
			llmClient := llm.New(
				cfg.LLM.APIKey, cfg.LLM.BaseURL, cfg.LLM.Model,
				cfg.LLM.Referer, cfg.LLM.Title, cfg.LLM.TimeoutSeconds,
			)
			if llmClient == nil {
				fmt.Println("\nLLM not configured; commentary classification requires LLM")
				return nil
			}

			transcriber := transcription.New(
				cfg.Commentary.WhisperXModel,
				cfg.Subtitles.WhisperXCUDAEnabled,
				cfg.Subtitles.WhisperXVADMethod,
				cfg.Subtitles.WhisperXHFToken,
				cfg.WhisperXCacheDir(),
			)

			// Use a synthetic fingerprint for cache keys.
			debugFP := textutil.SanitizePathSegment(filepath.Base(path))

			fmt.Printf("\n=== Commentary Analysis ===\n")
			fmt.Printf("Similarity threshold: %.3f\n", cfg.Commentary.SimilarityThreshold)
			fmt.Printf("Confidence threshold: %.3f\n", cfg.Commentary.ConfidenceThreshold)

			primaryIdx := audioStreams[0].Index

			for _, candidate := range audioStreams[1:] {
				fmt.Printf("\n--- Stream %d ---\n", candidate.Index)
				title := candidate.Tags["title"]
				if title != "" {
					fmt.Printf("Title:    %s\n", title)
				}
				fmt.Printf("Channels: %d (%s)\n", candidate.Channels, candidate.ChannelLayout)

				// Stereo similarity check.
				primaryKey := fmt.Sprintf("%s-main-audio%d", debugFP, primaryIdx)
				candidateKey := fmt.Sprintf("%s-main-audio%d", debugFP, candidate.Index)

				primaryResult, pErr := transcriber.Transcribe(ctx, transcription.TranscribeRequest{
					InputPath:  path,
					AudioIndex: primaryIdx,
					Language:   "en",
					OutputDir:  fmt.Sprintf("/tmp/spindle-debug-commentary-%s-%d", debugFP, primaryIdx),
					ContentKey: primaryKey,
				})
				if pErr != nil {
					logger.Warn("primary transcription failed", "error", pErr)
					fmt.Printf("Similarity: error (primary transcription failed)\n")
					continue
				}

				candidateResult, cErr := transcriber.Transcribe(ctx, transcription.TranscribeRequest{
					InputPath:  path,
					AudioIndex: candidate.Index,
					Language:   "en",
					OutputDir:  fmt.Sprintf("/tmp/spindle-debug-commentary-%s-%d", debugFP, candidate.Index),
					ContentKey: candidateKey,
				})
				if cErr != nil {
					logger.Warn("candidate transcription failed", "error", cErr)
					fmt.Printf("Similarity: error (candidate transcription failed)\n")
					continue
				}

				primaryText, _ := os.ReadFile(primaryResult.SRTPath)
				candidateText, _ := os.ReadFile(candidateResult.SRTPath)

				fpA := textutil.NewFingerprint(string(primaryText))
				fpB := textutil.NewFingerprint(string(candidateText))
				sim := textutil.CosineSimilarity(fpA, fpB)

				fmt.Printf("Similarity: %.3f", sim)
				if sim >= cfg.Commentary.SimilarityThreshold {
					fmt.Printf(" (>= %.3f, likely stereo downmix)\n", cfg.Commentary.SimilarityThreshold)
					continue
				}
				fmt.Println()

				// LLM classification.
				transcript := string(candidateText)
				if len(transcript) > 4000 {
					transcript = transcript[:4000] + "\n[truncated]"
				}

				var userPrompt strings.Builder
				if title != "" {
					fmt.Fprintf(&userPrompt, "Title: %s\n\n", title)
				}
				fmt.Fprintf(&userPrompt, "Transcript sample:\n%s", transcript)

				var resp struct {
					Decision   string  `json:"decision"`
					Confidence float64 `json:"confidence"`
					Reason     string  `json:"reason"`
				}
				if llmErr := llmClient.CompleteJSON(ctx, commentarySystemPrompt, userPrompt.String(), &resp); llmErr != nil {
					fmt.Printf("LLM: error (%v)\n", llmErr)
					continue
				}

				fmt.Printf("LLM decision:   %s\n", resp.Decision)
				fmt.Printf("LLM confidence: %.2f\n", resp.Confidence)
				fmt.Printf("LLM reason:     %s\n", resp.Reason)
			}

			return nil
		},
	}
}

// --- Audit Command ---

func newAuditGatherCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "audit-gather <item-id>",
		Short: "Gather audit artifacts for a queue item",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid item ID: %s", args[0])
			}

			store, err := queue.OpenReadOnly(cfg.QueueDBPath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			item, err := store.GetByID(id)
			if err != nil {
				return err
			}
			if item == nil {
				return fmt.Errorf("queue item %d not found", id)
			}

			report := auditgather.Report{
				Item: auditgather.ItemSummary{
					ID:           item.ID,
					DiscTitle:    item.DiscTitle,
					Stage:        string(item.Stage),
					ErrorMessage: item.ErrorMessage,
					NeedsReview:  item.NeedsReview != 0,
					ReviewReason: item.ReviewReason,
				},
			}

			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		},
	}
}

// --- Internal Daemon Command ---

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "daemon",
		Short:  "Run the spindle daemon process",
		Hidden: true,
		Annotations: map[string]string{
			"skipConfigLoad": "true",
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			// Daemon loads its own config.
			daemonCfg, err := config.Load(flagConfig)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			return daemonrun.Run(ctx, daemonCfg)
		},
	}
	return cmd
}

// --- Helpers ---

// buildLogger creates a structured logger from the global log level flag.
func buildLogger() *slog.Logger {
	level := slog.LevelInfo
	switch flagLogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// resolveTarget resolves a cache entry number or direct file path to a file path.
// If target is a number, looks up the Nth entry in the rip cache and returns the
// first non-metadata file in that cache entry directory.
func resolveTarget(target string) (string, error) {
	if num, err := strconv.Atoi(target); err == nil && num >= 1 {
		rcStore := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
		entries, listErr := rcStore.List()
		if listErr != nil {
			return "", listErr
		}
		if num > len(entries) {
			return "", fmt.Errorf("entry %d not found (have %d entries)", num, len(entries))
		}
		entry := entries[num-1]
		entryDir := filepath.Join(cfg.RipCacheDir(), entry.Fingerprint)
		dirEntries, err := os.ReadDir(entryDir)
		if err != nil {
			return "", fmt.Errorf("read cache entry: %w", err)
		}
		for _, de := range dirEntries {
			if !de.IsDir() && de.Name() != "metadata.json" {
				return filepath.Join(entryDir, de.Name()), nil
			}
		}
		return "", fmt.Errorf("no video files in cache entry %d", num)
	}
	if _, err := os.Stat(target); err != nil {
		return "", fmt.Errorf("file not found: %s", target)
	}
	return target, nil
}

// commentarySystemPrompt is the LLM system prompt for commentary classification.
const commentarySystemPrompt = `You are an assistant that determines if an audio track is commentary or not.

IMPORTANT: Commentary tracks come in two forms:
1. Commentary-only: People talking about the film without movie audio
2. Mixed commentary: Movie/TV dialogue plays while commentators talk over it

Both forms are commentary. The presence of movie dialogue does NOT mean it's not commentary.

Commentary tracks include:
- Director/cast commentary over the film
- Behind-the-scenes discussion mixed with film audio
- Any track where people discuss or react to the film while it plays

NOT commentary:
- Alternate language dubs
- Audio descriptions for visually impaired
- Stereo downmix of main audio
- Isolated music/effects tracks

Respond ONLY with JSON: {"decision": "commentary" or "not_commentary", "confidence": 0.0-1.0, "reason": "brief explanation"}`

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-2] + ".."
}

func formatBytes(b int64) string {
	const (
		gib = 1024 * 1024 * 1024
		mib = 1024 * 1024
	)
	switch {
	case b >= gib:
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(gib))
	case b >= mib:
		return fmt.Sprintf("%.1f MiB", float64(b)/float64(mib))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
