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

	"github.com/gofrs/flock"

	"spindle/internal/config"
	"spindle/internal/deps"
	"spindle/internal/logging"
	"spindle/internal/notifications"
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
	apiSrv     *apiServer

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
func New(cfg *config.Config, store *queue.Store, logger *slog.Logger, wf *workflow.Manager, logPath string, hub *logging.StreamHub, archive *logging.EventArchive) (*Daemon, error) {
	if cfg == nil || store == nil || logger == nil || wf == nil {
		return nil, errors.New("daemon requires config, store, logger, and workflow manager")
	}
	if strings.TrimSpace(logPath) == "" {
		return nil, errors.New("daemon requires log path")
	}

	lockPath := filepath.Join(cfg.Paths.LogDir, "spindle.lock")
	monitor := newDiscMonitor(cfg, store, logger)
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
		monitor:    monitor,
		notifier:   notifications.NewService(cfg),
	}
	apiSrv, err := newAPIServer(cfg, daemon, logger)
	if err != nil {
		return nil, err
	}
	daemon.apiSrv = apiSrv
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
	d.logger.Info("spindle daemon started", logging.String("lock", d.lockPath))
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
	if d.apiSrv != nil {
		d.apiSrv.stop()
	}
	d.workflow.Stop()
	if err := d.lock.Unlock(); err != nil {
		d.logger.Warn("failed to release daemon lock", logging.Error(err))
	}
	d.ctx = nil
	d.running.Store(false)
	d.logger.Info("spindle daemon stopped")
}

// Close releases resources held by the daemon.
func (d *Daemon) Close() error {
	d.Stop()
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
	notifier := notifications.NewService(d.cfg)
	if err := notifier.Publish(ctx, notifications.EventTestNotification, nil); err != nil {
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
	return Status{
		Running:      d.running.Load(),
		Workflow:     summary,
		QueueDBPath:  filepath.Join(d.cfg.Paths.LogDir, "queue.db"),
		LockFilePath: d.lockPath,
		Dependencies: dependencies,
		PID:          os.Getpid(),
	}
}

func (d *Daemon) runDependencyChecks(ctx context.Context) error {
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
		{
			Name:        "FFprobe",
			Command:     deps.ResolveFFprobePath(d.cfg.FFprobeBinary()),
			Description: "Required for media inspection",
		},
		{
			Name:        "MediaInfo",
			Command:     "mediainfo",
			Description: "Required for metadata inspection",
		},
		{
			Name:        "bd_info",
			Command:     "bd_info",
			Description: "Enhances disc metadata when MakeMKV titles are generic",
			Optional:    true,
		},
	}
	if d.cfg.Subtitles.Enabled {
		requirements = append(requirements, deps.Requirement{
			Name:        "uvx",
			Command:     "uvx",
			Description: "Required for WhisperX-driven transcription",
		})
	}
	if d.cfg.CommentaryDetection.Enabled {
		requirements = append(requirements, deps.Requirement{
			Name:        "fpcalc",
			Command:     "fpcalc",
			Description: "Required for commentary fingerprinting",
		}, deps.Requirement{
			Name:        "webrtcvad (cgo)",
			Command:     "cc",
			Description: "Required for commentary speech detection",
		})
	} else {
		requirements = append(requirements, deps.Requirement{
			Name:        "fpcalc",
			Command:     "fpcalc",
			Description: "Required for commentary fingerprinting",
			Optional:    true,
		}, deps.Requirement{
			Name:        "webrtcvad (cgo)",
			Command:     "cc",
			Description: "Required for commentary speech detection",
			Optional:    true,
		})
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
		fields := []logging.Attr{
			logging.String("dependency", status.Name),
			logging.String("command", status.Command),
		}
		if status.Detail != "" {
			fields = append(fields, logging.String("detail", status.Detail))
		}
		if status.Optional {
			fields = append(fields, logging.Bool("optional", true))
			d.logger.Warn("optional dependency unavailable", logging.Args(fields...)...)
		} else {
			d.logger.Error("required dependency unavailable", logging.Args(fields...)...)
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
