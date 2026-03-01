package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/daemonctl"
	"spindle/internal/ipc"
)

func newDaemonCommands(ctx *commandContext) []*cobra.Command {
	var startDiagnostic bool
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the spindle daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			stdout := cmd.OutOrStdout()
			client, err := ctx.dialClient()
			launched := false
			if err != nil {
				fmt.Fprintln(stdout, "Daemon not running, launching...")
				if launchErr := launchDaemonProcess(cmd, ctx, startDiagnostic); launchErr != nil {
					return launchErr
				}
				client, err = daemonctl.WaitForClient(ctx.socketPath(), 10*time.Second)
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
	startCmd.Flags().BoolVar(&startDiagnostic, "diagnostic", false, "Enable diagnostic mode with separate DEBUG logs")

	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the spindle daemon (completely terminates the process)",
		RunE: func(cmd *cobra.Command, args []string) error {
			stdout := cmd.OutOrStdout()
			result, err := daemonctl.StopAndTerminate(ctx.socketPath(), ctx.configValue(), 5*time.Second)
			if errors.Is(err, daemonctl.ErrDaemonNotRunning) {
				fmt.Fprintln(stdout, "Daemon is not running")
				return nil
			}
			if err != nil {
				return err
			}
			if !result.StopAcknowledged {
				fmt.Fprintln(stdout, "Stop request sent")
			} else {
				fmt.Fprintln(stdout, "Stopping daemon workflow...")
			}
			if result.ForcedKill && result.PID > 0 {
				fmt.Fprintf(stdout, "Stopping daemon process (pid %d)...\n", result.PID)
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
			statusResp, err := daemonctl.BuildStatusSnapshot(cmd.Context(), ctx.socketPath(), cfg)
			if err != nil {
				return err
			}

			stdout := cmd.OutOrStdout()

			colorize := shouldColorize(stdout)

			for _, line := range renderSectionHeader("System Status", colorize) {
				fmt.Fprintln(stdout, line)
			}
			if statusResp.Running {
				fmt.Fprintln(stdout, renderStatusLine("Spindle", statusOK, "Running", colorize))
				if statusResp.DiscPaused {
					fmt.Fprintln(stdout, renderStatusLine("Disc Processing", statusWarn, "Paused", colorize))
				} else {
					fmt.Fprintln(stdout, renderStatusLine("Disc Processing", statusOK, "Active", colorize))
				}
			} else {
				fmt.Fprintln(stdout, renderStatusLine("Spindle", statusWarn, "Not running (run `spindle start`)", colorize))
			}
			fmt.Fprintln(stdout, detectDiscLine(cfg.MakeMKV.OpticalDrive, colorize))
			fmt.Fprintln(stdout, jellyfinStatusLine(cfg, colorize))
			fmt.Fprintln(stdout, openSubtitlesStatusLine(cfg, colorize))
			if strings.TrimSpace(cfg.Notifications.NtfyTopic) != "" {
				fmt.Fprintln(stdout, renderStatusLine("Notifications", statusOK, "Configured", colorize))
			} else {
				fmt.Fprintln(stdout, renderStatusLine("Notifications", statusWarn, "Not configured", colorize))
			}
			fmt.Fprintln(stdout, discDetectionStatusLine(statusResp.Running, statusResp.NetlinkMonitoring, colorize))
			fmt.Fprintln(stdout)

			for _, line := range renderSectionHeader("Dependencies", colorize) {
				fmt.Fprintln(stdout, line)
			}
			for _, line := range dependencyLines(statusResp.Dependencies, colorize) {
				fmt.Fprintln(stdout, line)
			}
			fmt.Fprintln(stdout)

			for _, line := range renderSectionHeader("Library Paths", colorize) {
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
			for _, line := range renderSectionHeader("Queue Status", colorize) {
				fmt.Fprintln(stdout, line)
			}

			rows := buildQueueStatusRows(statusResp.QueueStats)
			if len(rows) == 0 {
				fmt.Fprintln(stdout, "Queue is empty")
				return nil
			}

			table := renderTable([]string{"Status", "Count"}, rows, []columnAlignment{alignLeft, alignRight})
			fmt.Fprint(stdout, table)
			return nil
		},
	}

	var restartDiagnostic bool
	restartCmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the spindle daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			stdout := cmd.OutOrStdout()

			stopResult, err := daemonctl.StopAndTerminate(ctx.socketPath(), ctx.configValue(), 5*time.Second)
			if err != nil && !errors.Is(err, daemonctl.ErrDaemonNotRunning) {
				return err
			}
			if err == nil {
				if stopResult.ForcedKill && stopResult.PID > 0 {
					fmt.Fprintf(stdout, "Stopping daemon process (pid %d)...\n", stopResult.PID)
				}
				fmt.Fprintln(stdout, "Daemon stopped")
			}

			// Start the daemon
			fmt.Fprintln(stdout, "Starting daemon...")
			if launchErr := launchDaemonProcess(cmd, ctx, restartDiagnostic); launchErr != nil {
				return launchErr
			}
			client, err := daemonctl.WaitForClient(ctx.socketPath(), 10*time.Second)
			if err != nil {
				return err
			}
			defer client.Close()

			statusResp, statusErr := client.Status()
			if statusErr == nil && statusResp != nil && statusResp.Running {
				fmt.Fprintln(stdout, "Daemon restarted")
				return nil
			}

			resp, err := client.Start()
			if err != nil {
				return err
			}
			if resp.Started || strings.EqualFold(strings.TrimSpace(resp.Message), "daemon already running") {
				fmt.Fprintln(stdout, "Daemon restarted")
			} else if strings.TrimSpace(resp.Message) != "" {
				fmt.Fprintln(stdout, resp.Message)
			} else {
				fmt.Fprintln(stdout, "Start request sent")
			}
			return nil
		},
	}
	restartCmd.Flags().BoolVar(&restartDiagnostic, "diagnostic", false, "Enable diagnostic mode with separate DEBUG logs")

	return []*cobra.Command{startCmd, stopCmd, restartCmd, statusCmd}
}

func dependencyLines(deps []ipc.DependencyStatus, colorize bool) []string {
	if len(deps) == 0 {
		return nil
	}
	lines := make([]string, 0, len(deps)+1)
	missingRequired := 0
	missingOptional := 0
	for _, dep := range deps {
		if dep.Available {
			continue
		}
		if dep.Optional {
			missingOptional++
		} else {
			missingRequired++
		}
	}
	lines = append(lines, dependencySummaryLine(len(deps), missingRequired, missingOptional, colorize))
	missing := make([]string, 0)
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
		missing = append(missing, dep.Name)
	}
	if len(missing) > 0 {
		lines = append(lines, renderStatusLine("Missing dependencies", statusWarn, fmt.Sprintf("%s (see README.md for install steps)", strings.Join(missing, ", ")), colorize))
	}
	return lines
}

func dependencySummaryLine(total, missingRequired, missingOptional int, colorize bool) string {
	if total <= 0 {
		return renderStatusLine("Summary", statusInfo, "No dependency checks configured", colorize)
	}
	missingCount := missingRequired + missingOptional
	available := total - missingCount
	if missingCount == 0 {
		return renderStatusLine("Summary", statusOK, fmt.Sprintf("%d/%d available", available, total), colorize)
	}

	kind := statusWarn
	if missingRequired > 0 {
		kind = statusError
	}
	detail := fmt.Sprintf("%d/%d available (missing: %d required, %d optional)", available, total, missingRequired, missingOptional)
	return renderStatusLine("Summary", kind, detail, colorize)
}

func launchDaemonProcess(cmd *cobra.Command, ctx *commandContext, diagnostic bool) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	opts := daemonctl.LaunchOptions{Diagnostic: diagnostic}
	if ctx.socketFlag != nil {
		if socket := strings.TrimSpace(*ctx.socketFlag); socket != "" {
			opts.SocketPath = socket
		}
	}
	if ctx.configFlag != nil {
		if config := strings.TrimSpace(*ctx.configFlag); config != "" {
			opts.ConfigPath = config
		}
	}
	return daemonctl.Launch(exe, opts)
}
