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
			if flagVerbose {
				fmt.Printf("Socket: %s\n", sp)
				fmt.Printf("Lock:   %s\n", lp)
				if flagConfig != "" {
					fmt.Printf("Config: %s\n", flagConfig)
				} else {
					fmt.Println("Config: (default search path)")
				}
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
				if flagVerbose {
					fmt.Printf("  %-12s [%s] %s\n", s.Name, mark, s.Detail)
				} else {
					fmt.Printf("  %-12s [%s]\n", s.Name, mark)
				}
			}

			fmt.Println("\n=== Library Paths ===")
			if cfg != nil {
				checkPath("Movies", filepath.Join(cfg.Paths.LibraryDir, cfg.Library.MoviesDir))
				checkPath("TV", filepath.Join(cfg.Paths.LibraryDir, cfg.Library.TVDir))
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
				if count > 0 || flagVerbose {
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
