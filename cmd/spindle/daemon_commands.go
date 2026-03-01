package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/api"
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
			exe, err := daemonExecutable()
			if err != nil {
				return err
			}

			result, err := daemonctl.EnsureStarted(
				ctx.socketPath(),
				exe,
				daemonLaunchOptions(ctx, startDiagnostic),
				10*time.Second,
			)
			if err != nil {
				return err
			}

			if result.Launched {
				fmt.Fprintln(stdout, "Daemon not running, launching...")
			}

			switch result.State {
			case daemonctl.StartStateStarted:
				fmt.Fprintln(stdout, "Daemon started")
			case daemonctl.StartStateAlreadyRunning:
				fmt.Fprintln(stdout, "Daemon already running")
			case daemonctl.StartStateRequested:
				if strings.TrimSpace(result.Message) != "" {
					fmt.Fprintln(stdout, result.Message)
					return nil
				}
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
			for _, line := range statusResp.SystemChecks {
				fmt.Fprintln(stdout, renderStatusLine(line.Label, statusKindFromSeverity(line.Severity), line.Detail, colorize))
			}
			fmt.Fprintln(stdout)

			for _, line := range renderSectionHeader("Dependencies", colorize) {
				fmt.Fprintln(stdout, line)
			}
			for _, line := range dependencyLines(statusResp.Dependencies, statusResp.DependencySummary, colorize) {
				fmt.Fprintln(stdout, line)
			}
			fmt.Fprintln(stdout)

			for _, line := range renderSectionHeader("Library Paths", colorize) {
				fmt.Fprintln(stdout, line)
			}
			for _, line := range statusResp.LibraryPaths {
				fmt.Fprintln(stdout, renderStatusLine(line.Label, statusKindFromSeverity(line.Severity), line.Detail, colorize))
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
			exe, err := daemonExecutable()
			if err != nil {
				return err
			}

			result, err := daemonctl.Restart(
				ctx.socketPath(),
				ctx.configValue(),
				exe,
				daemonLaunchOptions(ctx, restartDiagnostic),
				5*time.Second,
				10*time.Second,
			)
			if err != nil {
				return err
			}

			if result.WasRunning {
				if result.Stop.ForcedKill && result.Stop.PID > 0 {
					fmt.Fprintf(stdout, "Stopping daemon process (pid %d)...\n", result.Stop.PID)
				}
				fmt.Fprintln(stdout, "Daemon stopped")
			}

			switch result.Start.State {
			case daemonctl.StartStateStarted, daemonctl.StartStateAlreadyRunning:
				fmt.Fprintln(stdout, "Daemon restarted")
			case daemonctl.StartStateRequested:
				if strings.TrimSpace(result.Start.Message) != "" {
					fmt.Fprintln(stdout, result.Start.Message)
					return nil
				}
				fmt.Fprintln(stdout, "Start request sent")
			}
			return nil
		},
	}
	restartCmd.Flags().BoolVar(&restartDiagnostic, "diagnostic", false, "Enable diagnostic mode with separate DEBUG logs")

	return []*cobra.Command{startCmd, stopCmd, restartCmd, statusCmd}
}

func dependencyLines(deps []ipc.DependencyStatus, summary api.DependencySummary, colorize bool) []string {
	lines := make([]string, 0, len(deps)+1)
	lines = append(lines, renderStatusLine("Summary", statusKindFromSeverity(summary.Severity), summary.Detail, colorize))
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
		kind := statusKindFromSeverity(dep.Severity)
		lines = append(lines, renderStatusLine(dep.Name, kind, detail, colorize))
		missing = append(missing, dep.Name)
	}
	if len(missing) > 0 {
		lines = append(lines, renderStatusLine("Missing dependencies", statusWarn, fmt.Sprintf("%s (see README.md for install steps)", strings.Join(missing, ", ")), colorize))
	}
	return lines
}

func daemonExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	return exe, nil
}

func daemonLaunchOptions(ctx *commandContext, diagnostic bool) daemonctl.LaunchOptions {
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
	return opts
}
