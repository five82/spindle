package daemonctl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"spindle/internal/config"
	"spindle/internal/ipc"
	"spindle/internal/preflight"
)

// LaunchOptions controls daemon process launch behavior.
type LaunchOptions struct {
	SocketPath string
	ConfigPath string
	Diagnostic bool
}

// Launch starts a detached spindle daemon process.
func Launch(executablePath string, opts LaunchOptions) error {
	if strings.TrimSpace(executablePath) == "" {
		return fmt.Errorf("resolve executable: executable path is empty")
	}

	args := []string{"daemon"}
	if socket := strings.TrimSpace(opts.SocketPath); socket != "" {
		args = append(args, "--socket", socket)
	}
	if cfg := strings.TrimSpace(opts.ConfigPath); cfg != "" {
		args = append(args, "--config", cfg)
	}
	if opts.Diagnostic {
		args = append(args, "--diagnostic")
	}

	proc := exec.Command(executablePath, args...)
	if err := proc.Start(); err != nil {
		return fmt.Errorf("launch daemon: %w", err)
	}
	return proc.Process.Release()
}

// WaitForClient waits for IPC socket availability and returns a connected client.
func WaitForClient(socketPath string, timeout time.Duration) (*ipc.Client, error) {
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

// WaitForShutdown waits for daemon IPC to disappear or report not-running.
func WaitForShutdown(socketPath string, timeout time.Duration) error {
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

// ProcessInfo returns whether daemon IPC is reachable and the daemon PID when available.
func ProcessInfo(socketPath string) (bool, int, error) {
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

// DeriveLogDir determines daemon log directory from status and config hints.
func DeriveLogDir(lockPath, queueDBPath string, cfg *config.Config) string {
	if lockPath != "" {
		return filepath.Dir(lockPath)
	}
	if queueDBPath != "" {
		return filepath.Dir(queueDBPath)
	}
	if cfg != nil && strings.TrimSpace(cfg.Paths.LogDir) != "" {
		return cfg.Paths.LogDir
	}
	return ""
}

// ForceKillProcess sends SIGKILL to daemon process and cleans pid/lock files.
func ForceKillProcess(pidPath, lockPath string, fallbackPID int) (int, error) {
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

// ResolveDependencies returns current dependency availability for status output.
func ResolveDependencies(ctx context.Context, cfg *config.Config) []ipc.DependencyStatus {
	if cfg == nil {
		return nil
	}

	checks := preflight.CheckSystemDeps(ctx, cfg)
	statuses := make([]ipc.DependencyStatus, 0, len(checks)+1)
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

	if cfg.Commentary.Enabled {
		statuses = append(statuses, commentaryLLMDependencyStatus(ctx, cfg))
	}

	return statuses
}

func commentaryLLMDependencyStatus(ctx context.Context, cfg *config.Config) ipc.DependencyStatus {
	status := ipc.DependencyStatus{
		Name:        "Commentary LLM",
		Description: "LLM-driven commentary track detection",
		Optional:    true,
	}
	llmCfg := cfg.CommentaryLLM()
	if llmCfg.APIKey == "" {
		status.Detail = "API key missing (set commentary.api_key or llm.api_key)"
		return status
	}
	result := preflight.CheckLLM(ctx, "Commentary LLM", llmCfg)
	status.Available = result.Passed
	status.Detail = result.Detail
	return status
}
