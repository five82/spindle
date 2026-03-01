package ripping

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripcache"
	"spindle/internal/services/makemkv"
	"spindle/internal/stage"
)

// cacheDecision tracks the rip cache lookup result.
type cacheDecision string

const (
	cacheHit        cacheDecision = "hit"
	cacheMiss       cacheDecision = "miss"
	cacheInvalid    cacheDecision = "invalid"
	cacheError      cacheDecision = "error"
	cacheIncomplete cacheDecision = "incomplete"
)

// Ripper manages the MakeMKV ripping workflow.
type Ripper struct {
	store    *queue.Store
	cfg      *config.Config
	logger   *slog.Logger
	client   makemkv.Ripper
	notifier notifications.Service
	cache    *ripcache.Manager
}

// NewRipper constructs the ripping handler using default dependencies.
func NewRipper(cfg *config.Config, store *queue.Store, logger *slog.Logger, notifier notifications.Service) *Ripper {
	componentLogger := logging.NewComponentLogger(logger, "makemkv")
	client, err := makemkv.New(cfg.MakemkvBinary(), cfg.MakeMKV.RipTimeout,
		makemkv.WithLogger(componentLogger),
		makemkv.WithMinLength(cfg.MakeMKV.MinTitleLength),
	)
	if err != nil {
		logger.Warn("makemkv client unavailable; ripping disabled",
			logging.Error(err),
			logging.String(logging.FieldEventType, "makemkv_unavailable"),
			logging.String(logging.FieldErrorHint, "check makemkv_binary and license configuration"),
			logging.String(logging.FieldImpact, "disc ripping will not be available"),
		)
	}
	return NewRipperWithDependencies(cfg, store, logger, client, notifier)
}

// NewRipperWithDependencies allows injecting all collaborators (used in tests).
func NewRipperWithDependencies(cfg *config.Config, store *queue.Store, logger *slog.Logger, client makemkv.Ripper, notifier notifications.Service) *Ripper {
	rip := &Ripper{
		store:    store,
		cfg:      cfg,
		client:   client,
		notifier: notifier,
		cache:    ripcache.NewManager(cfg, logger),
	}
	rip.SetLogger(logger)
	return rip
}

// SetLogger updates the ripper's logging destination while preserving component labeling.
func (r *Ripper) SetLogger(logger *slog.Logger) {
	r.logger = logging.NewComponentLogger(logger, "ripper")
	if r.cache != nil {
		r.cache.SetLogger(logger)
	}
}

func (r *Ripper) Prepare(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, r.logger)
	item.InitProgress("Ripping", "Starting rip")
	logger.Debug("starting rip preparation")
	if r.notifier != nil {
		if err := r.notifier.Publish(ctx, notifications.EventRipStarted, notifications.Payload{"discTitle": item.DiscTitle}); err != nil {
			logger.Debug("failed to send rip start notification", logging.Error(err))
		}
	}
	return nil
}

// HealthCheck verifies MakeMKV ripping dependencies.
func (r *Ripper) HealthCheck(ctx context.Context) stage.Health {
	const name = "ripper"
	if r.cfg == nil {
		return stage.Unhealthy(name, "configuration unavailable")
	}
	if strings.TrimSpace(r.cfg.Paths.StagingDir) == "" {
		return stage.Unhealthy(name, "staging directory not configured")
	}
	if strings.TrimSpace(r.cfg.MakeMKV.OpticalDrive) == "" {
		return stage.Unhealthy(name, "optical drive not configured")
	}
	if r.client == nil {
		return stage.Unhealthy(name, "makemkv client unavailable")
	}
	binary := strings.TrimSpace(r.cfg.MakemkvBinary())
	if binary == "" {
		return stage.Unhealthy(name, "makemkv binary not configured")
	}
	if _, err := exec.LookPath(binary); err != nil {
		return stage.Unhealthy(name, fmt.Sprintf("makemkv binary %q not found", binary))
	}
	return stage.Healthy(name)
}
