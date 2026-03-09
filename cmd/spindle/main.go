package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/auditgather"
	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/daemonctl"
	"github.com/five82/spindle/internal/daemonrun"
	"github.com/five82/spindle/internal/deps"
	"github.com/five82/spindle/internal/discidcache"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/queueaccess"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/staging"
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
			fmt.Printf("Identifying disc on %s...\n", device)
			// Stage execution wired in full integration.
			fmt.Println("(identify stage execution not yet wired)")
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
			fmt.Printf("Generating subtitles for %s...\n", file)
			// Stage execution wired in full integration.
			fmt.Println("(gensubtitle stage execution not yet wired)")
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
			fmt.Printf("Ripping from %s into cache...\n", device)
			fmt.Println("(cache rip not yet wired)")
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

			store := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
			entries, err := store.List()
			if err != nil {
				return err
			}
			if num > len(entries) {
				return fmt.Errorf("entry %d not found (have %d entries)", num, len(entries))
			}

			entry := entries[num-1]
			fmt.Printf("Queuing cached rip: %s (fingerprint: %s)\n", entry.DiscTitle, entry.Fingerprint)
			if allowDuplicate {
				fmt.Println("(allowing duplicate fingerprint)")
			}
			fmt.Println("(cache process not yet wired)")
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
			target := args[0]
			fmt.Printf("Running crop detection on %s...\n", target)
			fmt.Println("(debug crop not yet wired)")
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
			target := args[0]
			fmt.Printf("Running commentary detection on %s...\n", target)
			fmt.Println("(debug commentary not yet wired)")
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
