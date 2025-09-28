package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/config"
	"spindle/internal/deps"
	"spindle/internal/ipc"
	"spindle/internal/queue"
)

func newDaemonCommands(ctx *commandContext) []*cobra.Command {
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the spindle daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			stdout := cmd.OutOrStdout()
			client, err := ctx.dialClient()
			if err != nil {
				fmt.Fprintln(stdout, "Daemon not running, launching...")
				if launchErr := launchDaemonProcess(cmd, ctx); launchErr != nil {
					return launchErr
				}
				client, err = waitForDaemonClient(ctx.socketPath(), 10*time.Second)
				if err != nil {
					return err
				}
			} else {
				defer client.Close()
			}
			defer client.Close()
			resp, err := client.Start()
			if err != nil {
				return err
			}
			switch {
			case resp.Started:
				fmt.Fprintln(stdout, "Daemon started")
			case resp.Message != "":
				fmt.Fprintln(stdout, resp.Message)
			default:
				fmt.Fprintln(stdout, "Daemon already running")
			}
			return nil
		},
	}

	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the spindle daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			stdout := cmd.OutOrStdout()
			client, err := ctx.dialClient()
			if err != nil {
				fmt.Fprintln(stdout, "Daemon is not running")
				return nil
			}
			resp, err := client.Stop()
			_ = client.Close()
			if err != nil {
				return err
			}
			if !resp.Stopped {
				fmt.Fprintln(stdout, "Stop request sent")
			} else {
				fmt.Fprintln(stdout, "Stopping daemon...")
			}
			if err := waitForDaemonShutdown(ctx.socketPath(), 5*time.Second); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "Daemon stopped")
			return nil
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

				if len(statusResp.Dependencies) == 0 {
					statusResp.Dependencies = resolveDependencies(cfg)
				}
			}

			if len(statusResp.Dependencies) == 0 {
				statusResp.Dependencies = resolveDependencies(cfg)
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

			for _, line := range dependencyLines(statusResp.Dependencies) {
				fmt.Fprintln(stdout, line)
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

func dependencyLines(deps []ipc.DependencyStatus) []string {
	if len(deps) == 0 {
		return nil
	}
	lines := make([]string, 0, len(deps))
	for _, dep := range deps {
		if dep.Available {
			text := fmt.Sprintf("🛠️ %s: Ready", dep.Name)
			if dep.Command != "" {
				text = fmt.Sprintf("%s (command: %s)", text, dep.Command)
			}
			lines = append(lines, text)
			continue
		}

		indicator := "❌"
		if dep.Optional {
			indicator = "⚠️"
		}
		detail := strings.TrimSpace(dep.Detail)
		if detail == "" {
			detail = "not available"
		}
		lines = append(lines, fmt.Sprintf("%s %s: %s", indicator, dep.Name, detail))
	}
	return lines
}

func resolveDependencies(cfg *config.Config) []ipc.DependencyStatus {
	if cfg == nil {
		return nil
	}
	requirements := []deps.Requirement{
		{
			Name:        "MakeMKV",
			Command:     cfg.MakemkvBinary(),
			Description: "Required for disc ripping",
		},
		{
			Name:        "Drapto",
			Command:     cfg.DraptoBinary(),
			Description: "Required for encoding",
		},
	}
	checks := deps.CheckBinaries(requirements)
	statuses := make([]ipc.DependencyStatus, 0, len(checks))
	for _, check := range checks {
		statuses = append(statuses, ipc.DependencyStatus{
			Name:        check.Name,
			Command:     check.Command,
			Description: check.Description,
			Optional:    check.Optional,
			Available:   check.Available,
			Detail:      check.Detail,
		})
	}
	return statuses
}

func launchDaemonProcess(cmd *cobra.Command, ctx *commandContext) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	args := []string{"daemon"}
	if ctx.socketFlag != nil {
		if socket := strings.TrimSpace(*ctx.socketFlag); socket != "" {
			args = append(args, "--socket", socket)
		}
	}
	if ctx.configFlag != nil {
		if config := strings.TrimSpace(*ctx.configFlag); config != "" {
			args = append(args, "--config", config)
		}
	}
	proc := exec.Command(exe, args...)
	if err := proc.Start(); err != nil {
		return fmt.Errorf("launch daemon: %w", err)
	}
	return proc.Process.Release()
}

func waitForDaemonClient(socketPath string, timeout time.Duration) (*ipc.Client, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		client, err := ipc.Dial(socketPath)
		if err == nil {
			return client, nil
		}
		lastErr = err
		time.Sleep(200 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timeout waiting for daemon")
	}
	return nil, fmt.Errorf("daemon failed to start: %w", lastErr)
}

func waitForDaemonShutdown(socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		client, err := ipc.Dial(socketPath)
		if err != nil {
			if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
				return nil
			}
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
		status, statusErr := client.Status()
		_ = client.Close()
		if statusErr == nil && !status.Running {
			return nil
		}
		if statusErr != nil {
			lastErr = statusErr
		} else {
			lastErr = fmt.Errorf("daemon still running")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timeout waiting for shutdown")
	}
	return fmt.Errorf("daemon did not stop: %w", lastErr)
}
