package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/disc"
	"spindle/internal/disc/fingerprint"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
)

type discInfo struct {
	Device string
	Label  string
	Type   string
}

type detectFunc func(ctx context.Context, device string) (*discInfo, error)

type discScanner interface {
	Scan(ctx context.Context, device string) (*disc.ScanResult, error)
}

type commandRunner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execCommandRunner struct{}

func (execCommandRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec
	return cmd.Output()
}

type discMonitor struct {
	cfg      *config.Config
	store    *queue.Store
	logger   *zap.Logger
	notifier notifications.Service
	scanner  discScanner
	fp       func(ctx context.Context, device, discType string, timeout time.Duration) (string, error)

	device       string
	scanTimeout  time.Duration
	pollInterval time.Duration
	detect       detectFunc

	mu          sync.Mutex
	running     bool
	discPresent bool
	processing  bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newDiscMonitor(cfg *config.Config, store *queue.Store, logger *zap.Logger) *discMonitor {
	if cfg == nil || store == nil {
		return nil
	}

	device := strings.TrimSpace(cfg.OpticalDrive)
	if device == "" {
		return nil
	}

	poll := time.Duration(cfg.DiscMonitorTimeout) * time.Second
	if poll <= 0 {
		poll = 5 * time.Second
	}

	scanTimeout := time.Duration(cfg.MakeMKVInfoTimeout) * time.Second
	if scanTimeout <= 0 {
		scanTimeout = 300 * time.Second
	}

	monitorLogger := logger
	if monitorLogger != nil {
		monitorLogger = monitorLogger.With(zap.String("component", "disc-monitor"))
	}

	runner := execCommandRunner{}
	detect := buildDetectFunc(runner, poll)

	return &discMonitor{
		cfg:          cfg,
		store:        store,
		logger:       monitorLogger,
		notifier:     notifications.NewService(cfg),
		scanner:      disc.NewScanner(cfg.MakemkvBinary()),
		device:       device,
		scanTimeout:  scanTimeout,
		pollInterval: poll,
		detect:       detect,
		fp:           fingerprint.ComputeTimeout,
	}
}

func (m *discMonitor) Start(ctx context.Context) error {
	if m == nil {
		return errors.New("disc monitor unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return errors.New("disc monitor already running")
	}

	runCtx, cancel := context.WithCancel(ctx)
	m.ctx = runCtx
	m.cancel = cancel
	m.running = true

	m.wg.Add(1)
	go m.loop()
	return nil
}

func (m *discMonitor) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	cancel := m.cancel
	m.running = false
	m.cancel = nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
}

func (m *discMonitor) loop() {
	defer m.wg.Done()

	m.poll()

	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.poll()
		}
	}
}

func (m *discMonitor) poll() {
	ctx := m.ctx
	if ctx == nil {
		return
	}

	info, err := m.detect(ctx, m.device)
	if err != nil {
		logger := m.logger
		if logger == nil {
			logger = zap.NewNop()
		}
		logger.Warn("disc detection failed", zap.Error(err))
		return
	}

	if info == nil {
		m.mu.Lock()
		m.discPresent = false
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	if m.discPresent || m.processing {
		m.mu.Unlock()
		return
	}
	m.discPresent = true
	m.processing = true
	m.mu.Unlock()

	m.wg.Add(1)
	go func(det discInfo) {
		defer m.wg.Done()
		success := m.handleDetectedDisc(ctx, det)

		m.mu.Lock()
		if !success {
			m.discPresent = false
		}
		m.processing = false
		m.mu.Unlock()
	}(*info)
}

func (m *discMonitor) handleDetectedDisc(ctx context.Context, info discInfo) bool {
	logger := logging.WithContext(ctx, m.logger).With(
		zap.String("device", info.Device),
		zap.String("disc_label", info.Label),
		zap.String("disc_type", info.Type),
	)
	logger.Info("detected disc")

	scanCtx := ctx
	var cancel context.CancelFunc
	if m.scanTimeout > 0 {
		scanCtx, cancel = context.WithTimeout(ctx, m.scanTimeout)
		defer cancel()
	}

	fingerprinter := m.fp
	if fingerprinter == nil {
		fingerprinter = fingerprint.ComputeTimeout
	}
	discFingerprint, fpErr := fingerprinter(scanCtx, info.Device, info.Type, m.scanTimeout)
	if fpErr != nil {
		logger.Error("generate fingerprint failed", zap.Error(fpErr))
		if m.notifier != nil {
			if notifyErr := m.notifier.Publish(ctx, notifications.EventError, notifications.Payload{
				"error":   fpErr,
				"context": info.Label,
			}); notifyErr != nil {
				logger.Warn("failed to send fingerprint error notification", zap.Error(notifyErr))
			}
		}
		return false
	}
	logger.Info("computed fingerprint", zap.String("fingerprint", discFingerprint))

	existing, err := m.store.FindByFingerprint(ctx, discFingerprint)
	if err != nil {
		logger.Error("lookup existing disc", zap.Error(err))
		return false
	}

	if existing != nil {
		updated := false
		label := strings.TrimSpace(info.Label)
		if label != "" && label != strings.TrimSpace(existing.DiscTitle) {
			existing.DiscTitle = label
			updated = true
		}
		if existing.DiscFingerprint != discFingerprint {
			existing.DiscFingerprint = discFingerprint
			updated = true
		}

		status := existing.Status
		if status == queue.StatusCompleted {
			if updated {
				if err := m.store.Update(ctx, existing); err != nil {
					logger.Warn("failed to update completed item", zap.Error(err))
				}
			}
			logger.Info("disc already completed", zap.Int64(logging.FieldItemID, existing.ID))
			return true
		}

		if status == queue.StatusIdentified || status == queue.StatusRipped || status == queue.StatusEncoded || status == queue.StatusOrganizing || existing.IsProcessing() {
			if updated {
				if err := m.store.Update(ctx, existing); err != nil {
					logger.Warn("failed to update in-flight item", zap.Error(err))
				}
			}
			logger.Info("disc already in workflow", zap.Int64(logging.FieldItemID, existing.ID), zap.String("status", string(existing.Status)))
			return true
		}

		existing.Status = queue.StatusPending
		existing.ErrorMessage = ""
		existing.ProgressStage = "Awaiting identification"
		existing.ProgressPercent = 0
		existing.ProgressMessage = ""
		existing.NeedsReview = false
		existing.ReviewReason = ""
		existing.DiscFingerprint = discFingerprint
		if label != "" {
			existing.DiscTitle = label
		}

		if err := m.store.Update(ctx, existing); err != nil {
			logger.Error("failed to reset existing item", zap.Error(err))
			return false
		}

		logger.Info("reset existing disc for processing", zap.Int64(logging.FieldItemID, existing.ID))
		return true
	}

	title := strings.TrimSpace(info.Label)
	if title == "" {
		title = "Unknown Disc"
	}

	item, err := m.store.NewDisc(ctx, title, discFingerprint)
	if err != nil {
		logger.Error("failed to enqueue disc", zap.Error(err))
		return false
	}

	logger.Info("queued new disc", zap.Int64(logging.FieldItemID, item.ID))
	return true
}

func buildDetectFunc(runner commandRunner, timeout time.Duration) detectFunc {
	return func(ctx context.Context, device string) (*discInfo, error) {
		return detectDisc(ctx, runner, device, timeout)
	}
}

func detectDisc(ctx context.Context, runner commandRunner, device string, timeout time.Duration) (*discInfo, error) {
	if device == "" {
		return nil, errors.New("optical drive not configured")
	}

	detectCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		detectCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	output, err := runner.Output(detectCtx, "lsblk", "-P", "-o", "LABEL,FSTYPE", device)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || detectCtx.Err() != nil {
			return nil, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, nil
		}
		return nil, fmt.Errorf("lsblk: %w", err)
	}

	label, fstype := parseLsblkOutput(string(output))
	if strings.TrimSpace(label) == "" && strings.TrimSpace(fstype) == "" {
		return nil, nil
	}

	discType := determineDiscType(detectCtx, runner, device, fstype, timeout)
	info := &discInfo{
		Device: device,
		Label:  fallbackLabel(label),
		Type:   discType,
	}
	return info, nil
}

func parseLsblkOutput(output string) (string, string) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		data := parseKeyValueLine(line)
		if len(data) == 0 {
			continue
		}
		return data["LABEL"], data["FSTYPE"]
	}
	return "", ""
}

func parseKeyValueLine(line string) map[string]string {
	result := make(map[string]string)
	fields := strings.Fields(line)
	for _, field := range fields {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, "\"")
		result[key] = value
	}
	return result
}

func determineDiscType(ctx context.Context, runner commandRunner, device, fstype string, timeout time.Duration) string {
	lowerFS := strings.ToLower(strings.TrimSpace(fstype))

	detectCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		detectCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	blkidOutput, err := runner.Output(detectCtx, "blkid", "-p", "-o", "export", device)
	if err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(blkidOutput)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			if strings.EqualFold(parts[0], "TYPE") {
				lowerFS = strings.ToLower(strings.TrimSpace(parts[1]))
				break
			}
		}
	}

	switch lowerFS {
	case "udf":
		if discType := detectBluRayOrDVD(ctx, runner, device, timeout); discType != "" {
			return discType
		}
		return "Blu-ray"
	case "iso9660":
		return "DVD"
	case "":
		if discType := detectBluRayOrDVD(ctx, runner, device, timeout); discType != "" {
			return discType
		}
		return "Unknown"
	default:
		return strings.ToUpper(lowerFS)
	}
}

func detectBluRayOrDVD(ctx context.Context, runner commandRunner, device string, timeout time.Duration) string {
	detectCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		detectCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	fileOutput, err := runner.Output(detectCtx, "file", "-s", device)
	if err == nil {
		lower := strings.ToLower(string(fileOutput))
		if strings.Contains(lower, "blu-ray") || strings.Contains(lower, "bdav") || strings.Contains(lower, "bdmv") {
			return "Blu-ray"
		}
		if strings.Contains(lower, "iso 9660") || strings.Contains(lower, "dvd") {
			return "DVD"
		}
	}

	mountPoints := []string{"/media/cdrom", "/media/cdrom0"}
	for _, mount := range mountPoints {
		entries, err := os.ReadDir(mount)
		if err != nil {
			continue
		}
		hasBDMV := false
		hasVideoTS := false
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := strings.ToUpper(entry.Name())
			if name == "BDMV" {
				hasBDMV = true
			}
			if name == "VIDEO_TS" {
				hasVideoTS = true
			}
		}
		if hasBDMV {
			return "Blu-ray"
		}
		if hasVideoTS {
			return "DVD"
		}
	}

	return ""
}

func fallbackLabel(label string) string {
	trimmed := strings.TrimSpace(label)
	if trimmed != "" {
		return trimmed
	}
	return "Unknown Disc"
}
