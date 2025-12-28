package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/config"
	"spindle/internal/deps"
	"spindle/internal/ipc"
	"spindle/internal/queue"
	"spindle/internal/services/presetllm"
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
		Short: "Stop the spindle daemon (completely terminates the process)",
		RunE: func(cmd *cobra.Command, args []string) error {
			stdout := cmd.OutOrStdout()
			client, err := ctx.dialClient()
			if err != nil {
				fmt.Fprintln(stdout, "Daemon is not running")
				return nil
			}
			statusResp, statusErr := client.Status()
			var lockPath, queueDBPath string
			pid := 0
			if statusErr == nil && statusResp != nil {
				lockPath = statusResp.LockPath
				queueDBPath = statusResp.QueueDBPath
				pid = statusResp.PID
			}
			resp, err := client.Stop()
			_ = client.Close()
			if err != nil {
				return err
			}
			if !resp.Stopped {
				fmt.Fprintln(stdout, "Stop request sent")
			} else {
				fmt.Fprintln(stdout, "Stopping daemon workflow...")
			}

			// Wait for graceful shutdown
			_ = waitForDaemonShutdown(ctx.socketPath(), 5*time.Second)
			alive, livePID, aliveErr := daemonProcessInfo(ctx.socketPath())
			if aliveErr != nil {
				alive = false
			}

			// Always terminate the process completely
			if alive {
				currentPID := livePID
				if currentPID == 0 {
					currentPID = pid
				}
				logDir := deriveLogDir(lockPath, queueDBPath, ctx)
				if logDir == "" {
					return fmt.Errorf("unable to determine daemon log directory")
				}
				pidPath := filepath.Join(logDir, "spindle.pid")
				lockFile := filepath.Join(logDir, "spindle.lock")
				fmt.Fprintf(stdout, "Stopping daemon process (pid %d)...\n", currentPID)
				_, killErr := forceKillDaemonProcess(pidPath, lockFile, currentPID)
				if killErr != nil {
					return fmt.Errorf("failed to stop daemon process: %w", killErr)
				}
				_ = os.Remove(ctx.socketPath())
				fmt.Fprintf(stdout, "Daemon stopped\n")
			} else {
				fmt.Fprintln(stdout, "Daemon stopped")
			}
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
			fmt.Fprintln(stdout, detectDiscLine(cfg.MakeMKV.OpticalDrive, colorize))
			fmt.Fprintln(stdout, jellyfinStatusLine(cfg, colorize))
			if strings.TrimSpace(cfg.Notifications.NtfyTopic) != "" {
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
				{label: "Library", path: cfg.Paths.LibraryDir},
				{label: "Movies", path: librarySubdirPath(cfg.Paths.LibraryDir, cfg.Library.MoviesDir)},
				{label: "TV", path: librarySubdirPath(cfg.Paths.LibraryDir, cfg.Library.TVDir)},
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
		{
			Name:        "bd_info",
			Command:     "bd_info",
			Description: "Enhances disc metadata when MakeMKV titles are generic",
			Optional:    true,
		},
	}
	if cfg.Subtitles.Enabled {
		requirements = append(requirements, deps.Requirement{
			Name:        "uvx",
			Command:     "uvx",
			Description: "Required for WhisperX-driven transcription",
		})
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
	if cfg.PresetDecider.Enabled {
		statuses = append(statuses, presetDeciderDependencyStatus(cfg))
	}
	return statuses
}

func presetDeciderDependencyStatus(cfg *config.Config) ipc.DependencyStatus {
	status := ipc.DependencyStatus{
		Name:        "Preset Decider",
		Description: "LLM-driven Drapto preset selector",
		Optional:    true,
	}
	key := strings.TrimSpace(cfg.PresetDecider.APIKey)
	if key == "" {
		status.Detail = "API key missing (set preset_decider_api_key or OPENROUTER_API_KEY)"
		return status
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := presetllm.NewClient(presetllm.Config{
		APIKey:  key,
		BaseURL: cfg.PresetDecider.BaseURL,
		Model:   cfg.PresetDecider.Model,
		Referer: cfg.PresetDecider.Referer,
		Title:   cfg.PresetDecider.Title,
	})
	if err := client.HealthCheck(ctx); err != nil {
		status.Detail = summarizePresetDeciderError(err)
		return status
	}
	status.Available = true
	status.Detail = "API reachable"
	return status
}

func summarizePresetDeciderError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "health check timed out (LLM API unresponsive)"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "health check timed out (LLM API unreachable)"
	}
	return err.Error()
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

func deriveLogDir(lockPath, queueDBPath string, ctx *commandContext) string {
	if lockPath != "" {
		return filepath.Dir(lockPath)
	}
	if queueDBPath != "" {
		return filepath.Dir(queueDBPath)
	}
	if cfg := ctx.configValue(); cfg != nil && strings.TrimSpace(cfg.Paths.LogDir) != "" {
		return cfg.Paths.LogDir
	}
	return ""
}

func forceKillDaemonProcess(pidPath, lockPath string, fallbackPID int) (int, error) {
	pid := fallbackPID
	data, err := os.ReadFile(pidPath)
	if err == nil {
		pidStr := strings.TrimSpace(string(data))
		if pidStr != "" {
			if parsed, parseErr := strconv.Atoi(pidStr); parseErr == nil && parsed > 0 {
				pid = parsed
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("read daemon pid file %q: %w", pidPath, err)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("unable to determine daemon pid (pid file: %s)", pidPath)
	}
	if pid == os.Getpid() {
		return 0, fmt.Errorf("refusing to kill current process (pid %d)", pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, fmt.Errorf("locate daemon process %d: %w", pid, err)
	}
	if err := proc.Kill(); err != nil {
		return 0, fmt.Errorf("kill daemon process %d: %w", pid, err)
	}
	if err := os.Remove(pidPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("remove pid file %q: %w", pidPath, err)
	}
	if lockPath != "" {
		_ = os.Remove(lockPath)
	}
	return pid, nil
}

func daemonProcessInfo(socketPath string) (bool, int, error) {
	client, err := ipc.Dial(socketPath)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
			return false, 0, nil
		}
		return false, 0, err
	}
	defer client.Close()
	status, statusErr := client.Status()
	if statusErr != nil {
		return true, 0, statusErr
	}
	pid := 0
	if status != nil {
		pid = status.PID
	}
	return true, pid, nil
}
