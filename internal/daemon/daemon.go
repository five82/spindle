// Package daemon manages the Spindle daemon lifecycle: lock file acquisition,
// startup recovery, HTTP API listeners, workflow execution, and graceful shutdown.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/workflow"
)

// Daemon is the main Spindle daemon process.
type Daemon struct {
	cfg         *config.Config
	store       *queue.Store
	manager     *workflow.Manager
	api         *httpapi.Server
	discMonitor *discmonitor.Monitor
	lock        *flock.Flock
	logger      *slog.Logger
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	shutdownCh  chan struct{}
}

// New creates a new daemon instance. discMon may be nil if no optical drive is configured.
func New(cfg *config.Config, store *queue.Store, manager *workflow.Manager, api *httpapi.Server, discMon *discmonitor.Monitor, logger *slog.Logger) *Daemon {
	return &Daemon{
		cfg:         cfg,
		store:       store,
		manager:     manager,
		api:         api,
		discMonitor: discMon,
		logger:      logger,
		shutdownCh:  make(chan struct{}),
	}
}

// DiscMonitor returns the disc monitor, or nil if none is configured.
func (d *Daemon) DiscMonitor() *discmonitor.Monitor { return d.discMonitor }

// ShutdownCh returns a channel that is closed when the daemon should stop.
func (d *Daemon) ShutdownCh() chan struct{} { return d.shutdownCh }

// Start starts the daemon with lock file protection.
func (d *Daemon) Start(ctx context.Context) error {
	// Acquire lock file.
	lockPath := d.cfg.LockPath()
	d.lock = flock.New(lockPath)
	locked, err := d.lock.TryLock()
	if err != nil {
		return fmt.Errorf("lock file: %w", err)
	}
	if !locked {
		return fmt.Errorf("another daemon instance is running (lock: %s)", lockPath)
	}

	// Startup recovery: reset any stale in-progress items.
	if err := d.store.ResetInProgress(); err != nil {
		d.logger.Error("startup recovery failed", "error", err)
	}

	// Start HTTP API.
	socketPath := d.cfg.SocketPath()
	if err := d.api.ListenUnix(socketPath); err != nil {
		return fmt.Errorf("start unix socket: %w", err)
	}
	d.logger.Info("HTTP API listening", "socket", socketPath)

	if d.cfg.API.Bind != "" {
		if err := d.api.ListenTCP(d.cfg.API.Bind); err != nil {
			return fmt.Errorf("start tcp: %w", err)
		}
		d.logger.Info("HTTP API listening", "addr", d.cfg.API.Bind)
	}

	// Start workflow manager.
	ctx, d.cancel = context.WithCancel(ctx)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.manager.Run(ctx)
	}()

	d.logger.Info("daemon started")
	return nil
}

// Stop gracefully stops the daemon.
func (d *Daemon) Stop() {
	d.logger.Info("daemon stopping")

	// Cancel workflow context.
	if d.cancel != nil {
		d.cancel()
	}

	// Wait for workflow to finish.
	d.wg.Wait()

	// Shutdown recovery: clear in-progress flags.
	if err := d.store.ResetInProgressOnShutdown(); err != nil {
		d.logger.Error("shutdown recovery failed", "error", err)
	}

	// Shutdown HTTP API.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := d.api.Shutdown(shutdownCtx); err != nil {
		d.logger.Error("api shutdown failed", "error", err)
	}

	// Clean up socket.
	_ = os.Remove(d.cfg.SocketPath())

	d.logger.Info("daemon stopped")
}

// Close releases all resources.
func (d *Daemon) Close() error {
	if d.lock != nil {
		return d.lock.Unlock()
	}
	return nil
}
