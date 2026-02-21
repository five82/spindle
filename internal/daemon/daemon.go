package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofrs/flock"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/preflight"
	"spindle/internal/queue"
	"spindle/internal/workflow"
)

type Daemon struct {
	cfg        *config.Config
	logger     *slog.Logger
	store      *queue.Store
	workflow   *workflow.Manager
	logPath    string
	logHub     *logging.StreamHub
	logArchive *logging.EventArchive
	monitor    *discMonitor
	netlink    *netlinkMonitor
	apiSrv     *apiServer

	lockPath string
	lock     *flock.Flock

	running    atomic.Bool
	discPaused atomic.Bool
	ctx        context.Context
	cancel     context.CancelFunc

	depsMu       sync.RWMutex
	dependencies []DependencyStatus
	notifier     notifications.Service
}

// Status represents daemon runtime information.
type Status struct {
	Running           bool
	DiscPaused        bool
	NetlinkMonitoring bool
	Workflow          workflow.StatusSummary
	QueueDBPath       string
	LockFilePath      string
	Dependencies      []DependencyStatus
	PID               int
}

// DependencyStatus reports the availability of an external requirement.
type DependencyStatus struct {
	Name        string
	Command     string
	Description string
	Optional    bool
	Available   bool
	Detail      string
}

// New constructs a daemon with initialized dependencies.
func New(cfg *config.Config, store *queue.Store, logger *slog.Logger, wf *workflow.Manager, logPath string, hub *logging.StreamHub, archive *logging.EventArchive, notifier notifications.Service) (*Daemon, error) {
	if cfg == nil || store == nil || logger == nil || wf == nil {
		return nil, errors.New("daemon requires config, store, logger, and workflow manager")
	}
	if strings.TrimSpace(logPath) == "" {
		return nil, errors.New("daemon requires log path")
	}

	lockPath := filepath.Join(cfg.Paths.LogDir, "spindle.lock")
	daemon := &Daemon{
		cfg:        cfg,
		logger:     logger,
		store:      store,
		workflow:   wf,
		logPath:    logPath,
		logHub:     hub,
		logArchive: archive,
		lockPath:   lockPath,
		lock:       flock.New(lockPath),
		notifier:   notifier,
	}
	daemon.monitor = newDiscMonitor(cfg, store, logger, daemon.DiscPaused, notifier)
	daemon.netlink = newNetlinkMonitor(cfg, logger, daemon.HandleDiscDetected, daemon.DiscPaused)
	apiSrv, err := newAPIServer(cfg, daemon, logger)
	if err != nil {
		return nil, err
	}
	daemon.apiSrv = apiSrv

	// Register disc pause hooks to stop monitoring during rips.
	wf.SetRipHooks(daemon)

	return daemon, nil
}

// Start launches the workflow manager and acquires the daemon lock.
func (d *Daemon) Start(ctx context.Context) error {
	if d.running.Load() {
		return errors.New("daemon already running")
	}

	ok, err := d.lock.TryLock()
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	if !ok {
		return errors.New("another spindle daemon instance is already running")
	}

	if err := d.runDependencyChecks(ctx); err != nil {
		_ = d.lock.Unlock()
		return err
	}

	d.ctx, d.cancel = context.WithCancel(ctx)
	if err := d.workflow.Start(d.ctx); err != nil {
		_ = d.lock.Unlock()
		d.cancel()
		d.ctx = nil
		d.cancel = nil
		return fmt.Errorf("start workflow: %w", err)
	}
	if d.monitor != nil {
		if err := d.monitor.Start(d.ctx); err != nil {
			d.workflow.Stop()
			d.cancel()
			d.ctx = nil
			d.cancel = nil
			_ = d.lock.Unlock()
			return fmt.Errorf("start disc monitor: %w", err)
		}
	}
	if d.netlink != nil {
		// Netlink monitor start is non-fatal - if it fails, log a warning
		// but continue with manual detection via IPC
		if err := d.netlink.Start(d.ctx); err != nil {
			d.logger.Warn("netlink monitor failed to start; automatic disc detection unavailable",
				logging.Error(err),
				logging.String(logging.FieldEventType, "netlink_start_failed"),
				logging.String(logging.FieldImpact, "disc detection requires manual trigger via spindle disc detected"),
			)
		}
	}
	if d.apiSrv != nil {
		if err := d.apiSrv.start(d.ctx); err != nil {
			if d.monitor != nil {
				d.monitor.Stop()
			}
			d.workflow.Stop()
			d.cancel()
			d.ctx = nil
			d.cancel = nil
			_ = d.lock.Unlock()
			return fmt.Errorf("start api server: %w", err)
		}
	}

	d.running.Store(true)
	d.logger.Info("spindle daemon started",
		logging.String("lock", d.lockPath),
		logging.String(logging.FieldEventType, "daemon_start"),
	)
	return nil
}

// Stop stops background processing and releases the daemon lock.
// The passed context is used as the parent for shutdown timeouts;
// pass context.Background() if no external cancellation is needed.
func (d *Daemon) Stop(ctx context.Context) {
	if !d.running.Load() {
		return
	}

	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	if d.netlink != nil {
		d.netlink.Stop()
	}
	if d.monitor != nil {
		d.monitor.Stop()
	}
	if d.apiSrv != nil {
		d.apiSrv.stop(ctx)
	}
	d.workflow.Stop()

	// Mark all active queue items as failed so they require explicit retry on restart
	if d.store != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if count, err := d.store.FailActiveOnShutdown(shutdownCtx); err != nil {
			d.logger.Warn("failed to mark active items as failed on shutdown",
				logging.Error(err),
				logging.String(logging.FieldEventType, "shutdown_fail_active_error"),
				logging.String(logging.FieldErrorHint, "items may auto-resume on next start; use queue retry to control"),
				logging.String(logging.FieldImpact, "active items may resume automatically on daemon restart"))
		} else if count > 0 {
			d.logger.Info("marked active items as failed on shutdown",
				logging.Int64("count", count),
				logging.String(logging.FieldEventType, "shutdown_fail_active"),
			)
		}
	}

	if err := d.lock.Unlock(); err != nil {
		d.logger.Warn("failed to release daemon lock",
			logging.Error(err),
			logging.String(logging.FieldEventType, "daemon_lock_release_failed"),
			logging.String(logging.FieldImpact, "stale lock may block future daemon starts"),
			logging.String(logging.FieldErrorHint, "Run spindle stop again or remove the lock file manually"))
	}
	d.ctx = nil
	d.running.Store(false)
	d.logger.Info("spindle daemon stopped",
		logging.String(logging.FieldEventType, "daemon_stop"),
	)
}

// Close releases resources held by the daemon.
func (d *Daemon) Close() error {
	d.Stop(context.Background())
	if d.logArchive != nil {
		_ = d.logArchive.Close()
	}
	if d.store != nil {
		return d.store.Close()
	}
	return nil
}

// ListQueue returns queue items filtered by optional statuses.
func (d *Daemon) ListQueue(ctx context.Context, statuses []queue.Status) ([]*queue.Item, error) {
	if d.store == nil {
		return nil, errors.New("queue store unavailable")
	}
	if len(statuses) == 0 {
		return d.store.List(ctx)
	}
	return d.store.List(ctx, statuses...)
}

// GetQueueItem fetches a single queue item by identifier.
func (d *Daemon) GetQueueItem(ctx context.Context, id int64) (*queue.Item, error) {
	if d.store == nil {
		return nil, errors.New("queue store unavailable")
	}
	return d.store.GetByID(ctx, id)
}

// ClearQueue removes all queue items.
func (d *Daemon) ClearQueue(ctx context.Context) (int64, error) {
	if d.store == nil {
		return 0, errors.New("queue store unavailable")
	}
	return d.store.Clear(ctx)
}

// ClearCompleted removes only completed queue items.
func (d *Daemon) ClearCompleted(ctx context.Context) (int64, error) {
	if d.store == nil {
		return 0, errors.New("queue store unavailable")
	}
	return d.store.ClearCompleted(ctx)
}

// ClearFailed removes only failed queue items.
func (d *Daemon) ClearFailed(ctx context.Context) (int64, error) {
	if d.store == nil {
		return 0, errors.New("queue store unavailable")
	}
	return d.store.ClearFailed(ctx)
}

// ResetStuck transitions in-flight items back to pending for retry.
func (d *Daemon) ResetStuck(ctx context.Context) (int64, error) {
	if d.store == nil {
		return 0, errors.New("queue store unavailable")
	}
	return d.store.ResetStuckProcessing(ctx)
}

// RetryFailed resets failed items (optionally a subset) back to pending.
func (d *Daemon) RetryFailed(ctx context.Context, ids []int64) (int64, error) {
	if d.store == nil {
		return 0, errors.New("queue store unavailable")
	}
	return d.store.RetryFailed(ctx, ids...)
}

// StopQueueItems moves items into review to halt further processing.
func (d *Daemon) StopQueueItems(ctx context.Context, ids []int64) (int64, error) {
	if d.store == nil {
		return 0, errors.New("queue store unavailable")
	}
	return d.store.StopItems(ctx, ids...)
}

// RemoveQueueItems deletes specific items from the queue by ID.
func (d *Daemon) RemoveQueueItems(ctx context.Context, ids []int64) (int64, error) {
	if d.store == nil {
		return 0, errors.New("queue store unavailable")
	}
	var removed int64
	for _, id := range ids {
		ok, err := d.store.Remove(ctx, id)
		if err != nil {
			return removed, err
		}
		if ok {
			removed++
		}
	}
	return removed, nil
}

// QueueHealth returns aggregate queue diagnostics.
func (d *Daemon) QueueHealth(ctx context.Context) (queue.HealthSummary, error) {
	if d.store == nil {
		return queue.HealthSummary{}, errors.New("queue store unavailable")
	}
	return d.store.Health(ctx)
}

// DatabaseHealth returns detailed database diagnostics.
func (d *Daemon) DatabaseHealth(ctx context.Context) (queue.DatabaseHealth, error) {
	if d.store == nil {
		return queue.DatabaseHealth{}, errors.New("queue store unavailable")
	}
	return d.store.CheckHealth(ctx)
}

// TestNotification triggers a test notification using the current configuration.
func (d *Daemon) TestNotification(ctx context.Context) (bool, string, error) {
	if d.cfg == nil {
		return false, "configuration unavailable", errors.New("configuration unavailable")
	}
	if strings.TrimSpace(d.cfg.Notifications.NtfyTopic) == "" {
		return false, "ntfy topic not configured", nil
	}
	if err := d.notifier.Publish(ctx, notifications.EventTestNotification, nil); err != nil {
		return false, "failed to send notification", err
	}
	return true, "test notification sent", nil
}

// LogPath returns the path to the daemon log file.
func (d *Daemon) LogPath() string {
	if d == nil {
		return ""
	}
	return d.logPath
}

// LogStream exposes the live log event hub.
func (d *Daemon) LogStream() *logging.StreamHub {
	if d == nil {
		return nil
	}
	return d.logHub
}

// LogArchive exposes the on-disk event archive used for API history.
func (d *Daemon) LogArchive() *logging.EventArchive {
	if d == nil {
		return nil
	}
	return d.logArchive
}

// Status returns the current daemon status.
func (d *Daemon) Status(ctx context.Context) Status {
	summary := d.workflow.Status(ctx)

	d.depsMu.RLock()
	dependencies := make([]DependencyStatus, len(d.dependencies))
	copy(dependencies, d.dependencies)
	d.depsMu.RUnlock()

	netlinkRunning := false
	if d.netlink != nil {
		netlinkRunning = d.netlink.Running()
	}

	return Status{
		Running:           d.running.Load(),
		DiscPaused:        d.discPaused.Load(),
		NetlinkMonitoring: netlinkRunning,
		Workflow:          summary,
		QueueDBPath:       filepath.Join(d.cfg.Paths.LogDir, "queue.db"),
		LockFilePath:      d.lockPath,
		Dependencies:      dependencies,
		PID:               os.Getpid(),
	}
}

// PauseDisc pauses detection of new disc insertions. Returns true if state changed.
func (d *Daemon) PauseDisc() bool {
	return d.discPaused.CompareAndSwap(false, true)
}

// ResumeDisc resumes detection of new disc insertions. Returns true if state changed.
func (d *Daemon) ResumeDisc() bool {
	return d.discPaused.CompareAndSwap(true, false)
}

// DiscPaused reports whether disc detection is paused.
func (d *Daemon) DiscPaused() bool {
	return d.discPaused.Load()
}

// DiscDetectedResult contains the outcome of a disc detection event.
type DiscDetectedResult struct {
	Handled bool
	Message string
	ItemID  int64
}

// HandleDiscDetect processes a disc detection request using the configured device.
func (d *Daemon) HandleDiscDetect(ctx context.Context) (*DiscDetectedResult, error) {
	if d.monitor == nil {
		return &DiscDetectedResult{
			Handled: false,
			Message: "disc monitor not available",
		}, nil
	}
	if d.discPaused.Load() {
		return &DiscDetectedResult{
			Handled: false,
			Message: "disc detection paused",
		}, nil
	}
	return d.monitor.HandleDetection(ctx)
}

// HandleDiscDetected processes a disc detection event for a specific device.
// Used by the netlink monitor when a disc insertion is detected.
func (d *Daemon) HandleDiscDetected(ctx context.Context, device string) (*DiscDetectedResult, error) {
	if d.monitor == nil {
		return &DiscDetectedResult{
			Handled: false,
			Message: "disc monitor not available",
		}, nil
	}
	if d.discPaused.Load() {
		return &DiscDetectedResult{
			Handled: false,
			Message: "disc detection paused",
		}, nil
	}
	return d.monitor.HandleDetectionForDevice(ctx, device)
}

// BeforeRip implements workflow.RipHooks. Called before MakeMKV starts reading.
func (d *Daemon) BeforeRip() {
	if d.PauseDisc() {
		d.logger.Debug("paused disc monitoring for rip",
			logging.String(logging.FieldEventType, "disc_monitor_paused"),
			logging.String("reason", "rip_started"),
		)
	}
}

// AfterRip implements workflow.RipHooks. Called after ripping completes.
func (d *Daemon) AfterRip() {
	if d.ResumeDisc() {
		d.logger.Debug("resumed disc monitoring after rip",
			logging.String(logging.FieldEventType, "disc_monitor_resumed"),
			logging.String("reason", "rip_completed"),
		)
	}
}

func (d *Daemon) runDependencyChecks(ctx context.Context) error {
	results := preflight.CheckSystemDeps(ctx, d.cfg)
	d.depsMu.Lock()
	d.dependencies = make([]DependencyStatus, len(results))
	for i, result := range results {
		d.dependencies[i] = DependencyStatus{
			Name:        result.Name,
			Command:     result.Command,
			Description: result.Description,
			Optional:    result.Optional,
			Available:   result.Available,
			Detail:      result.Detail,
		}
	}
	d.depsMu.Unlock()

	for _, status := range results {
		if status.Available {
			continue
		}
		fields := []logging.Attr{
			logging.String("dependency", status.Name),
			logging.String("command", status.Command),
		}
		if status.Detail != "" {
			fields = append(fields, logging.String("detail", status.Detail))
		}
		if status.Optional {
			fields = append(fields,
				logging.Bool("optional", true),
				logging.String(logging.FieldEventType, "dependency_unavailable"),
				logging.String(logging.FieldErrorHint, "install the dependency or disable the feature in config"),
			)
			d.logger.Warn("optional dependency unavailable; related features disabled", logging.Args(fields...)...)
		} else {
			fields = append(fields,
				logging.String(logging.FieldEventType, "dependency_unavailable"),
				logging.String(logging.FieldErrorHint, "install the dependency or update the configured binary path; see README.md"),
			)
			d.logger.Error("required dependency unavailable; daemon startup blocked", logging.Args(fields...)...)
			if d.notifier != nil {
				_ = d.notifier.Publish(ctx, notifications.EventError, notifications.Payload{
					"context": fmt.Sprintf("dependency %s", status.Name),
					"error":   status.Detail,
				})
			}
		}
	}
	missing := make([]string, 0)
	for _, status := range results {
		if status.Available || status.Optional {
			continue
		}
		missing = append(missing, status.Name)
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required dependencies: %s (see README.md)", strings.Join(missing, ", "))
	}
	return nil
}
