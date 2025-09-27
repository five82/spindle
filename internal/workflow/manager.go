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
)

// Stage defines the contract for workflow steps handled by the manager.
type Stage interface {
	Name() string
	TriggerStatus() queue.Status
	ProcessingStatus() queue.Status
	NextStatus() queue.Status
	Prepare(ctx context.Context, item *queue.Item) error
	Execute(ctx context.Context, item *queue.Item) error
	Rollback(ctx context.Context, item *queue.Item, stageErr error) error
	HealthCheck(ctx context.Context) StageHealth
}

type workItem struct {
	stage     Stage
	item      *queue.Item
	requestID string
}

// Manager coordinates queue processing using registered stages.
type Manager struct {
	cfg               *config.Config
	store             *queue.Store
	logger            *zap.Logger
	pollInterval      time.Duration
	workerCount       int
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	notifier          notifications.Service

	statusOrder []queue.Status
	stages      map[queue.Status]Stage

	jobCh chan workItem

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
		workerCount:       cfg.WorkflowWorkerCount,
		heartbeatInterval: time.Duration(cfg.WorkflowHeartbeatInterval) * time.Second,
		heartbeatTimeout:  time.Duration(cfg.WorkflowHeartbeatTimeout) * time.Second,
		statusOrder: []queue.Status{
			queue.StatusPending,
			queue.StatusIdentified,
			queue.StatusRipped,
			queue.StatusEncoded,
		},
		stages: make(map[queue.Status]Stage),
	}
}

// Register registers a stage for a given trigger status.
func (m *Manager) Register(stage Stage) {
	if stage == nil {
		return
	}
	status := stage.TriggerStatus()
	m.stages[status] = stage
}

// Start begins background processing.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return errors.New("workflow already running")
	}

	if m.workerCount <= 0 {
		return errors.New("invalid worker count configured for workflow manager")
	}

	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.running = true
	m.jobCh = make(chan workItem, m.workerCount)

	// Worker goroutines
	for i := 0; i < m.workerCount; i++ {
		m.wg.Add(1)
		go m.workerLoop(runCtx)
	}

	// Dispatcher goroutine
	m.wg.Add(1)
	go m.dispatchLoop(runCtx)

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

func (m *Manager) dispatchLoop(ctx context.Context) {
	defer m.wg.Done()
	defer close(m.jobCh)

	logger := m.logger.With(zap.String("component", "workflow-dispatch"))

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Reclaim stale processing items based on heartbeat timeout.
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

		stage := m.stages[item.Status]
		if stage == nil {
			logger.Warn("no stage registered for status", zap.String("status", string(item.Status)))
			select {
			case <-ctx.Done():
				return
			case <-time.After(m.pollInterval):
			}
			continue
		}

		requestID := uuid.NewString()
		stageCtx := withStageContext(ctx, stage, item, requestID)
		stageLogger := logging.WithContext(stageCtx, logger)

		if err := m.transitionToProcessing(stageCtx, stage, item); err != nil {
			stageLogger.Error("failed to transition item to processing", zap.Error(err))
			m.setLastError(err)
			continue
		}

		work := workItem{stage: stage, item: item, requestID: requestID}
		select {
		case <-ctx.Done():
			return
		case m.jobCh <- work:
		}
	}
}

func (m *Manager) workerLoop(ctx context.Context) {
	defer m.wg.Done()
	baseLogger := m.logger.With(zap.String("component", "workflow-worker"))

	for {
		select {
		case <-ctx.Done():
			return
		case work, ok := <-m.jobCh:
			if !ok {
				return
			}
			jobCtx := withStageContext(ctx, work.stage, work.item, work.requestID)
			logger := logging.WithContext(jobCtx, baseLogger)

			if err := m.processJob(jobCtx, work); err != nil {
				logger.Error("stage execution failed", zap.Error(err))
				m.setLastError(err)
			}
		}
	}
}

func (m *Manager) processJob(ctx context.Context, work workItem) error {
	stage := work.stage
	item := work.item

	ctx = withStageContext(ctx, stage, item, work.requestID)
	logger := logging.WithContext(ctx, m.logger.With(zap.String("component", "workflow-manager")))

	if err := stage.Prepare(ctx, item); err != nil {
		m.handleStageFailure(ctx, stage, item, err)
		return err
	}
	if err := m.store.Update(ctx, item); err != nil {
		return fmt.Errorf("persist stage preparation: %w", err)
	}

	hbCtx, hbCancel := context.WithCancel(ctx)
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go m.heartbeatLoop(hbCtx, &hbWG, item.ID)

	execErr := stage.Execute(ctx, item)
	hbCancel()
	hbWG.Wait()

	if execErr != nil {
		if rbErr := stage.Rollback(ctx, item, execErr); rbErr != nil {
			logger.Warn("stage rollback failed", zap.Error(rbErr))
		}
		m.handleStageFailure(ctx, stage, item, execErr)
		return execErr
	}

	finalStatus := stage.NextStatus()
	if item.Status == stage.ProcessingStatus() || item.Status == "" {
		item.Status = finalStatus
	}
	item.LastHeartbeat = nil
	if err := m.store.Update(ctx, item); err != nil {
		return fmt.Errorf("persist stage result: %w", err)
	}
	m.setLastItem(item)
	m.checkQueueCompletion(ctx)
	return nil
}

func (m *Manager) transitionToProcessing(ctx context.Context, stage Stage, item *queue.Item) error {
	ctx = withStageContext(ctx, stage, item, "")
	now := time.Now().UTC()
	processingStatus := stage.ProcessingStatus()
	if processingStatus == "" {
		return errors.New("processing status must not be empty")
	}

	item.Status = processingStatus
	if item.ProgressStage == "" {
		item.ProgressStage = deriveStageLabel(processingStatus)
	}
	if item.ProgressMessage == "" {
		item.ProgressMessage = fmt.Sprintf("%s started", stage.Name())
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

func (m *Manager) handleStageFailure(ctx context.Context, stage Stage, item *queue.Item, stageErr error) {
	logger := logging.WithContext(ctx, m.logger.With(zap.String("component", "workflow-manager")))
	status, errorMessage, progressMessage := classifyStageFailure(stage, stageErr)
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
	m.notifyStageError(ctx, stage, item, stageErr)
	m.checkQueueCompletion(ctx)
}

func classifyStageFailure(stage Stage, stageErr error) (queue.Status, string, string) {
	var se *services.ServiceError
	if stageErr == nil {
		msg := "stage failed without error detail"
		if stage != nil {
			msg = fmt.Sprintf("%s failed without error detail", stage.Name())
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

func withStageContext(ctx context.Context, stage Stage, item *queue.Item, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if item != nil {
		ctx = services.WithItemID(ctx, item.ID)
	}
	if stage != nil {
		ctx = services.WithStage(ctx, stage.Name())
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
	StageHealth map[string]StageHealth
}

// Status returns the latest workflow information.
func (m *Manager) Status(ctx context.Context) StatusSummary {
	m.mu.RLock()
	running := m.running
	lastErr := m.lastErr
	lastItem := m.lastItem
	stages := make([]Stage, 0, len(m.stages))
	for _, stage := range m.stages {
		if stage != nil {
			stages = append(stages, stage)
		}
	}
	m.mu.RUnlock()

	stats, err := m.store.Stats(ctx)
	if err != nil {
		m.logger.Warn("failed to read queue stats", zap.Error(err))
	}

	health := make(map[string]StageHealth, len(stages))
	for _, stage := range stages {
		// Run health checks in the provided context so logging retains caller metadata.
		health[stage.Name()] = stage.HealthCheck(ctx)
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

func (m *Manager) notifyStageError(ctx context.Context, stage Stage, item *queue.Item, stageErr error) {
	if m.notifier == nil || stageErr == nil {
		return
	}
	logger := logging.WithContext(ctx, m.logger.With(zap.String("component", "workflow-manager")))
	contextLabel := fmt.Sprintf("%s (item #%d)", stage.Name(), item.ID)
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
