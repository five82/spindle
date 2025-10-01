package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gofrs/flock"
	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/deps"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/workflow"
)

// Daemon coordinates the background processing services and enforces single-instance execution.
var manualFileExtensions = map[string]struct{}{
	".mkv": {},
	".mp4": {},
	".avi": {},
}

type Daemon struct {
	cfg      *config.Config
	logger   *zap.Logger
	store    *queue.Store
	workflow *workflow.Manager
	logPath  string
	monitor  *discMonitor

	lockPath string
	lock     *flock.Flock

	running atomic.Bool
	ctx     context.Context
	cancel  context.CancelFunc

	depsMu       sync.RWMutex
	dependencies []DependencyStatus
	notifier     notifications.Service
}

// Status represents daemon runtime information.
type Status struct {
	Running      bool
	Workflow     workflow.StatusSummary
	QueueDBPath  string
	LockFilePath string
	Dependencies []DependencyStatus
	PID          int
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
func New(cfg *config.Config, store *queue.Store, logger *zap.Logger, wf *workflow.Manager, logPath string) (*Daemon, error) {
	if cfg == nil || store == nil || logger == nil || wf == nil {
		return nil, errors.New("daemon requires config, store, logger, and workflow manager")
	}
	if strings.TrimSpace(logPath) == "" {
		return nil, errors.New("daemon requires log path")
	}

	lockPath := filepath.Join(cfg.LogDir, "spindle.lock")
	monitor := newDiscMonitor(cfg, store, logger)
	return &Daemon{
		cfg:      cfg,
		logger:   logger,
		store:    store,
		workflow: wf,
		logPath:  logPath,
		lockPath: lockPath,
		lock:     flock.New(lockPath),
		monitor:  monitor,
		notifier: notifications.NewService(cfg),
	}, nil
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

	d.running.Store(true)
	d.logger.Info("spindle daemon started", zap.String("lock", d.lockPath))
	d.runDependencyChecks(ctx)
	return nil
}

// Stop stops background processing and releases the daemon lock.
func (d *Daemon) Stop() {
	if !d.running.Load() {
		return
	}

	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	if d.monitor != nil {
		d.monitor.Stop()
	}
	d.workflow.Stop()
	if err := d.lock.Unlock(); err != nil {
		d.logger.Warn("failed to release daemon lock", zap.Error(err))
	}
	d.ctx = nil
	d.running.Store(false)
	d.logger.Info("spindle daemon stopped")
}

// Close releases resources held by the daemon.
func (d *Daemon) Close() error {
	d.Stop()
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
	if strings.TrimSpace(d.cfg.NtfyTopic) == "" {
		return false, "ntfy topic not configured", nil
	}
	notifier := notifications.NewService(d.cfg)
	if err := notifier.Publish(ctx, notifications.EventTestNotification, nil); err != nil {
		return false, "failed to send notification", err
	}
	return true, "test notification sent", nil
}

// AddFile enqueues a manual file for processing.
func (d *Daemon) AddFile(ctx context.Context, sourcePath string) (*queue.Item, error) {
	if d.store == nil {
		return nil, errors.New("queue store unavailable")
	}
	trimmed := strings.TrimSpace(sourcePath)
	if trimmed == "" {
		return nil, errors.New("source path is required")
	}
	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return nil, fmt.Errorf("resolve source path: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat source file: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("source path %q is a directory", absPath)
	}
	ext := strings.ToLower(filepath.Ext(info.Name()))
	if _, ok := manualFileExtensions[ext]; !ok {
		return nil, fmt.Errorf("unsupported file extension %q", ext)
	}
	item, err := d.store.NewFile(ctx, absPath)
	if err != nil {
		return nil, fmt.Errorf("enqueue manual file: %w", err)
	}
	d.logger.Info("manual file queued", zap.Int64(logging.FieldItemID, item.ID), zap.String("source", absPath))
	return item, nil
}

// LogPath returns the path to the daemon log file.
func (d *Daemon) LogPath() string {
	return d.logPath
}

// Status returns the current daemon status.
func (d *Daemon) Status(ctx context.Context) Status {
	summary := d.workflow.Status(ctx)

	d.depsMu.RLock()
	dependencies := make([]DependencyStatus, len(d.dependencies))
	copy(dependencies, d.dependencies)
	d.depsMu.RUnlock()
	return Status{
		Running:      d.running.Load(),
		Workflow:     summary,
		QueueDBPath:  filepath.Join(d.cfg.LogDir, "queue.db"),
		LockFilePath: d.lockPath,
		Dependencies: dependencies,
		PID:          os.Getpid(),
	}
}

func (d *Daemon) runDependencyChecks(ctx context.Context) {
	requirements := []deps.Requirement{
		{
			Name:        "MakeMKV",
			Command:     d.cfg.MakemkvBinary(),
			Description: "Required for disc ripping",
		},
		{
			Name:        "Drapto",
			Command:     d.cfg.DraptoBinary(),
			Description: "Required for encoding",
		},
	}

	results := deps.CheckBinaries(requirements)
	results = append(results, deps.CheckFFmpegForDrapto(d.cfg.DraptoBinary()))
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
		fields := []zap.Field{
			zap.String("dependency", status.Name),
			zap.String("command", status.Command),
		}
		if status.Detail != "" {
			fields = append(fields, zap.String("detail", status.Detail))
		}
		if status.Optional {
			fields = append(fields, zap.Bool("optional", true))
			d.logger.Warn("optional dependency unavailable", fields...)
		} else {
			d.logger.Error("required dependency unavailable", fields...)
			if d.notifier != nil {
				_ = d.notifier.Publish(ctx, notifications.EventError, notifications.Payload{
					"context": fmt.Sprintf("dependency %s", status.Name),
					"error":   status.Detail,
				})
			}
		}
	}
}
