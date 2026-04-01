//go:build linux

package discmonitor

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/five82/spindle/internal/fingerprint"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/queue"
)

var (
	leadingDigitPrefixRe = regexp.MustCompile(`^\d+_`)
	seasonDiscSuffixRe   = regexp.MustCompile(`(?i)_S\d+_DISC_\d+$`)
	tvSuffixRe           = regexp.MustCompile(`(?i)_TV$`)
	allDigitsRe          = regexp.MustCompile(`^\d+$`)
)

// unusableLabels are generic disc labels that provide no useful identification.
var unusableLabels = []string{
	"logical_volume_id",
	"volume_id",
	"dvd_video",
	"bluray",
	"bd_rom",
	"untitled",
	"unknown disc",
}

// unusablePrefixes are label prefixes that indicate generic disc labels.
var unusablePrefixes = []string{
	"volume_",
	"disk_",
	"track_",
}

// DiscEvent represents a disc insertion or removal event.
type DiscEvent struct {
	Device    string // e.g., "/dev/sr0"
	Label     string // disc label from lsblk
	DiscType  string // "Blu-ray", "DVD", "Unknown"
	MountPath string // mount point if mounted
}

// Monitor watches for optical disc events.
type Monitor struct {
	device     string
	logger     *slog.Logger
	paused     atomic.Bool
	mu         sync.Mutex
	processing bool
	store      *queue.Store
}

// New creates a disc monitor for the given device.
func New(device string, store *queue.Store, logger *slog.Logger) *Monitor {
	if device == "" {
		device = "/dev/sr0"
	}
	logger = logs.Default(logger)
	return &Monitor{
		device: device,
		logger: logger,
		store:  store,
	}
}

// Device returns the optical drive device path.
func (m *Monitor) Device() string { return m.device }

// PauseDisc atomically pauses disc event processing.
// Returns true if the state changed (was not already paused).
func (m *Monitor) PauseDisc() bool { return m.paused.CompareAndSwap(false, true) }

// ResumeDisc atomically resumes disc event processing.
// Returns true if the state changed (was actually paused).
func (m *Monitor) ResumeDisc() bool { return m.paused.CompareAndSwap(true, false) }

// IsPaused returns whether the monitor is paused.
func (m *Monitor) IsPaused() bool { return m.paused.Load() }

// acquireForDetection runs the shared guard checks (paused, disc-busy,
// already-processing) and sets the processing flag on success. Returns a
// human-readable skip reason or "" if the caller may proceed. When "" is
// returned the caller owns the processing flag and must clear it.
func (m *Monitor) acquireForDetection() (skipReason string, _ error) {
	if m.IsPaused() {
		m.logger.Info("disc detection skipped (paused)",
			"decision_type", logs.DecisionDetectGuard,
			"decision_result", "skipped",
			"decision_reason", "monitor paused",
		)
		return "disc detection paused", nil
	}

	if m.store != nil {
		busy, err := m.store.HasDiscDependentItem()
		if err != nil {
			return "", fmt.Errorf("check disc dependent items: %w", err)
		}
		if busy {
			m.logger.Info("disc detection skipped (disc-dependent item in progress)",
				"decision_type", logs.DecisionDetectGuard,
				"decision_result", "skipped",
				"decision_reason", "disc-dependent pipeline stage active",
			)
			return "disc in use by active workflow", nil
		}
	}

	m.mu.Lock()
	if m.processing {
		m.mu.Unlock()
		m.logger.Info("disc detection skipped (already processing)",
			"decision_type", logs.DecisionDetectGuard,
			"decision_result", "skipped",
			"decision_reason", "detection already in progress",
		)
		return "already processing a disc", nil
	}
	m.processing = true
	m.mu.Unlock()
	return "", nil
}

// releaseProcessing clears the processing flag.
func (m *Monitor) releaseProcessing() {
	m.mu.Lock()
	m.processing = false
	m.mu.Unlock()
}

// Detect wraps the full disc detection pipeline with concurrency guards.
// Returns nil event (not an error) if paused, already processing, or a
// disc-dependent item is in progress.
func (m *Monitor) Detect(ctx context.Context) (*DiscEvent, error) {
	skip, err := m.acquireForDetection()
	if err != nil {
		return nil, err
	}
	if skip != "" {
		return nil, nil
	}
	defer m.releaseProcessing()

	// Create a fingerprint context with 2-minute timeout.
	fpCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	event, err := ProbeDisc(fpCtx, m.device)
	if err != nil {
		return nil, err
	}

	return event, nil
}

// DetectResponse is the synchronous result of an async detection request.
// Guard checks and disc probe run synchronously; fingerprinting and queue
// insertion continue in a background goroutine.
type DetectResponse struct {
	Handled bool   `json:"handled"`
	Message string `json:"message"`
}

// DetectAsync runs guard checks and an lsblk probe synchronously, then spawns
// a background goroutine for mount resolution, fingerprinting, and queue
// insertion. The processing mutex spans the entire pipeline (set here, cleared
// when the background goroutine completes).
func (m *Monitor) DetectAsync(ctx context.Context) (*DetectResponse, error) {
	skip, err := m.acquireForDetection()
	if err != nil {
		return nil, err
	}
	if skip != "" {
		return &DetectResponse{Handled: false, Message: skip}, nil
	}
	// Do NOT defer releaseProcessing here; the background goroutine owns the reset.

	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	event, err := ProbeDisc(probeCtx, m.device)
	if err != nil {
		m.releaseProcessing()
		return nil, err
	}
	if event == nil {
		m.releaseProcessing()
		return &DetectResponse{Handled: false, Message: "no disc detected in drive"}, nil
	}

	m.logger.Info("disc detected, starting background enqueue",
		"event_type", "disc_detected",
		"label", event.Label,
		"disc_type", event.DiscType,
		"device", event.Device,
	)

	go m.enqueueBackground(event)

	return &DetectResponse{
		Handled: true,
		Message: fmt.Sprintf("Disc detected: %s (%s)", event.Label, event.DiscType),
	}, nil
}

// enqueueBackground runs enqueuePipeline in a background goroutine with a
// detached 2-minute timeout. All errors are logged; there is no caller to
// return them to.
func (m *Monitor) enqueueBackground(event *DiscEvent) {
	defer m.releaseProcessing()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if _, err := m.enqueuePipeline(ctx, event); err != nil {
		m.logger.Error("background enqueue failed", "error", err)
	}
}

// enqueuePipeline runs the mount → fingerprint → duplicate-check → queue-insert
// pipeline. Shared by both the synchronous (DetectAndEnqueue) and asynchronous
// (enqueueBackground) code paths.
func (m *Monitor) enqueuePipeline(ctx context.Context, event *DiscEvent) (*EnqueueResult, error) {
	mountPoint, cleanup, err := ResolveMountPoint(ctx, event.Device, event.MountPath, m.logger)
	if err != nil {
		m.logger.Error("mount resolution failed",
			"error", err,
			"device", event.Device,
			"event_type", "fingerprint_error",
			"error_hint", "disc may not be mounted; check fstab or mount manually",
		)
		return nil, fmt.Errorf("resolve mount point: %w", err)
	}
	defer cleanup()

	fp, err := fingerprint.Generate(mountPoint, m.logger)
	if err != nil {
		m.logger.Error("fingerprint computation failed",
			"error", err,
			"mount_point", mountPoint,
			"event_type", "fingerprint_error",
			"error_hint", "disc filesystem may be unreadable",
		)
		return nil, fmt.Errorf("compute fingerprint: %w", err)
	}

	m.logger.Debug("disc fingerprint computed",
		"fingerprint", fp,
		"mount_point", mountPoint,
		"disc_type", event.DiscType,
	)

	if m.store == nil {
		return nil, fmt.Errorf("queue store not configured")
	}

	existing, err := m.store.FindByFingerprint(fp)
	if err != nil {
		return nil, fmt.Errorf("check duplicate fingerprint: %w", err)
	}

	if existing != nil {
		m.logDuplicateDecision(ctx, existing, event, fp)
		return &EnqueueResult{Item: existing, Event: event, Duplicate: true}, nil
	}

	title := ExtractDiscNameFromVolumeID(event.Label)
	if title == "" {
		title = "Unknown Disc"
	}

	item, err := m.store.NewDisc(title, fp)
	if err != nil {
		return nil, fmt.Errorf("create queue item: %w", err)
	}

	m.logger.Info("disc enqueued",
		"decision_type", logs.DecisionDiscEnqueue,
		"decision_result", "created",
		"decision_reason", "new disc fingerprint",
		"item_id", item.ID,
		"disc_title", title,
		"fingerprint", fp,
	)

	return &EnqueueResult{Item: item, Event: event}, nil
}

// logDuplicateDecision handles the decision logic for a disc whose fingerprint
// already exists in the queue. It logs the outcome and optionally refreshes the
// disc title for terminal items. Used by both sync and async code paths.
func (m *Monitor) logDuplicateDecision(ctx context.Context, existing *queue.Item, event *DiscEvent, fp string) {
	// User-stopped items are not reset for reprocessing.
	if existing.Stage == queue.StageFailed && strings.Contains(existing.ReviewReason, queue.ReviewReasonUserStopped) {
		m.logger.Info("disc detection skipped (user-stopped item)",
			"decision_type", logs.DecisionDuplicateDetection,
			"decision_result", "skipped",
			"decision_reason", "item was intentionally stopped by user",
			"item_id", existing.ID,
			"fingerprint", fp,
		)
		return
	}

	// Terminal items (completed/failed): allow re-insertion title refresh.
	if existing.Stage == queue.StageCompleted || existing.Stage == queue.StageFailed {
		if shouldRefreshDiscTitle(existing.DiscTitle) {
			tryRefreshDiscTitle(ctx, m.store, existing, event.Device, m.logger)
		}
		m.logger.Info("disc already processed",
			"decision_type", logs.DecisionDuplicateDetection,
			"decision_result", "skipped",
			"decision_reason", "disc already in terminal stage",
			"item_id", existing.ID,
			"stage", existing.Stage,
			"fingerprint", fp,
		)
		return
	}

	// In-workflow duplicate: log and skip.
	m.logger.Info("duplicate disc detected",
		"decision_type", logs.DecisionDuplicateDetection,
		"decision_result", "skipped",
		"decision_reason", "identical fingerprint already in workflow",
		"item_id", existing.ID,
		"stage", existing.Stage,
		"fingerprint", fp,
	)
}

// EnqueueResult describes the outcome of DetectAndEnqueue.
type EnqueueResult struct {
	Item      *queue.Item `json:"item"`
	Event     *DiscEvent  `json:"event"`
	Duplicate bool        `json:"duplicate"` // true if an existing in-workflow item was found
}

// DetectAndEnqueue runs the full disc detection pipeline: probe, fingerprint,
// duplicate check, and queue submission. Returns nil result (not an error) when
// detection is skipped (paused, busy, no disc). Per spec:
//   - Duplicate fingerprint in workflow: return existing item, do not create new.
//   - User-stopped item: do not reset for reprocessing.
//   - Completed disc re-insertion: refresh title if unusable.
func (m *Monitor) DetectAndEnqueue(ctx context.Context) (*EnqueueResult, error) {
	event, err := m.Detect(ctx)
	if err != nil {
		return nil, err
	}
	if event == nil {
		return nil, nil
	}

	m.logger.Info("disc detected, starting enqueue pipeline",
		"event_type", "disc_detected",
		"label", event.Label,
		"disc_type", event.DiscType,
		"device", event.Device,
	)

	return m.enqueuePipeline(ctx, event)
}

// ProbeDisc detects a loaded disc via lsblk and returns a DiscEvent.
// Returns nil if no disc is detected or lsblk reports no block devices.
func ProbeDisc(ctx context.Context, device string) (*DiscEvent, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	//nolint:gosec // device path is validated by caller
	cmd := exec.CommandContext(ctx, "lsblk", "--json", "-o", "NAME,LABEL,FSTYPE,MOUNTPOINT", device)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("lsblk probe failed for %s: %w", device, err)
	}

	dev, err := parseLsblk(out)
	if err != nil {
		return nil, fmt.Errorf("parse lsblk output for %s: %w", device, err)
	}

	return &DiscEvent{
		Device:    device,
		Label:     strings.TrimSpace(dev.Label),
		DiscType:  classifyDisc(dev.FSType),
		MountPath: dev.MountPoint,
	}, nil
}

// EjectDisc ejects the disc in the given device.
func EjectDisc(ctx context.Context, device string) error {
	//nolint:gosec // device path is validated by caller
	cmd := exec.CommandContext(ctx, "eject", device)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("eject %s: %w", device, err)
	}
	return nil
}

// ValidateLabel checks that a disc label is non-empty and contains printable
// characters. Labels that are all whitespace or control characters are rejected.
func ValidateLabel(label string) bool {
	if label == "" {
		return false
	}
	for _, r := range label {
		if r > ' ' && r != 0x7f {
			return true
		}
	}
	return false
}

// IsUnusableLabel returns true if the label is semantically useless for
// identification: empty, a known generic name, a known generic prefix, or
// all digits.
func IsUnusableLabel(label string) bool {
	trimmed := strings.TrimSpace(label)
	if trimmed == "" {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, g := range unusableLabels {
		if lower == g {
			return true
		}
	}
	for _, p := range unusablePrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return allDigitsRe.MatchString(trimmed)
}

// ExtractDiscNameFromVolumeID cleans a volume ID into a human-readable disc name.
func ExtractDiscNameFromVolumeID(volumeID string) string {
	s := leadingDigitPrefixRe.ReplaceAllString(volumeID, "")
	s = seasonDiscSuffixRe.ReplaceAllString(s, "")
	s = tvSuffixRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "_", " ")
	return strings.TrimSpace(s)
}

// shouldRefreshDiscTitle returns true if the disc title is empty or
// a placeholder that should be replaced when better metadata is available.
func shouldRefreshDiscTitle(title string) bool {
	trimmed := strings.TrimSpace(title)
	return trimmed == "" || strings.EqualFold(trimmed, "Unknown Disc")
}

// tryRefreshDiscTitle re-reads the disc label and updates the queue item's
// title if a better label is available. All failures are non-fatal.
// Called when a previously completed disc is re-inserted with a bad title.
func tryRefreshDiscTitle(ctx context.Context, store *queue.Store, item *queue.Item, device string, logger *slog.Logger) {
	event, err := ProbeDisc(ctx, device)
	if err != nil {
		logger.Warn("title refresh probe failed",
			"event_type", "title_refresh_failed",
			"error_hint", "disc probe for title refresh failed",
			"impact", "disc title may remain stale",
			"error", err,
			"item_id", item.ID,
		)
		return
	}
	if event == nil || IsUnusableLabel(event.Label) {
		return
	}
	name := ExtractDiscNameFromVolumeID(event.Label)
	if name == "" || name == item.DiscTitle {
		return
	}
	item.DiscTitle = name
	if err := store.Update(item); err != nil {
		logger.Warn("title refresh update failed",
			"event_type", "title_refresh_persist_failed",
			"error_hint", "failed to persist refreshed title",
			"impact", "title update lost",
			"error", err,
			"item_id", item.ID,
		)
		return
	}
	logger.Info("disc title refreshed on re-insertion",
		"decision_type", logs.DecisionTitleRefresh,
		"decision_result", "updated",
		"decision_reason", "better label available from re-inserted disc",
		"item_id", item.ID,
		"new_title", name,
	)
}
