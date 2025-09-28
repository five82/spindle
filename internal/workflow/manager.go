package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/services"
	"spindle/internal/stage"
)

// StageHandler describes the narrow contract the manager needs from each stage.
type StageHandler interface {
	Prepare(context.Context, *queue.Item) error
	Execute(context.Context, *queue.Item) error
	HealthCheck(context.Context) stage.Health
}

// StageSet bundles the concrete workflow handlers the manager orchestrates.
type StageSet struct {
	Identifier StageHandler
	Ripper     StageHandler
	Encoder    StageHandler
	Organizer  StageHandler
}

type stageDefinition struct {
	name       string
	trigger    queue.Status
	processing queue.Status
	next       queue.Status
	prepare    func(context.Context, *queue.Item) error
	execute    func(context.Context, *queue.Item) error
	health     func(context.Context) stage.Health
}

// Manager coordinates queue processing using registered stage functions.
type Manager struct {
	cfg               *config.Config
	store             *queue.Store
	logger            *zap.Logger
	pollInterval      time.Duration
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	notifier          notifications.Service

	statusOrder     []queue.Status
	stageDefs       []*stageDefinition
	stagesByTrigger map[queue.Status]*stageDefinition

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
func NewManager(cfg *config.Config, store *queue.Store, logger *zap.Logger) *Manager {
	return NewManagerWithNotifier(cfg, store, logger, notifications.NewService(cfg))
}

// NewManagerWithNotifier constructs a workflow manager with a custom notifier (used in tests).
func NewManagerWithNotifier(cfg *config.Config, store *queue.Store, logger *zap.Logger, notifier notifications.Service) *Manager {
	return &Manager{
		cfg:               cfg,
		store:             store,
		logger:            logger,
		notifier:          notifier,
		pollInterval:      time.Duration(cfg.QueuePollInterval) * time.Second,
		heartbeatInterval: time.Duration(cfg.WorkflowHeartbeatInterval) * time.Second,
		heartbeatTimeout:  time.Duration(cfg.WorkflowHeartbeatTimeout) * time.Second,
		statusOrder: []queue.Status{
			queue.StatusPending,
			queue.StatusIdentified,
			queue.StatusRipped,
			queue.StatusEncoded,
		},
		stagesByTrigger: make(map[queue.Status]*stageDefinition),
	}
}

// ConfigureStages registers the concrete stage handlers the workflow will run.

func (m *Manager) ConfigureStages(set StageSet) {
	defs := make([]*stageDefinition, 0, 4)

	if set.Identifier != nil {
		defs = append(defs, &stageDefinition{
			name:       "identifier",
			trigger:    queue.StatusPending,
			processing: queue.StatusIdentifying,
			next:       queue.StatusIdentified,
			prepare:    set.Identifier.Prepare,
			execute:    set.Identifier.Execute,
			health:     set.Identifier.HealthCheck,
		})
	}
	if set.Ripper != nil {
		defs = append(defs, &stageDefinition{
			name:       "ripper",
			trigger:    queue.StatusIdentified,
			processing: queue.StatusRipping,
			next:       queue.StatusRipped,
			prepare:    set.Ripper.Prepare,
			execute:    set.Ripper.Execute,
			health:     set.Ripper.HealthCheck,
		})
	}
	if set.Encoder != nil {
		defs = append(defs, &stageDefinition{
			name:       "encoder",
			trigger:    queue.StatusRipped,
			processing: queue.StatusEncoding,
			next:       queue.StatusEncoded,
			prepare:    set.Encoder.Prepare,
			execute:    set.Encoder.Execute,
			health:     set.Encoder.HealthCheck,
		})
	}
	if set.Organizer != nil {
		defs = append(defs, &stageDefinition{
			name:       "organizer",
			trigger:    queue.StatusEncoded,
			processing: queue.StatusOrganizing,
			next:       queue.StatusCompleted,
			prepare:    set.Organizer.Prepare,
			execute:    set.Organizer.Execute,
			health:     set.Organizer.HealthCheck,
		})
	}

	m.mu.Lock()
	m.stageDefs = defs
	m.stagesByTrigger = make(map[queue.Status]*stageDefinition, len(defs))
	for _, def := range defs {
		m.stagesByTrigger[def.trigger] = def
	}
	m.mu.Unlock()
}

// Start begins background processing.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return errors.New("workflow already running")
	}
	if len(m.stageDefs) == 0 {
		return errors.New("workflow stages not configured")
	}

	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.running = true

	m.wg.Add(1)
	go m.run(runCtx)

	return nil
}

// Stop terminates background processing and waits for completion.
func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	cancel := m.cancel
	m.running = false
	m.cancel = nil
	m.mu.Unlock()

	cancel()
	m.wg.Wait()
}

func (m *Manager) run(ctx context.Context) {
	defer m.wg.Done()
	logger := m.logger.With(zap.String("component", "workflow-runner"))

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if m.heartbeatTimeout > 0 {
			cutoff := time.Now().Add(-m.heartbeatTimeout)
			if reclaimed, err := m.store.ReclaimStaleProcessing(ctx, cutoff); err != nil {
				logger.Warn("reclaim stale processing failed", zap.Error(err))
			} else if reclaimed > 0 {
				logger.Info("reclaimed stale items", zap.Int64("count", reclaimed))
			}
		}

		item, err := m.nextItem(ctx)
		if err != nil {
			m.setLastError(err)
			logger.Error("failed to fetch next queue item", zap.Error(err))
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(m.cfg.ErrorRetryInterval) * time.Second):
			}
			continue
		}
		if item == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(m.pollInterval):
			}
			continue
		}

		def := m.definitionFor(item.Status)
		if def == nil {
			logger.Warn("no stage configured for status", zap.String("status", string(item.Status)))
			select {
			case <-ctx.Done():
				return
			case <-time.After(m.pollInterval):
			}
			continue
		}

		requestID := uuid.NewString()
		stageCtx := withStageContext(ctx, def.name, item, requestID)
		stageLogger := logging.WithContext(stageCtx, logger)

		if err := m.transitionToProcessing(stageCtx, def.processing, def.name, item); err != nil {
			stageLogger.Error("failed to transition item to processing", zap.Error(err))
			m.setLastError(err)
			continue
		}

		if def.prepare != nil {
			if err := def.prepare(stageCtx, item); err != nil {
				m.handleStageFailure(stageCtx, def.name, item, err)
				m.setLastError(err)
				continue
			}
			if err := m.store.Update(stageCtx, item); err != nil {
				wrapped := fmt.Errorf("persist stage preparation: %w", err)
				stageLogger.Error("failed to persist stage preparation", zap.Error(wrapped))
				m.setLastError(wrapped)
				continue
			}
		}

		hbCtx, hbCancel := context.WithCancel(stageCtx)
		var hbWG sync.WaitGroup
		hbWG.Add(1)
		go m.heartbeatLoop(hbCtx, &hbWG, item.ID)

		execErr := def.execute(stageCtx, item)
		hbCancel()
		hbWG.Wait()

		if execErr != nil {
			m.handleStageFailure(stageCtx, def.name, item, execErr)
			m.setLastError(execErr)
			continue
		}

		if item.Status == def.processing || item.Status == "" {
			item.Status = def.next
		}
		item.LastHeartbeat = nil
		if err := m.store.Update(stageCtx, item); err != nil {
			wrapped := fmt.Errorf("persist stage result: %w", err)
			stageLogger.Error("failed to persist stage result", zap.Error(wrapped))
			m.setLastError(wrapped)
			continue
		}
		m.setLastItem(item)
		m.checkQueueCompletion(stageCtx)
	}
}

func (m *Manager) definitionFor(status queue.Status) *stageDefinition {
	m.mu.RLock()
	def := m.stagesByTrigger[status]
	m.mu.RUnlock()
	return def
}

func (m *Manager) transitionToProcessing(ctx context.Context, processing queue.Status, stageName string, item *queue.Item) error {
	now := time.Now().UTC()
	if processing == "" {
		return errors.New("processing status must not be empty")
	}

	item.Status = processing
	if item.ProgressStage == "" {
		item.ProgressStage = deriveStageLabel(processing)
	}
	if item.ProgressMessage == "" {
		item.ProgressMessage = fmt.Sprintf("%s started", deriveStageLabel(processing))
	}
	item.ProgressPercent = 0
	item.ErrorMessage = ""
	item.LastHeartbeat = &now

	if err := m.store.Update(ctx, item); err != nil {
		return fmt.Errorf("persist processing transition: %w", err)
	}
	m.setLastItem(item)
	m.onItemStarted(ctx)
	return nil
}

func (m *Manager) heartbeatLoop(ctx context.Context, wg *sync.WaitGroup, itemID int64) {
	defer wg.Done()
	ticker := time.NewTicker(m.heartbeatInterval)
	defer ticker.Stop()

	logger := logging.WithContext(ctx, m.logger.With(zap.String("component", "workflow-heartbeat")))

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.store.UpdateHeartbeat(ctx, itemID); err != nil {
				logger.Warn("heartbeat update failed", zap.Error(err))
			}
		}
	}
}

func (m *Manager) handleStageFailure(ctx context.Context, stageName string, item *queue.Item, stageErr error) {
	logger := logging.WithContext(ctx, m.logger.With(zap.String("component", "workflow-manager")))
	status, errorMessage, progressMessage := classifyStageFailure(stageName, stageErr)
	item.Status = status
	if strings.TrimSpace(errorMessage) == "" {
		errorMessage = "workflow stage failed"
	}
	item.ErrorMessage = errorMessage
	if status == queue.StatusReview {
		item.ProgressStage = "Needs review"
	} else {
		item.ProgressStage = "Failed"
	}
	if strings.TrimSpace(progressMessage) == "" {
		progressMessage = errorMessage
	}
	item.ProgressMessage = progressMessage
	item.ProgressPercent = 0
	item.LastHeartbeat = nil
	if err := m.store.Update(ctx, item); err != nil {
		logger.Error("failed to persist stage failure", zap.Error(err))
	}
	m.setLastItem(item)
	m.notifyStageError(ctx, stageName, item, stageErr)
	m.checkQueueCompletion(ctx)
}

func classifyStageFailure(stageName string, stageErr error) (queue.Status, string, string) {
	var se *services.ServiceError
	if stageErr == nil {
		msg := "stage failed without error detail"
		if stageName != "" {
			msg = fmt.Sprintf("%s failed without error detail", stageName)
		}
		return queue.StatusFailed, msg, msg
	}
	if errors.As(stageErr, &se) {
		status := se.FailureStatus()
		message := se.Error()
		progress := message
		if hint := strings.TrimSpace(se.Hint); hint != "" {
			progress = hint
		}
		return status, message, progress
	}
	message := stageErr.Error()
	return queue.StatusFailed, message, message
}

func (m *Manager) nextItem(ctx context.Context) (*queue.Item, error) {
	return m.store.NextForStatuses(ctx, m.statusOrder...)
}

func withStageContext(ctx context.Context, stageName string, item *queue.Item, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if item != nil {
		ctx = services.WithItemID(ctx, item.ID)
	}
	if stageName != "" {
		ctx = services.WithStage(ctx, stageName)
	}
	if requestID != "" {
		ctx = services.WithRequestID(ctx, requestID)
	}
	return ctx
}

func deriveStageLabel(status queue.Status) string {
	if status == "" {
		return ""
	}
	parts := strings.Fields(strings.ReplaceAll(string(status), "_", " "))
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(strings.ToLower(part))
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

// StatusSummary represents lightweight workflow diagnostics.
type StatusSummary struct {
	Running     bool
	LastError   string
	LastItem    *queue.Item
	QueueStats  map[queue.Status]int
	StageHealth map[string]stage.Health
}

// Status returns the latest workflow information.
func (m *Manager) Status(ctx context.Context) StatusSummary {
	m.mu.RLock()
	running := m.running
	lastErr := m.lastErr
	lastItem := m.lastItem
	defs := make([]*stageDefinition, len(m.stageDefs))
	copy(defs, m.stageDefs)
	m.mu.RUnlock()

	stats, err := m.store.Stats(ctx)
	if err != nil {
		m.logger.Warn("failed to read queue stats", zap.Error(err))
	}

	health := make(map[string]stage.Health, len(defs))
	for _, def := range defs {
		if def == nil || def.health == nil {
			continue
		}
		health[def.name] = def.health(ctx)
	}

	summary := StatusSummary{Running: running, QueueStats: stats, StageHealth: health}
	if lastErr != nil {
		summary.LastError = lastErr.Error()
	}
	if lastItem != nil {
		copy := *lastItem
		summary.LastItem = &copy
	}
	return summary
}

func (m *Manager) setLastError(err error) {
	m.mu.Lock()
	m.lastErr = err
	m.mu.Unlock()
}

func (m *Manager) setLastItem(item *queue.Item) {
	m.mu.Lock()
	if item != nil {
		copy := *item
		m.lastItem = &copy
	} else {
		m.lastItem = nil
	}
	m.mu.Unlock()
}

func (m *Manager) notifyStageError(ctx context.Context, stageName string, item *queue.Item, stageErr error) {
	if m.notifier == nil || stageErr == nil {
		return
	}
	logger := logging.WithContext(ctx, m.logger.With(zap.String("component", "workflow-manager")))
	contextLabel := fmt.Sprintf("%s (item #%d)", stageName, item.ID)
	if err := m.notifier.NotifyError(ctx, stageErr, contextLabel); err != nil {
		logger.Warn("stage error notification failed", zap.Error(err))
	}
}

func (m *Manager) onItemStarted(ctx context.Context) {
	if m.notifier == nil {
		return
	}
	stats, err := m.store.Stats(ctx)
	if err != nil {
		m.logger.Warn("queue stats unavailable for start notification", zap.Error(err))
		return
	}
	m.mu.Lock()
	if m.queueActive {
		m.mu.Unlock()
		return
	}
	m.queueActive = true
	m.queueStart = time.Now()
	m.mu.Unlock()

	count := countWorkItems(stats)
	if err := m.notifier.NotifyQueueStarted(ctx, count); err != nil {
		m.logger.Warn("queue start notification failed", zap.Error(err))
	}
}

func (m *Manager) checkQueueCompletion(ctx context.Context) {
	if m.notifier == nil {
		return
	}
	stats, err := m.store.Stats(ctx)
	if err != nil {
		m.logger.Warn("queue stats unavailable for completion notification", zap.Error(err))
		return
	}
	if active := countActiveItems(stats); active > 0 {
		return
	}

	m.mu.Lock()
	if !m.queueActive {
		m.mu.Unlock()
		return
	}
	start := m.queueStart
	m.queueActive = false
	m.queueStart = time.Time{}
	m.mu.Unlock()

	duration := time.Duration(0)
	if !start.IsZero() {
		duration = time.Since(start)
	}
	processed := stats[queue.StatusCompleted]
	failed := stats[queue.StatusFailed]
	if err := m.notifier.NotifyQueueCompleted(ctx, processed, failed, duration); err != nil {
		m.logger.Warn("queue completion notification failed", zap.Error(err))
	}
}

func countWorkItems(stats map[queue.Status]int) int {
	total := 0
	for status, count := range stats {
		if status == queue.StatusCompleted || status == queue.StatusFailed {
			continue
		}
		total += count
	}
	return total
}

func countActiveItems(stats map[queue.Status]int) int {
	activeStatuses := []queue.Status{
		queue.StatusPending,
		queue.StatusIdentifying,
		queue.StatusIdentified,
		queue.StatusRipping,
		queue.StatusRipped,
		queue.StatusEncoding,
		queue.StatusEncoded,
		queue.StatusOrganizing,
	}
	total := 0
	for _, status := range activeStatuses {
		total += stats[status]
	}
	return total
}
