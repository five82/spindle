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
	"syscall"
	"time"

	"spindle/internal/api"
	"spindle/internal/config"
	"spindle/internal/ipc"
	"spindle/internal/preflight"
	"spindle/internal/queue"
)

// LaunchOptions controls daemon process launch behavior.
type LaunchOptions struct {
	SocketPath string
	ConfigPath string
	Diagnostic bool
}

type StartState string

const (
	StartStateStarted        StartState = "started"
	StartStateAlreadyRunning StartState = "already_running"
	StartStateRequested      StartState = "start_requested"
)

// StartResult captures daemon start orchestration state.
type StartResult struct {
	State    StartState
	Launched bool
	Message  string
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

// EnsureStarted launches and/or starts the daemon and returns the resulting state.
func EnsureStarted(socketPath, executablePath string, opts LaunchOptions, waitTimeout time.Duration) (StartResult, error) {
	client, err := ipc.Dial(socketPath)
	launched := false
	if err != nil {
		if launchErr := Launch(executablePath, opts); launchErr != nil {
			return StartResult{}, launchErr
		}
		client, err = WaitForClient(socketPath, waitTimeout)
		if err != nil {
			return StartResult{}, err
		}
		launched = true
	}
	defer client.Close()

	statusResp, statusErr := client.Status()
	if statusErr == nil && statusResp != nil && statusResp.Running {
		if launched {
			return StartResult{State: StartStateStarted, Launched: true}, nil
		}
		return StartResult{State: StartStateAlreadyRunning}, nil
	}

	resp, err := client.Start()
	if err != nil {
		return StartResult{}, err
	}

	if resp != nil {
		message := strings.TrimSpace(resp.Message)
		if resp.Started {
			return StartResult{State: StartStateStarted, Launched: launched, Message: message}, nil
		}
		if strings.EqualFold(message, "daemon already running") {
			if launched {
				return StartResult{State: StartStateStarted, Launched: true, Message: message}, nil
			}
			return StartResult{State: StartStateAlreadyRunning, Message: message}, nil
		}
		if message != "" {
			return StartResult{State: StartStateRequested, Launched: launched, Message: message}, nil
		}
	}

	return StartResult{State: StartStateRequested, Launched: launched, Message: "Start request sent"}, nil
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

// ErrDaemonNotRunning indicates daemon IPC is unavailable.
var ErrDaemonNotRunning = errors.New("daemon not running")

// StopResult captures daemon stop/termination outcome.
type StopResult struct {
	StopAcknowledged bool
	ForcedKill       bool
	PID              int
}

// RestartResult captures stop/start outcomes for daemon restart.
type RestartResult struct {
	WasRunning bool
	Stop       StopResult
	Start      StartResult
}

// StopAndTerminate requests daemon stop and force-kills the process if still alive after gracePeriod.
func StopAndTerminate(socketPath string, cfg *config.Config, gracePeriod time.Duration) (StopResult, error) {
	client, err := ipc.Dial(socketPath)
	if err != nil {
		if isDaemonUnavailable(err) {
			return StopResult{}, ErrDaemonNotRunning
		}
		return StopResult{}, err
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
		return StopResult{}, err
	}
	result := StopResult{PID: pid}
	if resp != nil {
		result.StopAcknowledged = resp.Stopped
	}

	_ = WaitForShutdown(socketPath, gracePeriod)
	alive, livePID, aliveErr := ProcessInfo(socketPath)
	if aliveErr != nil {
		alive = false
	}
	if !alive {
		return result, nil
	}

	currentPID := livePID
	if currentPID == 0 {
		currentPID = pid
	}
	logDir := DeriveLogDir(lockPath, queueDBPath, cfg)
	if logDir == "" {
		return result, fmt.Errorf("unable to determine daemon log directory")
	}
	pidPath := filepath.Join(logDir, "spindle.pid")
	lockFile := filepath.Join(logDir, "spindle.lock")
	killedPID, killErr := ForceKillProcess(pidPath, lockFile, currentPID)
	if killErr != nil {
		return result, fmt.Errorf("failed to stop daemon process: %w", killErr)
	}
	_ = os.Remove(socketPath)
	result.ForcedKill = true
	result.PID = killedPID
	return result, nil
}

// Restart stops the daemon if running, then ensures it is started.
func Restart(socketPath string, cfg *config.Config, executablePath string, opts LaunchOptions, stopGracePeriod, startWaitTimeout time.Duration) (RestartResult, error) {
	stopResult, stopErr := StopAndTerminate(socketPath, cfg, stopGracePeriod)
	if stopErr != nil && !errors.Is(stopErr, ErrDaemonNotRunning) {
		return RestartResult{}, stopErr
	}

	startResult, err := EnsureStarted(socketPath, executablePath, opts, startWaitTimeout)
	if err != nil {
		return RestartResult{}, err
	}

	return RestartResult{
		WasRunning: stopErr == nil,
		Stop:       stopResult,
		Start:      startResult,
	}, nil
}

// BuildStatusSnapshot collects daemon status and applies offline fallbacks for queue stats and dependencies.
func BuildStatusSnapshot(ctx context.Context, socketPath string, cfg *config.Config) (*ipc.StatusResponse, error) {
	if cfg == nil {
		return nil, errors.New("configuration not available")
	}
	statusResp := &ipc.StatusResponse{}

	client, err := ipc.Dial(socketPath)
	if err == nil {
		defer client.Close()
		if resp, statusErr := client.Status(); statusErr == nil && resp != nil {
			statusResp = resp
		}
	}

	queueStats := make(map[string]int, len(statusResp.QueueStats))
	for k, v := range statusResp.QueueStats {
		queueStats[k] = v
	}

	if !statusResp.Running {
		queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
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
			statusResp.Dependencies = ResolveDependencies(context.Background(), cfg)
		}
	}

	if len(statusResp.Dependencies) == 0 {
		statusResp.Dependencies = ResolveDependencies(context.Background(), cfg)
	}
	for i := range statusResp.Dependencies {
		if strings.TrimSpace(statusResp.Dependencies[i].Severity) != "" {
			continue
		}
		severity := "ok"
		if !statusResp.Dependencies[i].Available {
			severity = "error"
			if statusResp.Dependencies[i].Optional {
				severity = "warn"
			}
		}
		statusResp.Dependencies[i].Severity = severity
	}

	statusResp.QueueStats = queueStats
	statusResp.SystemChecks = BuildSystemChecks(cfg, statusResp.Running, statusResp.DiscPaused, statusResp.NetlinkMonitoring)
	statusResp.LibraryPaths = BuildLibraryPathChecks(cfg)
	statusResp.DependencySummary = BuildDependencySummary(statusResp.Dependencies)
	return statusResp, nil
}

func isDaemonUnavailable(err error) bool {
	return os.IsNotExist(err) ||
		errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, syscall.ECONNREFUSED)
}

// ResolveDependencies returns current dependency availability for status output.
func ResolveDependencies(ctx context.Context, cfg *config.Config) []ipc.DependencyStatus {
	if cfg == nil {
		return nil
	}

	checks := preflight.CheckSystemDeps(ctx, cfg)
	statuses := make([]ipc.DependencyStatus, 0, len(checks)+1)
	for _, check := range checks {
		severity := "ok"
		if !check.Available {
			severity = "error"
			if check.Optional {
				severity = "warn"
			}
		}
		statuses = append(statuses, ipc.DependencyStatus{
			Name:        check.Name,
			Command:     check.Command,
			Description: check.Description,
			Optional:    check.Optional,
			Available:   check.Available,
			Detail:      check.Detail,
			Severity:    severity,
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
		Severity:    "warn",
	}
	llmCfg := cfg.CommentaryLLM()
	if llmCfg.APIKey == "" {
		status.Detail = "API key missing (set commentary.api_key or llm.api_key)"
		return status
	}
	result := preflight.CheckLLM(ctx, "Commentary LLM", llmCfg)
	status.Available = result.Passed
	status.Detail = result.Detail
	if status.Available {
		status.Severity = "ok"
	}
	return status
}

// BuildSystemChecks resolves status lines that combine runtime state and config checks.
func BuildSystemChecks(cfg *config.Config, daemonRunning, discPaused, netlinkActive bool) []api.StatusLine {
	lines := make([]api.StatusLine, 0, 6)
	if daemonRunning {
		lines = append(lines, api.StatusLine{Label: "Spindle", Severity: "ok", Detail: "Running"})
		if discPaused {
			lines = append(lines, api.StatusLine{Label: "Disc Processing", Severity: "warn", Detail: "Paused"})
		} else {
			lines = append(lines, api.StatusLine{Label: "Disc Processing", Severity: "ok", Detail: "Active"})
		}
	} else {
		lines = append(lines, api.StatusLine{Label: "Spindle", Severity: "warn", Detail: "Not running (run `spindle start`)"})
	}

	probe := preflight.ProbeDisc(cfg.MakeMKV.OpticalDrive)
	if !probe.Detected {
		lines = append(lines, api.StatusLine{Label: "Disc", Severity: "info", Detail: "No disc detected"})
	} else {
		lines = append(lines, api.StatusLine{Label: "Disc", Severity: "ok", Detail: probe.DiscDetail()})
	}

	jellyfin := preflight.CheckJellyfinFromConfig(cfg)
	switch {
	case jellyfin.Passed:
		lines = append(lines, api.StatusLine{Label: "Jellyfin", Severity: "ok", Detail: jellyfin.Detail})
	case strings.EqualFold(strings.TrimSpace(jellyfin.Detail), "Unknown"):
		lines = append(lines, api.StatusLine{Label: "Jellyfin", Severity: "info", Detail: jellyfin.Detail})
	default:
		lines = append(lines, api.StatusLine{Label: "Jellyfin", Severity: "warn", Detail: jellyfin.Detail})
	}

	openSubs := preflight.CheckOpenSubtitlesFromConfig(cfg)
	switch {
	case openSubs.Passed && strings.EqualFold(strings.TrimSpace(openSubs.Detail), "Disabled"):
		lines = append(lines, api.StatusLine{Label: "OpenSubtitles", Severity: "info", Detail: openSubs.Detail})
	case openSubs.Passed:
		lines = append(lines, api.StatusLine{Label: "OpenSubtitles", Severity: "ok", Detail: openSubs.Detail})
	case strings.EqualFold(strings.TrimSpace(openSubs.Detail), "Unknown"):
		lines = append(lines, api.StatusLine{Label: "OpenSubtitles", Severity: "info", Detail: openSubs.Detail})
	default:
		lines = append(lines, api.StatusLine{Label: "OpenSubtitles", Severity: "warn", Detail: openSubs.Detail})
	}

	if strings.TrimSpace(cfg.Notifications.NtfyTopic) != "" {
		lines = append(lines, api.StatusLine{Label: "Notifications", Severity: "ok", Detail: "Configured"})
	} else {
		lines = append(lines, api.StatusLine{Label: "Notifications", Severity: "warn", Detail: "Not configured"})
	}

	if netlinkActive {
		lines = append(lines, api.StatusLine{Label: "Disc Detection", Severity: "ok", Detail: "Netlink monitoring active"})
	} else if !daemonRunning {
		lines = append(lines, api.StatusLine{Label: "Disc Detection", Severity: "info", Detail: "Inactive (daemon not running)"})
	} else {
		lines = append(lines, api.StatusLine{Label: "Disc Detection", Severity: "warn", Detail: "Netlink unavailable (manual detection via 'spindle disc detect')"})
	}

	return lines
}

// BuildLibraryPathChecks resolves configured library path readiness.
func BuildLibraryPathChecks(cfg *config.Config) []api.StatusLine {
	lines := make([]api.StatusLine, 0, 3)
	for _, dir := range []struct {
		label string
		path  string
	}{
		{label: "Library", path: cfg.Paths.LibraryDir},
		{label: "Movies", path: librarySubdirPath(cfg.Paths.LibraryDir, cfg.Library.MoviesDir)},
		{label: "TV", path: librarySubdirPath(cfg.Paths.LibraryDir, cfg.Library.TVDir)},
	} {
		result := preflight.CheckDirectoryAccess(dir.label, dir.path)
		severity := "error"
		if result.Passed {
			severity = "ok"
		}
		lines = append(lines, api.StatusLine{
			Label:    dir.label,
			Severity: severity,
			Detail:   result.Detail,
		})
	}
	return lines
}

// BuildDependencySummary computes aggregate dependency readiness.
func BuildDependencySummary(deps []ipc.DependencyStatus) api.DependencySummary {
	if len(deps) == 0 {
		return api.DependencySummary{
			Severity: "info",
			Detail:   "No dependency checks configured",
		}
	}

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

	missingCount := missingRequired + missingOptional
	available := len(deps) - missingCount
	severity := "ok"
	if missingRequired > 0 {
		severity = "error"
	} else if missingOptional > 0 {
		severity = "warn"
	}
	detail := fmt.Sprintf("%d/%d available (missing: %d required, %d optional)", available, len(deps), missingRequired, missingOptional)
	if missingCount == 0 {
		detail = fmt.Sprintf("%d/%d available", available, len(deps))
	}

	return api.DependencySummary{
		Total:           len(deps),
		Available:       available,
		MissingRequired: missingRequired,
		MissingOptional: missingOptional,
		Severity:        severity,
		Detail:          detail,
	}
}

func librarySubdirPath(root, child string) string {
	child = strings.TrimSpace(child)
	if child == "" {
		return root
	}
	if filepath.IsAbs(child) {
		return child
	}
	return filepath.Join(root, child)
}
