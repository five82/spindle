package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/ipc"
	"spindle/internal/queue"
)

func newDaemonCommands(ctx *commandContext) []*cobra.Command {
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the spindle daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.withClient(func(client *ipc.Client) error {
				resp, err := client.Start()
				if err != nil {
					return err
				}
				switch {
				case resp.Started:
					fmt.Fprintln(cmd.OutOrStdout(), "Daemon started")
				case resp.Message != "":
					fmt.Fprintln(cmd.OutOrStdout(), resp.Message)
				default:
					fmt.Fprintln(cmd.OutOrStdout(), "Daemon already running")
				}
				return nil
			})
		},
	}

	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the spindle daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.withClient(func(client *ipc.Client) error {
				resp, err := client.Stop()
				if err != nil {
					return err
				}
				if resp.Stopped {
					fmt.Fprintln(cmd.OutOrStdout(), "Daemon stopped")
				}
				return nil
			})
		},
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show system and queue status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ctx.configValue()
			if cfg == nil {
				return errors.New("configuration not available")
			}

			stdout := cmd.OutOrStdout()
			statusResp := &ipc.StatusResponse{}

			client, err := ipc.Dial(ctx.socketPath())
			if err == nil {
				defer client.Close()
				if resp, statusErr := client.Status(); statusErr == nil {
					statusResp = resp
				}
			}

			queueStats := make(map[string]int, len(statusResp.QueueStats))
			for k, v := range statusResp.QueueStats {
				queueStats[k] = v
			}

			if !statusResp.Running {
				queryCtx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
				defer cancel()

				store, openErr := queue.Open(cfg)
				if openErr == nil {
					stats, statsErr := store.Stats(queryCtx)
					_ = store.Close()
					if statsErr == nil {
						queueStats = make(map[string]int, len(stats))
						for status, count := range stats {
							queueStats[string(status)] = count
						}
					}
				}
			}

			fmt.Fprintln(stdout, "System Status")
			if statusResp.Running {
				fmt.Fprintln(stdout, "🟢 Spindle: Running")
			} else {
				fmt.Fprintln(stdout, "🔴 Spindle: Not running")
			}
			fmt.Fprintln(stdout, detectDiscLine(cfg.OpticalDrive))
			if executableAvailable("drapto") {
				fmt.Fprintln(stdout, "⚙️ Drapto: Available")
			} else {
				fmt.Fprintln(stdout, "⚙️ Drapto: Not available")
			}
			fmt.Fprintln(stdout, plexStatusLine(cfg))
			if strings.TrimSpace(cfg.NtfyTopic) != "" {
				fmt.Fprintln(stdout, "📱 Notifications: Configured")
			} else {
				fmt.Fprintln(stdout, "📱 Notifications: Not configured")
			}

			for _, dir := range []struct {
				label string
				path  string
			}{
				{label: "Library", path: cfg.LibraryDir},
				{label: "Movies", path: librarySubdirPath(cfg.LibraryDir, cfg.MoviesDir)},
				{label: "TV", path: librarySubdirPath(cfg.LibraryDir, cfg.TVDir)},
			} {
				fmt.Fprintln(stdout, directoryStatusLine(dir.label, dir.path))
			}

			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "Queue Status")

			rows := buildQueueStatusRows(queueStats)
			if len(rows) == 0 {
				fmt.Fprintln(stdout, "Queue is empty")
				return nil
			}

			table := renderTable([]string{"Status", "Count"}, rows, []columnAlignment{alignLeft, alignRight})
			fmt.Fprint(stdout, table)
			return nil
		},
	}

	return []*cobra.Command{startCmd, stopCmd, statusCmd}
}
