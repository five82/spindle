package encoding

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/encodingstate"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripcache"
	"spindle/internal/ripping"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/services/drapto"
	"spindle/internal/stage"
)

// Encoder manages Drapto encoding of ripped files.
type Encoder struct {
	store            *queue.Store
	cfg              *config.Config
	logger           *slog.Logger
	client           drapto.Client
	notifier         notifications.Service
	cache            *ripcache.Manager
	presetClassifier presetClassifier
	runner           *draptoRunner
	planner          encodePlanner
	jobRunner        *encodeJobRunner
}

// NewEncoder constructs the encoding handler.
func NewEncoder(cfg *config.Config, store *queue.Store, logger *slog.Logger) *Encoder {
	client := drapto.NewCLI(
		drapto.WithBinary(cfg.DraptoBinary()),
	)
	return NewEncoderWithDependencies(cfg, store, logger, client, notifications.NewService(cfg))
}

// NewEncoderWithDependencies allows injecting custom dependencies (used for tests).
func NewEncoderWithDependencies(cfg *config.Config, store *queue.Store, logger *slog.Logger, client drapto.Client, notifier notifications.Service) *Encoder {
	enc := &Encoder{
		store:    store,
		cfg:      cfg,
		client:   client,
		notifier: notifier,
		cache:    ripcache.NewManager(cfg, logger),
	}
	enc.runner = newDraptoRunner(cfg, client, store)
	if cfg != nil && cfg.PresetDecider.Enabled {
		enc.presetClassifier = newPresetLLMClassifier(cfg)
	}
	enc.planner = newEncodePlanner(enc.selectPreset)
	enc.SetLogger(logger)
	return enc
}

// SetLogger updates the encoder's logging destination while preserving component labeling.
func (e *Encoder) SetLogger(logger *slog.Logger) {
	e.logger = logging.NewComponentLogger(logger, "encoder")
	if e.cache != nil {
		e.cache.SetLogger(logger)
	}
}

func (e *Encoder) Prepare(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, e.logger)
	item.InitProgress("Encoding", "Starting Drapto encoding")
	item.DraptoPresetProfile = ""
	logger.Debug("starting encoding preparation")
	return nil
}

func (e *Encoder) Execute(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, e.logger)
	stageStart := time.Now()

	env, err := e.validateAndParseInputs(ctx, item, logger)
	if err != nil {
		return err
	}

	stagingRoot, encodedDir, err := e.prepareEncodedDirectory(ctx, item, logger)
	if err != nil {
		return err
	}

	planner := e.ensurePlanner()
	jobs, decision, err := planner.Plan(ctx, item, env, encodedDir, logger)
	if err != nil {
		return err
	}

	encodedPaths, err := e.runEncodingJobs(ctx, item, env, jobs, decision, stagingRoot, encodedDir, logger)
	if err != nil {
		return err
	}

	// Enforce Drapto's internal validation results
	if err := e.enforceDraptoValidation(ctx, item, logger); err != nil {
		return err
	}

	e.finalizeEncodedItem(item, env, encodedPaths, decision, logger)
	e.reportEncodingSummary(ctx, item, encodedPaths, stageStart, decision, logger)

	return nil
}

// ensurePlanner returns the planner, initializing it lazily if needed.
// Tests may set e.planner directly to inject a stub implementation.
func (e *Encoder) ensurePlanner() encodePlanner {
	if e.planner == nil {
		e.planner = newEncodePlanner(e.selectPreset)
	}
	return e.planner
}

// ensureRunner returns the Drapto runner, initializing it lazily if needed.
// Tests may set e.runner directly to inject a stub implementation.
func (e *Encoder) ensureRunner() *draptoRunner {
	if e.runner == nil {
		e.runner = newDraptoRunner(e.cfg, e.client, e.store)
	}
	return e.runner
}

// ensureJobRunner returns the job runner, initializing it lazily if needed.
// Tests may set e.jobRunner directly to inject a stub implementation.
func (e *Encoder) ensureJobRunner() *encodeJobRunner {
	if e.jobRunner == nil {
		e.jobRunner = newEncodeJobRunner(e.store, e.ensureRunner())
	}
	return e.jobRunner
}

// validateAndParseInputs parses the rip spec and ensures ripped files are available.
func (e *Encoder) validateAndParseInputs(ctx context.Context, item *queue.Item, logger *slog.Logger) (ripspec.Envelope, error) {
	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		return ripspec.Envelope{}, services.Wrap(
			services.ErrValidation,
			"encoding",
			"parse rip spec",
			"Rip specification missing or invalid; rerun identification",
			err,
		)
	}

	logger.Debug("starting encoding")
	if strings.TrimSpace(item.RippedFile) == "" {
		return ripspec.Envelope{}, services.Wrap(
			services.ErrValidation,
			"encoding",
			"validate inputs",
			"No ripped file available for encoding; ensure the ripping stage completed successfully",
			nil,
		)
	}

	if err := e.restoreFromCacheIfNeeded(ctx, item, env, logger); err != nil {
		return ripspec.Envelope{}, err
	}

	return env, nil
}

// restoreFromCacheIfNeeded checks if ripped files need to be restored from cache
// and performs the restore operation if necessary.
func (e *Encoder) restoreFromCacheIfNeeded(ctx context.Context, item *queue.Item, env ripspec.Envelope, logger *slog.Logger) error {
	cacheEnabled := e.cfg != nil && e.cfg.RipCache.Enabled && e.cache != nil
	if !cacheEnabled {
		logger.Debug(
			"rip cache decision",
			logging.String(logging.FieldDecisionType, "rip_cache_restore"),
			logging.String("decision_result", "skip"),
			logging.String("decision_reason", "cache_disabled"),
			logging.String("decision_options", "restore, skip"),
		)
		return nil
	}

	ripDir := filepath.Dir(strings.TrimSpace(item.RippedFile))
	if fileExists(item.RippedFile) {
		logger.Debug(
			"rip cache decision",
			logging.String(logging.FieldDecisionType, "rip_cache_restore"),
			logging.String("decision_result", "skip"),
			logging.String("decision_reason", "ripped_file_present"),
			logging.String("decision_options", "restore, skip"),
			logging.String("rip_dir", ripDir),
		)
		return nil
	}

	logger.Debug(
		"rip cache decision",
		logging.String(logging.FieldDecisionType, "rip_cache_restore"),
		logging.String("decision_result", "restore"),
		logging.String("decision_reason", "ripped_file_missing"),
		logging.String("decision_options", "restore, skip"),
		logging.String("rip_dir", ripDir),
	)

	restored, err := e.cache.Restore(ctx, item, ripDir)
	if err != nil {
		return services.Wrap(
			services.ErrTransient,
			"encoding",
			"restore rip cache",
			"Failed to restore ripped files from cache; check cache path and permissions",
			err,
		)
	}

	logger.Debug(
		"rip cache restore result",
		logging.String(logging.FieldDecisionType, "rip_cache_restore"),
		logging.String("decision_result", ternary(restored, "cache_hit", "cache_miss")),
		logging.String("decision_reason", ternary(restored, "restored_from_cache", "no_cache_entry_or_target_not_empty")),
		logging.String("decision_options", "cache_hit, cache_miss"),
		logging.String("rip_dir", ripDir),
	)

	if restored {
		logger.Debug(
			"audio refinement decision",
			logging.String(logging.FieldDecisionType, "audio_refine"),
			logging.String("decision_result", "refine"),
			logging.String("decision_reason", "rip_cache_restore"),
			logging.String("decision_options", "refine, skip"),
		)
		paths := rippedPaths(item, env)
		if _, err := ripping.RefineAudioTargets(ctx, e.cfg, logger, paths); err != nil {
			return services.Wrap(
				services.ErrExternalTool,
				"encoding",
				"refine audio tracks",
				"Failed to optimize ripped audio tracks after cache restore",
				err,
			)
		}
	}

	return nil
}

func rippedPaths(item *queue.Item, env ripspec.Envelope) []string {
	paths := make([]string, 0)
	if len(env.Episodes) > 0 {
		for _, episode := range env.Episodes {
			asset, ok := env.Assets.FindAsset("ripped", episode.Key)
			if !ok {
				continue
			}
			if value := strings.TrimSpace(asset.Path); value != "" {
				paths = append(paths, value)
			}
		}
	}
	if len(paths) == 0 && item != nil {
		if value := strings.TrimSpace(item.RippedFile); value != "" {
			paths = append(paths, value)
		}
	}
	return paths
}

// prepareEncodedDirectory creates a clean output directory for encoded files.
func (e *Encoder) prepareEncodedDirectory(ctx context.Context, item *queue.Item, logger *slog.Logger) (stagingRoot, encodedDir string, err error) {
	stagingRoot = item.StagingRoot(e.cfg.Paths.StagingDir)
	if stagingRoot == "" {
		stagingRoot = filepath.Join(strings.TrimSpace(e.cfg.Paths.StagingDir), fmt.Sprintf("queue-%d", item.ID))
	}
	encodedDir = filepath.Join(stagingRoot, "encoded")
	if err := e.cleanupEncodedDir(logger, encodedDir); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		return "", "", services.Wrap(
			services.ErrConfiguration,
			"encoding",
			"ensure encoded dir",
			"Failed to create encoded directory; set staging_dir to a writable path",
			err,
		)
	}
	logger.Debug("prepared encoding directory", logging.String("encoded_dir", encodedDir))
	return stagingRoot, encodedDir, nil
}

// runEncodingJobs encodes all jobs (episodes or single file) and returns the output paths and sources used.
func (e *Encoder) runEncodingJobs(ctx context.Context, item *queue.Item, env ripspec.Envelope, jobs []encodeJob, decision presetDecision, stagingRoot, encodedDir string, logger *slog.Logger) ([]string, error) {
	return e.ensureJobRunner().Run(ctx, item, env, jobs, decision, stagingRoot, encodedDir, logger)
}

// finalizeEncodedItem updates the queue item with encoding results.
func (e *Encoder) finalizeEncodedItem(item *queue.Item, env ripspec.Envelope, encodedPaths []string, decision presetDecision, logger *slog.Logger) {
	if encoded, err := env.Encode(); err == nil {
		item.RipSpecData = encoded
	} else {
		logger.Warn("failed to encode rip spec after encoding; metadata may be stale",
			logging.Error(err),
			logging.String(logging.FieldEventType, "rip_spec_encode_failed"),
			logging.String(logging.FieldErrorHint, "rerun identification if rip spec data looks wrong"),
			logging.String(logging.FieldImpact, "encoding metadata may not reflect latest state"),
		)
	}

	item.EncodedFile = encodedPaths[0]
	item.ProgressStage = "Encoded"
	item.ProgressPercent = 100
	item.ActiveEpisodeKey = ""

	if len(encodedPaths) > 1 {
		item.ProgressMessage = fmt.Sprintf("Encoding completed (%d episodes)", len(encodedPaths))
	} else if e.client != nil {
		item.ProgressMessage = "Encoding completed"
	} else {
		item.ProgressMessage = "Encoded placeholder artifact"
	}

	if suffix := presetSummary(decision); suffix != "" {
		item.ProgressMessage = fmt.Sprintf("%s – %s", item.ProgressMessage, suffix)
	}
}

// reportEncodingSummary calculates metrics, sends notifications, and logs the summary.
func (e *Encoder) reportEncodingSummary(ctx context.Context, item *queue.Item, encodedPaths []string, stageStart time.Time, decision presetDecision, logger *slog.Logger) {
	var totalInputBytes, totalOutputBytes int64
	for _, path := range encodedPaths {
		if info, err := os.Stat(path); err == nil {
			totalOutputBytes += info.Size()
		}
	}
	if info, err := os.Stat(strings.TrimSpace(item.RippedFile)); err == nil {
		totalInputBytes = info.Size()
	}

	var compressionRatio float64
	if totalInputBytes > 0 {
		compressionRatio = float64(totalOutputBytes) / float64(totalInputBytes) * 100
	}

	if e.notifier != nil {
		if err := e.notifier.Publish(ctx, notifications.EventEncodingCompleted, notifications.Payload{
			"discTitle":   item.DiscTitle,
			"placeholder": e.client == nil,
			"ratio":       compressionRatio,
			"inputBytes":  totalInputBytes,
			"outputBytes": totalOutputBytes,
			"files":       len(encodedPaths),
			"preset":      strings.TrimSpace(item.DraptoPresetProfile),
		}); err != nil {
			logger.Debug("encoding notification failed", logging.Error(err))
		}

		// Check for validation failures and notify
		e.notifyValidationFailures(ctx, item, logger)
	}

	summaryAttrs := []logging.Attr{
		logging.String(logging.FieldEventType, "stage_complete"),
		logging.String("encoded_file", item.EncodedFile),
		logging.Duration("stage_duration", time.Since(stageStart)),
		logging.Int64("input_bytes", totalInputBytes),
		logging.Int64("output_bytes", totalOutputBytes),
		logging.Float64("compression_ratio_percent", compressionRatio),
		logging.Int("files_encoded", len(encodedPaths)),
		logging.String("preset_profile", strings.TrimSpace(item.DraptoPresetProfile)),
	}
	logger.Info("encoding stage summary", logging.Args(summaryAttrs...)...)
}

// notifyValidationFailures checks the encoding snapshot for validation failures
// and sends a notification if any validation steps failed.
func (e *Encoder) notifyValidationFailures(ctx context.Context, item *queue.Item, logger *slog.Logger) {
	if e.notifier == nil || item == nil {
		return
	}

	snapshot, err := encodingstate.Unmarshal(item.EncodingDetailsJSON)
	if err != nil {
		logger.Debug("failed to unmarshal encoding snapshot for validation check", logging.Error(err))
		return
	}

	if snapshot.Validation == nil || snapshot.Validation.Passed {
		return
	}

	// Collect failed step names
	var failedNames []string
	for _, step := range snapshot.Validation.Steps {
		if !step.Passed {
			failedNames = append(failedNames, strings.TrimSpace(step.Name))
		}
	}

	if err := e.notifier.Publish(ctx, notifications.EventValidationFailed, notifications.Payload{
		"discTitle":   item.DiscTitle,
		"failedSteps": len(failedNames),
		"totalSteps":  len(snapshot.Validation.Steps),
		"failedNames": strings.Join(failedNames, ", "),
	}); err != nil {
		logger.Debug("validation failure notification failed", logging.Error(err))
	}
}

func (e *Encoder) cleanupEncodedDir(logger *slog.Logger, encodedDir string) error {
	encodedDir = strings.TrimSpace(encodedDir)
	if encodedDir == "" {
		return nil
	}
	info, err := os.Stat(encodedDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return services.Wrap(
			services.ErrConfiguration,
			"encoding",
			"inspect encoded dir",
			"Failed to inspect previous encoded artifacts",
			err,
		)
	}
	if !info.IsDir() {
		return services.Wrap(
			services.ErrConfiguration,
			"encoding",
			"inspect encoded dir",
			fmt.Sprintf("Expected encoded path %q to be a directory", encodedDir),
			nil,
		)
	}
	if err := os.RemoveAll(encodedDir); err != nil {
		return services.Wrap(
			services.ErrConfiguration,
			"encoding",
			"remove stale artifacts",
			"Failed to remove previous encoded outputs", err,
		)
	}
	if logger != nil {
		logger.Debug("removed stale encoded artifacts", logging.String("encoded_dir", encodedDir))
	}
	return nil
}

// HealthCheck verifies encoding dependencies for Drapto.
func (e *Encoder) HealthCheck(ctx context.Context) stage.Health {
	const name = "encoder"
	if e.cfg == nil {
		return stage.Unhealthy(name, "configuration unavailable")
	}
	if strings.TrimSpace(e.cfg.Paths.StagingDir) == "" {
		return stage.Unhealthy(name, "staging directory not configured")
	}
	if e.client == nil {
		return stage.Unhealthy(name, "drapto client unavailable")
	}
	binary := strings.TrimSpace(e.cfg.DraptoBinary())
	if binary == "" {
		return stage.Unhealthy(name, "drapto binary not configured")
	}
	if _, err := exec.LookPath(binary); err != nil {
		return stage.Unhealthy(name, fmt.Sprintf("drapto binary %q not found", binary))
	}
	return stage.Healthy(name)
}

// enforceDraptoValidation checks Drapto's internal validation results and fails
// the encoding stage if any validation steps failed. Drapto validates codec,
// duration, HDR, audio, and A/V sync - we enforce those results here rather
// than duplicating the checks.
func (e *Encoder) enforceDraptoValidation(ctx context.Context, item *queue.Item, logger *slog.Logger) error {
	if e.cfg != nil && !e.cfg.Validation.EnforceDraptoValidation {
		logger.Debug("drapto validation enforcement disabled")
		return nil
	}

	snapshot, err := encodingstate.Unmarshal(item.EncodingDetailsJSON)
	if err != nil {
		logger.Debug("failed to unmarshal encoding snapshot for validation", logging.Error(err))
		return nil // No snapshot = can't validate, continue
	}

	if snapshot.Validation == nil {
		logger.Debug("drapto did not report validation results")
		return nil // Drapto didn't report validation
	}

	if snapshot.Validation.Passed {
		logger.Debug("drapto validation passed",
			logging.Int("steps", len(snapshot.Validation.Steps)),
		)
		return nil
	}

	// Collect failed step details
	var failures []string
	for _, step := range snapshot.Validation.Steps {
		if !step.Passed {
			detail := strings.TrimSpace(step.Name)
			if step.Details != "" {
				detail = fmt.Sprintf("%s: %s", step.Name, step.Details)
			}
			failures = append(failures, detail)
		}
	}

	logger.Error("drapto validation failed",
		logging.Int("failed_steps", len(failures)),
		logging.Int("total_steps", len(snapshot.Validation.Steps)),
		logging.String("failures", strings.Join(failures, "; ")),
	)

	return services.Wrap(
		services.ErrValidation,
		"encoding",
		"drapto validation",
		fmt.Sprintf("Drapto validation failed (%d/%d steps): %s",
			len(failures), len(snapshot.Validation.Steps), strings.Join(failures, "; ")),
		nil,
	)
}
