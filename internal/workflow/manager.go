package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"

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
	Identifier        StageHandler
	Ripper            StageHandler
	EpisodeIdentifier StageHandler
	Encoder           StageHandler
	Subtitles         StageHandler
	Organizer         StageHandler
}

type pipelineStage struct {
	name             string
	handler          StageHandler
	startStatus      queue.Status
	processingStatus queue.Status
	doneStatus       queue.Status
}

type loggerAware interface {
	SetLogger(*slog.Logger)
}

type laneKind string

const (
	laneForeground laneKind = "foreground"
	laneBackground laneKind = "background"
)

type laneState struct {
	kind                 laneKind
	name                 string
	stages               []pipelineStage
	statusOrder          []queue.Status
	stageByStart         map[queue.Status]pipelineStage
	processingStatuses   []queue.Status
	logger               *slog.Logger
	notificationsEnabled bool
	runReclaimer         bool
}

func (l *laneState) finalize() {
	if l == nil {
		return
	}
	l.stageByStart = make(map[queue.Status]pipelineStage, len(l.stages))
	l.statusOrder = make([]queue.Status, 0, len(l.stages))
	seenProcessing := make(map[queue.Status]struct{})
	for _, stg := range l.stages {
		l.stageByStart[stg.startStatus] = stg
		l.statusOrder = append(l.statusOrder, stg.startStatus)
		if stg.processingStatus != "" {
			if _, ok := seenProcessing[stg.processingStatus]; !ok {
				l.processingStatuses = append(l.processingStatuses, stg.processingStatus)
				seenProcessing[stg.processingStatus] = struct{}{}
			}
		}
	}
}

func (l *laneState) stageForStatus(status queue.Status) (pipelineStage, bool) {
	if l == nil {
		return pipelineStage{}, false
	}
	stg, ok := l.stageByStart[status]
	return stg, ok
}

// Manager coordinates queue processing using registered stage functions.
type Manager struct {
	cfg               *config.Config
	store             *queue.Store
	logger            *slog.Logger
	pollInterval      time.Duration
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	notifier          notifications.Service
	logHub            *logging.StreamHub

	lanes            map[laneKind]*laneState
	laneOrder        []laneKind
	backgroundLogDir string

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
	backgroundDir := ""
	if cfg != nil && cfg.LogDir != "" {
		backgroundDir = filepath.Join(cfg.LogDir, "background")
	}
	return &Manager{
		cfg:               cfg,
		store:             store,
		logger:            logger,
		notifier:          notifier,
		logHub:            logHub,
		pollInterval:      time.Duration(cfg.QueuePollInterval) * time.Second,
		heartbeatInterval: time.Duration(cfg.WorkflowHeartbeatInterval) * time.Second,
		heartbeatTimeout:  time.Duration(cfg.WorkflowHeartbeatTimeout) * time.Second,
		lanes:             make(map[laneKind]*laneState),
		backgroundLogDir:  backgroundDir,
	}
}

// ConfigureStages registers the concrete stage handlers the workflow will run.

func (m *Manager) ConfigureStages(set StageSet) {
	foreground := &laneState{kind: laneForeground, name: "foreground", notificationsEnabled: true}
	background := &laneState{kind: laneBackground, name: "background", notificationsEnabled: false}

	if set.Identifier != nil {
		foreground.stages = append(foreground.stages, pipelineStage{
			name:             "identifier",
			handler:          set.Identifier,
			startStatus:      queue.StatusPending,
			processingStatus: queue.StatusIdentifying,
			doneStatus:       queue.StatusIdentified,
		})
	}
	if set.Ripper != nil {
		foreground.stages = append(foreground.stages, pipelineStage{
			name:             "ripper",
			handler:          set.Ripper,
			startStatus:      queue.StatusIdentified,
			processingStatus: queue.StatusRipping,
			doneStatus:       queue.StatusRipped,
		})
	}
	encoderStart := queue.StatusRipped
	if set.EpisodeIdentifier != nil {
		background.stages = append(background.stages, pipelineStage{
			name:             "episode-identifier",
			handler:          set.EpisodeIdentifier,
			startStatus:      queue.StatusRipped,
			processingStatus: queue.StatusEpisodeIdentifying,
			doneStatus:       queue.StatusEpisodeIdentified,
		})
		encoderStart = queue.StatusEpisodeIdentified
	}
	organizerStart := queue.StatusEncoded
	if set.Encoder != nil {
		background.stages = append(background.stages, pipelineStage{
			name:             "encoder",
			handler:          set.Encoder,
			startStatus:      encoderStart,
			processingStatus: queue.StatusEncoding,
			doneStatus:       queue.StatusEncoded,
		})
	}
	if set.Subtitles != nil {
		background.stages = append(background.stages, pipelineStage{
			name:             "subtitles",
			handler:          set.Subtitles,
			startStatus:      queue.StatusEncoded,
			processingStatus: queue.StatusSubtitling,
			doneStatus:       queue.StatusSubtitled,
		})
		organizerStart = queue.StatusSubtitled
	}
	if set.Organizer != nil {
		background.stages = append(background.stages, pipelineStage{
			name:             "organizer",
			handler:          set.Organizer,
			startStatus:      organizerStart,
			processingStatus: queue.StatusOrganizing,
			doneStatus:       queue.StatusCompleted,
		})
	}

	lanes := make(map[laneKind]*laneState)
	order := make([]laneKind, 0, 2)

	if len(foreground.stages) > 0 {
		foreground.finalize()
		lanes[foreground.kind] = foreground
		order = append(order, foreground.kind)
	}
	if len(background.stages) > 0 {
		background.finalize()
		lanes[background.kind] = background
		order = append(order, background.kind)
	}

	for _, lane := range lanes {
		if lane == nil {
			continue
		}
		lane.runReclaimer = len(lane.processingStatuses) > 0
	}

	m.mu.Lock()
	m.lanes = lanes
	m.laneOrder = order
	m.mu.Unlock()
}

// Start begins background processing.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return errors.New("workflow already running")
	}
	lanes := make([]*laneState, 0, len(m.laneOrder))
	for _, kind := range m.laneOrder {
		lane := m.lanes[kind]
		if lane == nil || len(lane.statusOrder) == 0 {
			continue
		}
		lanes = append(lanes, lane)
	}
	if len(lanes) == 0 {
		m.mu.Unlock()
		return errors.New("workflow stages not configured")
	}

	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.running = true

	for _, lane := range lanes {
		lane.logger = m.laneLogger(lane)
	}
	m.wg.Add(len(lanes))
	m.mu.Unlock()

	for _, lane := range lanes {
		go m.runLane(runCtx, lane)
	}

	return nil
}

func (m *Manager) laneLogger(lane *laneState) *slog.Logger {
	if m.logger == nil {
		return logging.NewNop()
	}
	name := lane.name
	if name == "" {
		name = string(lane.kind)
	}
	return m.logger.With(
		logging.String("component", fmt.Sprintf("workflow-%s-runner", name)),
		logging.String("lane", name),
	)
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

func (m *Manager) runLane(ctx context.Context, lane *laneState) {
	defer m.wg.Done()
	if lane == nil {
		return
	}
	logger := lane.logger
	if logger == nil {
		logger = m.logger
	}
	if logger == nil {
		logger = logging.NewNop()
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if lane.runReclaimer {
			if err := m.reclaimStaleItems(ctx, logger, lane.processingStatuses); err != nil {
				logger.Warn("reclaim stale processing failed", logging.Error(err))
			}
		}

		item, err := m.nextItemForLane(ctx, lane)
		if err != nil {
			m.handleNextItemError(ctx, logger, err)
			continue
		}
		if item == nil {
			m.waitForItemOrShutdown(ctx)
			continue
		}

		if err := m.processItem(ctx, lane, logger, item); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
		}
	}
}

func (m *Manager) reclaimStaleItems(ctx context.Context, logger *slog.Logger, statuses []queue.Status) error {
	if m.heartbeatTimeout <= 0 {
		return nil
	}
	if len(statuses) == 0 {
		return nil
	}
	cutoff := time.Now().Add(-m.heartbeatTimeout)
	reclaimed, err := m.store.ReclaimStaleProcessing(ctx, cutoff, statuses...)
	if err != nil {
		return err
	}
	if reclaimed > 0 {
		logger.Info("reclaimed stale items", logging.Int64("count", reclaimed))
	}
	return nil
}

func (m *Manager) nextItemForLane(ctx context.Context, lane *laneState) (*queue.Item, error) {
	if lane == nil || len(lane.statusOrder) == 0 {
		return nil, nil
	}
	return m.store.NextForStatuses(ctx, lane.statusOrder...)
}

func (m *Manager) handleNextItemError(ctx context.Context, logger *slog.Logger, err error) {
	m.setLastError(err)
	logger.Error("failed to fetch next queue item", logging.Error(err))
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(m.cfg.ErrorRetryInterval) * time.Second):
	}
}

func (m *Manager) waitForItemOrShutdown(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(m.pollInterval):
	}
}

func (m *Manager) processItem(ctx context.Context, lane *laneState, laneLogger *slog.Logger, item *queue.Item) error {
	stage, ok := lane.stageForStatus(item.Status)
	if !ok {
		if laneLogger == nil {
			laneLogger = m.logger
		}
		if laneLogger == nil {
			laneLogger = logging.NewNop()
		}
		laneLogger.Warn("no stage configured for status", logging.String("status", string(item.Status)))
		m.waitForItemOrShutdown(ctx)
		return nil
	}

	requestID := uuid.NewString()
	stageCtx := withStageContext(ctx, lane, stage.name, item, requestID)
	stageLogger := m.stageLoggerForLane(stageCtx, lane, laneLogger, item)
	if aware, ok := stage.handler.(loggerAware); ok {
		aware.SetLogger(stageLogger)
	}

	if err := m.transitionToProcessing(stageCtx, lane, stage.processingStatus, stage.name, item); err != nil {
		stageLogger.Error("failed to transition item to processing", logging.Error(err))
		m.setLastError(err)
		return err
	}

	return m.executeStage(stageCtx, lane, stageLogger, stage, item)
}

func (m *Manager) stageLoggerForLane(ctx context.Context, lane *laneState, laneLogger *slog.Logger, item *queue.Item) *slog.Logger {
	base := laneLogger
	if base == nil {
		base = m.logger
	}
	if base == nil {
		base = logging.NewNop()
	}

	if lane != nil && lane.kind == laneBackground {
		path, created, err := m.ensureBackgroundLog(item)
		if err != nil {
			base.Warn("background log unavailable", logging.Error(err))
		} else {
			bgHandler, logErr := m.newBackgroundHandler(path)
			if logErr != nil {
				base.Warn("failed to create background log writer", logging.Error(logErr))
			} else {
				if created && laneLogger != nil {
					laneLogger.Info(
						"background log created",
						logging.String("path", path),
						logging.Int64("item_id", item.ID),
					)
				}
				// Background tasks should log ONLY to the item log, not the daemon log
				// Ensure item_id is baked into the logger so all background logs are properly tagged
				base = slog.New(bgHandler).With(logging.Int64("item_id", item.ID))
			}
		}
	}

	return logging.WithContext(ctx, base)
}

func (m *Manager) ensureBackgroundLog(item *queue.Item) (string, bool, error) {
	if item == nil {
		return "", false, errors.New("queue item is nil")
	}
	if strings.TrimSpace(m.backgroundLogDir) == "" {
		return "", false, errors.New("background log directory not configured")
	}
	created := false
	if strings.TrimSpace(item.BackgroundLogPath) == "" {
		filename := m.backgroundLogFilename(item)
		if filename == "" {
			filename = fmt.Sprintf("item-%d.log", item.ID)
		}
		item.BackgroundLogPath = filepath.Join(m.backgroundLogDir, filename)
		created = true
	}
	if err := os.MkdirAll(filepath.Dir(item.BackgroundLogPath), 0o755); err != nil {
		return "", false, fmt.Errorf("ensure background log directory: %w", err)
	}
	return item.BackgroundLogPath, created, nil
}

func (m *Manager) newBackgroundHandler(path string) (slog.Handler, error) {
	level := "info"
	format := "json"
	if m.cfg != nil {
		if strings.TrimSpace(m.cfg.LogLevel) != "" {
			level = m.cfg.LogLevel
		}
		if strings.TrimSpace(m.cfg.LogFormat) != "" {
			format = m.cfg.LogFormat
		}
	}
	logger, err := logging.New(logging.Options{
		Level:            level,
		Format:           format,
		OutputPaths:      []string{path},
		ErrorOutputPaths: []string{path},
		Development:      false,
		// Background logs write to item files, but still publish to the daemon stream so
		// users can observe per-item/episode progress via the log API and `spindle show --lane background --item <id>`.
		Stream: m.logHub,
	})
	if err != nil {
		return nil, err
	}
	return logger.Handler(), nil
}

func (m *Manager) backgroundLogFilename(item *queue.Item) string {
	timestamp := time.Now().UTC().Format("20060102T150405")
	fingerprint := strings.TrimSpace(item.DiscFingerprint)
	if fingerprint == "" {
		fingerprint = fmt.Sprintf("item-%d", item.ID)
	}
	title := sanitizeSlug(item.DiscTitle)
	if title == "" {
		title = "untitled"
	}
	return fmt.Sprintf("%s-%s-%s.log", timestamp, fingerprint, title)
}

func sanitizeSlug(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(value))
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(unicode.ToLower(r))
			lastDash = false
		case unicode.IsDigit(r):
			builder.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return ""
	}
	return slug
}

func (m *Manager) executeStage(ctx context.Context, lane *laneState, stageLogger *slog.Logger, stage pipelineStage, item *queue.Item) error {
	stageStart := time.Now()
	stageLogger.Info(
		"stage started",
		logging.String("processing_status", string(stage.processingStatus)),
		logging.String("disc_title", strings.TrimSpace(item.DiscTitle)),
		logging.String("source_path", strings.TrimSpace(item.SourcePath)),
	)
	if lane != nil && lane.kind == laneBackground && lane.logger != nil {
		logging.WithContext(ctx, lane.logger).Info(
			"background stage started",
			logging.String("stage", stage.name),
			logging.Int64("item_id", item.ID),
			logging.String("log_path", strings.TrimSpace(item.BackgroundLogPath)),
		)
	}

	handler := stage.handler
	if handler == nil {
		stageLogger.Warn("missing stage handler", logging.String("stage", stage.name))
		item.Status = queue.StatusFailed
		item.ErrorMessage = fmt.Sprintf("stage %s missing handler", stage.name)
		if err := m.store.Update(ctx, item); err != nil {
			stageLogger.Error("failed to persist missing handler failure", logging.Error(err))
		}
		m.setLastError(errors.New("stage handler unavailable"))
		return errors.New("stage handler unavailable")
	}

	if err := handler.Prepare(ctx, item); err != nil {
		m.handleStageFailure(ctx, stage.name, item, err)
		m.setLastError(err)
		return err
	}
	if err := m.store.Update(ctx, item); err != nil {
		wrapped := fmt.Errorf("persist stage preparation: %w", err)
		stageLogger.Error("failed to persist stage preparation", logging.Error(wrapped))
		m.setLastError(wrapped)
		return wrapped
	}

	execErr := m.executeWithHeartbeat(ctx, handler, item)
	if execErr != nil {
		if errors.Is(execErr, context.Canceled) {
			stageLogger.Info("stage interrupted by shutdown")
			return execErr
		}
		m.handleStageFailure(ctx, stage.name, item, execErr)
		m.setLastError(execErr)
		return execErr
	}

	if item.Status == stage.processingStatus || item.Status == "" {
		item.Status = stage.doneStatus
	}
	item.LastHeartbeat = nil
	if err := m.store.Update(ctx, item); err != nil {
		wrapped := fmt.Errorf("persist stage result: %w", err)
		stageLogger.Error("failed to persist stage result", logging.Error(wrapped))
		m.setLastError(wrapped)
		return wrapped
	}
	stageLogger.Info(
		"stage completed",
		logging.String("next_status", string(item.Status)),
		logging.String("progress_stage", strings.TrimSpace(item.ProgressStage)),
		logging.String("progress_message", strings.TrimSpace(item.ProgressMessage)),
		logging.Duration("elapsed", time.Since(stageStart)),
	)
	if lane != nil && lane.kind == laneBackground && lane.logger != nil {
		logging.WithContext(ctx, lane.logger).Info(
			"background stage completed",
			logging.String("stage", stage.name),
			logging.Int64("item_id", item.ID),
			logging.Duration("elapsed", time.Since(stageStart)),
		)
	}
	m.setLastItem(item)
	m.checkQueueCompletion(ctx)
	return nil
}

func (m *Manager) executeWithHeartbeat(ctx context.Context, handler StageHandler, item *queue.Item) error {
	hbCtx, hbCancel := context.WithCancel(ctx)
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go m.heartbeatLoop(hbCtx, &hbWG, item.ID)

	execErr := handler.Execute(ctx, item)
	hbCancel()
	hbWG.Wait()
	return execErr
}

func (m *Manager) transitionToProcessing(ctx context.Context, lane *laneState, processing queue.Status, stageName string, item *queue.Item) error {
	if processing == "" {
		return errors.New("processing status must not be empty")
	}

	m.setItemProcessingState(item, processing)
	if err := m.store.Update(ctx, item); err != nil {
		return fmt.Errorf("persist processing transition: %w", err)
	}
	m.setLastItem(item)
	if lane == nil || lane.notificationsEnabled {
		m.onItemStarted(ctx)
	}
	return nil
}

func (m *Manager) setItemProcessingState(item *queue.Item, processing queue.Status) {
	now := time.Now().UTC()
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
}

func (m *Manager) heartbeatLoop(ctx context.Context, wg *sync.WaitGroup, itemID int64) {
	defer wg.Done()
	ticker := time.NewTicker(m.heartbeatInterval)
	defer ticker.Stop()

	logger := logging.WithContext(ctx, m.logger.With(logging.String("component", "workflow-heartbeat")))

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.store.UpdateHeartbeat(ctx, itemID); err != nil {
				// Check if this is a context cancellation (normal shutdown)
				if errors.Is(err, context.Canceled) {
					logger.Info("daemon shutting down, heartbeat update cancelled")
				} else {
					logger.Warn("heartbeat update failed", logging.Error(err))
				}
			}
		}
	}
}

func (m *Manager) handleStageFailure(ctx context.Context, stageName string, item *queue.Item, stageErr error) {
	logger := logging.WithContext(ctx, m.logger.With(logging.String("component", "workflow-manager")))

	status, message := m.classifyStageFailure(stageName, stageErr)
	m.setItemFailureState(item, status, message)

	alertValue := "stage_failure"
	if status == queue.StatusReview {
		alertValue = "review_required"
	}
	logger.Error("stage failed",
		logging.String("resolved_status", string(status)),
		logging.String("error_message", strings.TrimSpace(message)),
		logging.Alert(alertValue),
		logging.Error(stageErr),
	)

	if err := m.store.Update(ctx, item); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("daemon shutting down, could not update stage failure")
		} else {
			logger.Error("failed to persist stage failure", logging.Error(err))
		}
	}

	m.setLastItem(item)
	m.notifyStageError(ctx, stageName, item, stageErr)
	m.checkQueueCompletion(ctx)
}

func (m *Manager) classifyStageFailure(stageName string, stageErr error) (queue.Status, string) {
	if stageErr == nil {
		msg := m.getStageFailureMessage(stageName, "failed without error detail")
		return queue.StatusFailed, msg
	}

	status := services.FailureStatus(stageErr)
	message := strings.TrimSpace(stageErr.Error())
	if message == "" {
		message = m.getStageFailureMessage(stageName, "failed")
	}
	return status, message
}

func (m *Manager) getStageFailureMessage(stageName, defaultMsg string) string {
	if stageName != "" {
		return fmt.Sprintf("%s %s", stageName, defaultMsg)
	}
	return fmt.Sprintf("workflow %s", defaultMsg)
}

func (m *Manager) setItemFailureState(item *queue.Item, status queue.Status, message string) {
	item.Status = status
	item.ErrorMessage = message

	if status == queue.StatusReview {
		item.ProgressStage = "Needs review"
	} else {
		item.ProgressStage = "Failed"
	}

	item.ProgressMessage = message
	item.ProgressPercent = 0
	item.LastHeartbeat = nil
}

func withStageContext(ctx context.Context, lane *laneState, stageName string, item *queue.Item, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if item != nil {
		ctx = services.WithItemID(ctx, item.ID)
	}
	if stageName != "" {
		ctx = services.WithStage(ctx, stageName)
	}
	if lane != nil {
		laneLabel := strings.TrimSpace(lane.name)
		if laneLabel == "" {
			laneLabel = string(lane.kind)
		}
		ctx = services.WithLane(ctx, laneLabel)
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
	stageSet := make([]pipelineStage, 0)
	for _, kind := range m.laneOrder {
		lane := m.lanes[kind]
		if lane == nil {
			continue
		}
		stageSet = append(stageSet, lane.stages...)
	}
	m.mu.RUnlock()

	stats, err := m.store.Stats(ctx)
	if err != nil {
		m.logger.Warn("failed to read queue stats", logging.Error(err))
	}

	health := make(map[string]stage.Health, len(stageSet))
	for _, stg := range stageSet {
		handler := stg.handler
		if handler == nil {
			continue
		}
		health[stg.name] = handler.HealthCheck(ctx)
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
	logger := logging.WithContext(ctx, m.logger.With(logging.String("component", "workflow-manager")))
	contextLabel := fmt.Sprintf("%s (item #%d)", stageName, item.ID)
	if err := m.notifier.Publish(ctx, notifications.EventError, notifications.Payload{
		"error":   stageErr,
		"context": contextLabel,
	}); err != nil {
		// Check if this is a context cancellation (normal shutdown)
		if errors.Is(err, context.Canceled) {
			logger.Info("daemon shutting down, could not send error notification")
		} else {
			logger.Debug("stage error notification failed", logging.Error(err))
		}
	}
}

func (m *Manager) onItemStarted(ctx context.Context) {
	if m.notifier == nil {
		return
	}
	stats, err := m.store.Stats(ctx)
	if err != nil {
		// Check if this is a context cancellation (normal shutdown)
		if errors.Is(err, context.Canceled) {
			m.logger.Info("daemon shutting down, could not get queue stats for start notification")
		} else {
			m.logger.Warn("queue stats unavailable for start notification", logging.Error(err))
		}
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
	if err := m.notifier.Publish(ctx, notifications.EventQueueStarted, notifications.Payload{"count": count}); err != nil {
		// Check if this is a context cancellation (normal shutdown)
		if errors.Is(err, context.Canceled) {
			m.logger.Info("daemon shutting down, could not send queue start notification")
		} else {
			m.logger.Debug("queue start notification failed", logging.Error(err))
		}
	}
}

func (m *Manager) checkQueueCompletion(ctx context.Context) {
	if m.notifier == nil {
		return
	}
	stats, err := m.store.Stats(ctx)
	if err != nil {
		// Check if this is a context cancellation (normal shutdown)
		if errors.Is(err, context.Canceled) {
			m.logger.Info("daemon shutting down, could not check queue completion")
		} else {
			m.logger.Warn("queue stats unavailable for completion notification", logging.Error(err))
		}
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
	if err := m.notifier.Publish(ctx, notifications.EventQueueCompleted, notifications.Payload{
		"processed": processed,
		"failed":    failed,
		"duration":  duration,
	}); err != nil {
		// Check if this is a context cancellation (normal shutdown)
		if errors.Is(err, context.Canceled) {
			m.logger.Info("daemon shutting down, could not send queue completion notification")
		} else {
			m.logger.Debug("queue completion notification failed", logging.Error(err))
		}
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
