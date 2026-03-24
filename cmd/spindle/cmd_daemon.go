package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/daemonctl"
	"github.com/five82/spindle/internal/daemonrun"
	"github.com/five82/spindle/internal/deps"
	"github.com/five82/spindle/internal/queue"
)

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
			err := daemonctl.Start(daemonctl.StartOptions{
				LockPath:   lp,
				SocketPath: sp,
				LogPath:    cfg.DaemonLogPath(),
				ConfigFlag: flagConfig,
			})
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
			err := daemonctl.Stop(daemonctl.StopOptions{
				LockPath:   lockPath(),
				SocketPath: socketPath(),
				Token:      cfg.API.Token,
			})
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
			err := daemonctl.Stop(daemonctl.StopOptions{
				LockPath:   lp,
				SocketPath: sp,
				Token:      cfg.API.Token,
			})
			if err != nil && !errors.Is(err, daemonctl.ErrDaemonNotRunning) {
				return fmt.Errorf("stop: %w", err)
			}
			if err := daemonctl.Start(daemonctl.StartOptions{
				LockPath:   lp,
				SocketPath: sp,
				LogPath:    cfg.DaemonLogPath(),
				ConfigFlag: flagConfig,
			}); err != nil {
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

			fmt.Println()
			fmt.Println(headerStyle("Spindle Status"))
			fmt.Println()
			if running {
				fmt.Printf("  %-12s %s\n", labelStyle("Daemon"), successStyle("running"))
			} else {
				fmt.Printf("  %-12s %s\n", labelStyle("Daemon"), failStyle("stopped"))
			}
			if flagVerbose {
				fmt.Printf("  %-12s %s\n", labelStyle("Socket"), dimStyle(sp))
				fmt.Printf("  %-12s %s\n", labelStyle("Lock"), dimStyle(lp))
				if flagConfig != "" {
					fmt.Printf("  %-12s %s\n", labelStyle("Config"), dimStyle(flagConfig))
				} else {
					fmt.Printf("  %-12s %s\n", labelStyle("Config"), dimStyle("(default search path)"))
				}
			}

			fmt.Println()
			fmt.Println(headerStyle("Dependencies"))
			fmt.Println()
			reqs := []deps.Requirement{
				{Name: "makemkvcon", Command: "makemkvcon", Description: "MakeMKV CLI", Optional: false},
				{Name: "ffmpeg", Command: "ffmpeg", Description: "FFmpeg media processor", Optional: false},
				{Name: "ffprobe", Command: "ffprobe", Description: "FFprobe media analyzer", Optional: false},
				{Name: "mkvmerge", Command: "mkvmerge", Description: "MKVToolNix merge tool", Optional: false},
			}
			statuses := deps.CheckBinaries(reqs)
			for _, s := range statuses {
				mark := successStyle("✓")
				if !s.Available {
					mark = failStyle("✗")
				}
				if flagVerbose {
					fmt.Printf("  %-12s %s  %s\n", s.Name, mark, dimStyle(s.Detail))
				} else {
					fmt.Printf("  %-12s %s\n", s.Name, mark)
				}
			}

			fmt.Println()
			fmt.Println(headerStyle("Library Paths"))
			fmt.Println()
			if cfg != nil {
				checkPath("Movies", filepath.Join(cfg.Paths.LibraryDir, cfg.Library.MoviesDir))
				checkPath("TV", filepath.Join(cfg.Paths.LibraryDir, cfg.Library.TVDir))
			}

			fmt.Println()
			fmt.Println(headerStyle("Queue"))
			fmt.Println()
			acc, err := openQueueAccess()
			if err != nil {
				fmt.Printf("  %s\n", dimStyle("No active queue"))
				return nil
			}
			stats, err := acc.Stats()
			if err != nil {
				fmt.Printf("  %s\n", dimStyle("No active queue"))
				return nil
			}
			hasItems := false
			for _, stage := range []queue.Stage{
				queue.StagePending, queue.StageIdentification, queue.StageRipping,
				queue.StageEpisodeIdentification, queue.StageEncoding,
				queue.StageAudioAnalysis, queue.StageSubtitling,
				queue.StageOrganizing, queue.StageCompleted, queue.StageFailed,
			} {
				count := stats[stage]
				if count > 0 || flagVerbose {
					fmt.Printf("  %-24s %d\n", labelStyle(stage), count)
					hasItems = true
				}
			}
			if !hasItems {
				fmt.Printf("  %s\n", dimStyle("Empty"))
			}
			return nil
		},
	}
}

func checkPath(label, path string) {
	if path == "" {
		fmt.Printf("  %-8s %s\n", label, dimStyle("(not configured)"))
		return
	}
	if _, err := os.Stat(path); err != nil {
		fmt.Printf("  %-8s %s  %s\n", label, path, failStyle("✗"))
	} else {
		fmt.Printf("  %-8s %s  %s\n", label, path, successStyle("✓"))
	}
}

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
			daemonCfg, err := config.Load(flagConfig, nil)
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
