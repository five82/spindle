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
			launched := false
			if err != nil {
				fmt.Fprintln(stdout, "Daemon not running, launching...")
				if launchErr := launchDaemonProcess(cmd, ctx); launchErr != nil {
					return launchErr
				}
				client, err = waitForDaemonClient(ctx.socketPath(), 10*time.Second)
				if err != nil {
					return err
				}
				launched = true
			}
			defer client.Close()

			statusResp, statusErr := client.Status()
			if statusErr == nil && statusResp != nil && statusResp.Running {
				if launched {
					fmt.Fprintln(stdout, "Daemon started")
				} else {
					fmt.Fprintln(stdout, "Daemon already running")
				}
				return nil
			}

			fmt.Fprintln(stdout, "Starting daemon...")
			resp, err := client.Start()
			if err != nil {
				return err
			}
			switch {
			case resp.Started:
				fmt.Fprintln(stdout, "Daemon started")
			case launched && strings.EqualFold(strings.TrimSpace(resp.Message), "daemon already running"):
				fmt.Fprintln(stdout, "Daemon started")
			case strings.TrimSpace(resp.Message) != "":
				fmt.Fprintln(stdout, resp.Message)
			default:
				fmt.Fprintln(stdout, "Start request sent")
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

			colorize := shouldColorize(stdout)

			fmt.Fprintln(stdout, "System Status")
			if statusResp.Running {
				fmt.Fprintln(stdout, renderStatusLine("Spindle", statusOK, "Running", colorize))
			} else {
				fmt.Fprintln(stdout, renderStatusLine("Spindle", statusWarn, "Not running (run `spindle start`)", colorize))
			}
			fmt.Fprintln(stdout, detectDiscLine(cfg.OpticalDrive, colorize))
			if executableAvailable("drapto") {
				fmt.Fprintln(stdout, renderStatusLine("Drapto", statusOK, "Available", colorize))
			} else {
				fmt.Fprintln(stdout, renderStatusLine("Drapto", statusError, "Not available", colorize))
			}
			fmt.Fprintln(stdout, plexStatusLine(cfg, colorize))
			if strings.TrimSpace(cfg.NtfyTopic) != "" {
				fmt.Fprintln(stdout, renderStatusLine("Notifications", statusOK, "Configured", colorize))
			} else {
				fmt.Fprintln(stdout, renderStatusLine("Notifications", statusWarn, "Not configured", colorize))
			}

			for _, line := range dependencyLines(statusResp.Dependencies, colorize) {
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
				fmt.Fprintln(stdout, directoryStatusLine(dir.label, dir.path, colorize))
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

func dependencyLines(deps []ipc.DependencyStatus, colorize bool) []string {
	if len(deps) == 0 {
		return nil
	}
	lines := make([]string, 0, len(deps))
	for _, dep := range deps {
		if dep.Available {
			message := "Ready"
			if dep.Command != "" {
				message = fmt.Sprintf("Ready (command: %s)", dep.Command)
			}
			lines = append(lines, renderStatusLine(dep.Name, statusOK, message, colorize))
			continue
		}

		detail := strings.TrimSpace(dep.Detail)
		if detail == "" {
			detail = "not available"
		}
		kind := statusError
		if dep.Optional {
			kind = statusWarn
		}
		lines = append(lines, renderStatusLine(dep.Name, kind, detail, colorize))
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
	checks = append(checks, deps.CheckFFmpegForDrapto(cfg.DraptoBinary()))
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
