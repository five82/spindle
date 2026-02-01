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

// RipHooks provides callbacks around the ripping stage to coordinate
// external resources like disc monitoring.
type RipHooks interface {
	// BeforeRip is called before MakeMKV starts reading the disc.
	// Use this to pause disc monitoring and avoid concurrent device access.
	BeforeRip()
	// AfterRip is called after ripping completes (success or failure).
	// Use this to resume disc monitoring.
	AfterRip()
}

// Manager coordinates queue processing using registered stage functions.
type Manager struct {
	cfg          *config.Config
	store        *queue.Store
	logger       *slog.Logger
	pollInterval time.Duration
	notifier     notifications.Service
	ripHooks     RipHooks

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

// ManagerOption configures optional Manager behavior.
type ManagerOption func(*managerOptions)

type managerOptions struct {
	diagnosticMode    bool
	diagnosticItemDir string
	sessionID         string
}

// WithDiagnosticMode enables diagnostic logging with separate DEBUG logs.
func WithDiagnosticMode(enabled bool, itemDir, sessionID string) ManagerOption {
	return func(o *managerOptions) {
		o.diagnosticMode = enabled
		o.diagnosticItemDir = itemDir
		o.sessionID = sessionID
	}
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
func NewManagerWithOptions(cfg *config.Config, store *queue.Store, logger *slog.Logger, notifier notifications.Service, logHub *logging.StreamHub, opts ...ManagerOption) *Manager {
	options := &managerOptions{}
	for _, opt := range opts {
		opt(options)
	}
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
		bgLogger: NewBackgroundLogger(cfg, logHub, options.diagnosticMode, options.diagnosticItemDir, options.sessionID),
		lanes:    make(map[laneKind]*laneState),
	}
}

// SetRipHooks registers callbacks for disc coordination during ripping.
// The daemon uses this to pause disc monitoring while MakeMKV reads.
func (m *Manager) SetRipHooks(hooks RipHooks) {
	m.ripHooks = hooks
}
