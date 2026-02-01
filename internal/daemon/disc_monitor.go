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

	"log/slog"

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

type fingerprintProvider interface {
	Compute(ctx context.Context, device, discType string) (string, error)
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
	cfg     *config.Config
	logger  *slog.Logger
	scanner discScanner

	queueHandler        queueProcessor
	errorNotifier       fingerprintErrorNotifier
	fingerprintProvider fingerprintProvider

	device       string
	scanTimeout  time.Duration
	pollInterval time.Duration
	detect       detectFunc
	isPaused     func() bool

	mu          sync.Mutex
	running     bool
	discPresent bool
	processing  bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// defaultFingerprintProvider uses the fingerprint package.
type defaultFingerprintProvider struct{}

func (defaultFingerprintProvider) Compute(ctx context.Context, device, discType string) (string, error) {
	return fingerprint.ComputeTimeout(ctx, device, discType, 2*time.Minute)
}

func newDiscMonitor(cfg *config.Config, store *queue.Store, logger *slog.Logger, isPaused func() bool) *discMonitor {
	if cfg == nil || store == nil {
		return nil
	}

	device := strings.TrimSpace(cfg.MakeMKV.OpticalDrive)
	if device == "" {
		return nil
	}

	poll := time.Duration(cfg.Workflow.DiscMonitorTimeout) * time.Second
	if poll <= 0 {
		poll = 5 * time.Second
	}

	scanTimeout := time.Duration(cfg.MakeMKV.InfoTimeout) * time.Second
	if scanTimeout <= 0 {
		scanTimeout = 300 * time.Second
	}

	monitorLogger := logger
	if monitorLogger != nil {
		monitorLogger = monitorLogger.With(logging.String("component", "disc-monitor"))
	}

	runner := execCommandRunner{}
	detect := buildDetectFunc(runner, poll)

	return &discMonitor{
		cfg:                 cfg,
		logger:              monitorLogger,
		scanner:             disc.NewScanner(cfg.MakemkvBinary()),
		queueHandler:        newQueueStoreProcessor(store),
		errorNotifier:       newNotifierAdapter(notifications.NewService(cfg)),
		fingerprintProvider: defaultFingerprintProvider{},
		device:              device,
		scanTimeout:         scanTimeout,
		pollInterval:        poll,
		detect:              detect,
		isPaused:            isPaused,
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

	if m.isPaused != nil && m.isPaused() {
		return
	}

	// Skip detection if disc already being tracked or processed.
	// This avoids running lsblk/blkid while MakeMKV is ripping,
	// which can cause read errors from concurrent device access.
	m.mu.Lock()
	if m.discPresent || m.processing {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	info, err := m.detect(ctx, m.device)
	if err != nil {
		logger := m.logger
		if logger == nil {
			logger = logging.NewNop()
		}
		logger.Warn("disc detection failed; will retry",
			logging.Error(err),
			logging.String(logging.FieldEventType, "disc_detect_failed"),
			logging.String(logging.FieldErrorHint, "check optical drive path, permissions, and mount state; see README.md"),
		)
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
		// Race: another goroutine beat us to it
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
		logging.String("device", info.Device),
		logging.String("disc_label", info.Label),
		logging.String("disc_type", info.Type),
	)
	logger.Info("detected disc",
		logging.String(logging.FieldEventType, "disc_detected"),
	)

	// Compute fingerprint from disc filesystem (uses SHA-256 hash of metadata files).
	// This must happen before MakeMKV scan to ensure CLI and daemon use the same method.
	logger.Debug("computing fingerprint from disc filesystem")
	discFingerprint, fpErr := m.fingerprintProvider.Compute(ctx, info.Device, info.Type)
	if fpErr != nil {
		logger.Error("fingerprint computation failed; disc not queued",
			logging.Error(fpErr),
			logging.String(logging.FieldEventType, "disc_fingerprint_failed"),
			logging.String(logging.FieldErrorHint, "verify disc is readable and mounted"),
		)
		if m.errorNotifier != nil {
			m.errorNotifier.FingerprintFailed(ctx, info, fpErr, logger)
		}
		return false
	}
	logger.Debug("computed fingerprint", logging.String("fingerprint", discFingerprint))

	// Skip MakeMKV scan if disc is already being processed. This prevents scan
	// failures when the drive is locked by an active rip operation.
	if m.queueHandler != nil {
		if inWorkflow, itemID := m.queueHandler.IsInWorkflow(ctx, discFingerprint); inWorkflow {
			logger.Debug("disc already in workflow, skipping scan",
				logging.Int64(logging.FieldItemID, itemID),
				logging.String("fingerprint", discFingerprint),
			)
			return true
		}
	}

	scanCtx := ctx
	var cancel context.CancelFunc
	if m.scanTimeout > 0 {
		scanCtx, cancel = context.WithTimeout(ctx, m.scanTimeout)
		defer cancel()
	}

	scanner := m.scanner
	if scanner == nil {
		logger.Error("disc scanner unavailable; disc not queued",
			logging.String(logging.FieldEventType, "disc_scan_unavailable"),
			logging.String(logging.FieldErrorHint, "check MakeMKV installation and config.makemkv_binary"),
		)
		return false
	}

	logger.Debug("scanning disc for title information", logging.Duration("timeout", m.scanTimeout))
	_, scanErr := scanner.Scan(scanCtx, info.Device)
	if scanErr != nil {
		logger.Error("disc scan failed; disc not queued",
			logging.Error(scanErr),
			logging.String(logging.FieldEventType, "disc_scan_failed"),
			logging.String(logging.FieldErrorHint, "verify drive access and MakeMKV availability; rerun with spindle identify for details"),
		)
		if m.errorNotifier != nil {
			m.errorNotifier.FingerprintFailed(ctx, info, scanErr, logger)
		}
		return false
	}

	queueHandler := m.queueHandler
	if queueHandler == nil {
		logger.Error("queue handler unavailable; disc not queued",
			logging.String(logging.FieldEventType, "queue_handler_unavailable"),
			logging.String(logging.FieldErrorHint, "restart the daemon or check queue database initialization"),
		)
		return false
	}

	success, err := queueHandler.Process(ctx, info, discFingerprint, logger)
	if err != nil {
		logger.Error("queue processing failed; disc not queued",
			logging.Error(err),
			logging.String(logging.FieldEventType, "queue_processing_failed"),
			logging.String(logging.FieldErrorHint, "check queue database health and daemon logs"),
		)
		return false
	}
	return success
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
