package workflow

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
)

// Manager coordinates queue processing using registered stage functions.
type Manager struct {
	cfg          *config.Config
	store        *queue.Store
	logger       *slog.Logger
	pollInterval time.Duration
	notifier     notifications.Service

	heartbeat *HeartbeatMonitor
	bgLogger  *BackgroundLogger

	lanes     map[laneKind]*laneState
	laneOrder []laneKind

	mu       sync.RWMutex
	running  bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	lastErr  error
	lastItem *queue.Item

	queueActive bool
	queueStart  time.Time
}

// NewManager constructs a new workflow manager.
func NewManager(cfg *config.Config, store *queue.Store, logger *slog.Logger) *Manager {
	return NewManagerWithOptions(cfg, store, logger, notifications.NewService(cfg), nil)
}

// NewManagerWithNotifier constructs a workflow manager with a custom notifier (used in tests).
func NewManagerWithNotifier(cfg *config.Config, store *queue.Store, logger *slog.Logger, notifier notifications.Service) *Manager {
	return NewManagerWithOptions(cfg, store, logger, notifier, nil)
}

// NewManagerWithOptions constructs a workflow manager with full configuration.
func NewManagerWithOptions(cfg *config.Config, store *queue.Store, logger *slog.Logger, notifier notifications.Service, logHub *logging.StreamHub) *Manager {
	return &Manager{
		cfg:          cfg,
		store:        store,
		logger:       logger,
		notifier:     notifier,
		pollInterval: time.Duration(cfg.Workflow.QueuePollInterval) * time.Second,
		heartbeat: NewHeartbeatMonitor(
			store,
			logger,
			time.Duration(cfg.Workflow.HeartbeatInterval)*time.Second,
			time.Duration(cfg.Workflow.HeartbeatTimeout)*time.Second,
		),
		bgLogger: NewBackgroundLogger(cfg, logHub),
		lanes:    make(map[laneKind]*laneState),
	}
}
