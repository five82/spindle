package encoder

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/five82/drapto"
	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

// Handler implements stage.Handler for encoding.
type Handler struct {
	cfg      *config.Config
	store    *queue.Store
	notifier *notify.Notifier
}

// New creates an encoding handler.
func New(cfg *config.Config, store *queue.Store, notifier *notify.Notifier) *Handler {
	return &Handler{cfg: cfg, store: store, notifier: notifier}
}

// encodingJob describes a single file to encode.
type encodingJob struct {
	episodeKey string
	inputPath  string
}

// planJobs determines the encoding jobs from the envelope's ripped assets.
// Movies produce one job; TV produces one job per episode.
func planJobs(env *ripspec.Envelope) []encodingJob {
	var jobs []encodingJob
	for _, asset := range env.Assets.Ripped {
		if !asset.IsCompleted() {
			continue
		}
		jobs = append(jobs, encodingJob{
			episodeKey: asset.EpisodeKey,
			inputPath:  asset.Path,
		})
	}
	return jobs
}

// Run executes the encoding stage.
func (h *Handler) Run(ctx context.Context, item *queue.Item) error {
	logger := stage.LoggerFromContext(ctx)
	logger.Info("encoding stage started", "event_type", "stage_start", "stage", "encoding")

	env, err := stage.ParseRipSpec(item.RipSpecData)
	if err != nil {
		return err
	}

	stagingRoot, err := item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		return fmt.Errorf("staging root: %w", err)
	}
	encodedDir := filepath.Join(stagingRoot, "encoded")

	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		return fmt.Errorf("create encoded dir: %w", err)
	}

	jobs := planJobs(&env)
	if len(jobs) == 0 {
		return fmt.Errorf("no ripped assets to encode")
	}

	logger.Info("encoding plan",
		"decision_type", "encoding_plan",
		"decision_result", fmt.Sprintf("%d jobs", len(jobs)),
		"decision_reason", fmt.Sprintf("media_type=%s", env.Metadata.MediaType),
	)

	opts := []drapto.Option{drapto.WithPreset(drapto.PresetGrain)}
	if h.cfg.Encoding.SVTAV1Preset >= 0 && h.cfg.Encoding.SVTAV1Preset <= 13 {
		opts = append(opts, drapto.WithSVTAV1Preset(uint8(h.cfg.Encoding.SVTAV1Preset)))
		logger.Info("SVT-AV1 preset override applied",
			"decision_type", "encoding_config",
			"decision_result", fmt.Sprintf("preset %d", h.cfg.Encoding.SVTAV1Preset),
			"decision_reason", "config svt_av1_preset",
		)
	}
	encoder, err := drapto.New(opts...)
	if err != nil {
		return fmt.Errorf("create drapto encoder: %w", err)
	}

	var encodeErrors int
	for i, job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Resume: skip already-encoded assets.
		if existing, found := env.Assets.FindAsset("encoded", job.episodeKey); found && existing.IsCompleted() {
			logger.Info("skipping already-encoded asset",
				"decision_type", "encode_resume",
				"decision_result", "skipped",
				"decision_reason", "asset already completed",
				"episode_key", job.episodeKey,
			)
			continue
		}

		// Remove stale output from a previous run. The staging directory is
		// keyed by disc fingerprint, so a re-inserted disc reuses the same
		// encoded/ directory. Drapto refuses to overwrite existing files.
		expectedOutput := filepath.Join(encodedDir, filepath.Base(job.inputPath))
		if err := os.Remove(expectedOutput); err == nil {
			logger.Info("removed stale encoded file",
				"decision_type", "encode_cleanup",
				"decision_result", "removed",
				"decision_reason", "stale output from previous run",
				"path", expectedOutput,
			)
		}

		logger.Info(fmt.Sprintf("Phase %d/%d - Encoding %s", i+1, len(jobs), filepath.Base(job.inputPath)),
			"event_type", "encode_start",
			"episode_key", job.episodeKey,
		)

		item.ActiveEpisodeKey = job.episodeKey
		item.ProgressMessage = fmt.Sprintf("Phase %d/%d - Encoding %s", i+1, len(jobs), filepath.Base(job.inputPath))

		// Reset encoding snapshot and force-persist.
		var snap encodingstate.Snapshot
		snap.InputFile = filepath.Base(job.inputPath)
		snap.Substage = "initializing"

		// Probe input to populate initial snapshot fields.
		if probeResult, probeErr := ffprobe.Inspect(ctx, "", job.inputPath); probeErr == nil {
			for _, s := range probeResult.Streams {
				if s.CodecType == "video" {
					snap.Resolution = fmt.Sprintf("%dx%d", s.Width, s.Height)
					break
				}
			}
			snap.OriginalSize = probeResult.SizeBytes()
		}

		item.EncodingDetailsJSON = snap.Marshal()
		if err := h.store.UpdateProgress(item); err != nil {
			logger.Warn("failed to persist initial snapshot",
				"event_type", "progress_persist_error",
				"error_hint", err.Error(),
				"impact", "progress display may be stale",
			)
		}

		// Create progress reporter.
		reporter := newSpindleReporter(item, h.store, logger)

		// Encode.
		result, encErr := encoder.EncodeWithReporter(ctx, job.inputPath, encodedDir, reporter)
		if encErr != nil {
			logger.Error("encoding failed",
				"event_type", "encode_error",
				"error_hint", encErr.Error(),
				"error", encErr,
				"episode_key", job.episodeKey,
			)

			// Record failed asset, continue to next job (failure isolation).
			env.Assets.AddAsset("encoded", ripspec.Asset{
				EpisodeKey: job.episodeKey,
				Path:       "",
				Status:     "failed",
				ErrorMsg:   encErr.Error(),
			})
			encodeErrors++

			// Persist failure in snapshot (re-read from item to preserve reporter fields).
			snap, _ = encodingstate.Unmarshal(item.EncodingDetailsJSON)
			snap.Error = &encodingstate.Issue{
				Title:   "Encoding failed",
				Message: encErr.Error(),
			}
			item.EncodingDetailsJSON = snap.Marshal()
			if persistErr := h.store.UpdateProgress(item); persistErr != nil {
				logger.Warn("failed to persist error snapshot",
					"event_type", "progress_persist_error",
					"error_hint", persistErr.Error(),
					"impact", "error state not reflected in progress",
				)
			}
			continue
		}

		// Re-read snapshot from item (reporter callbacks kept it current).
		snap, _ = encodingstate.Unmarshal(item.EncodingDetailsJSON)
		snap.Substage = "complete"
		snap.Percent = 100
		snap.EncodedSize = int64(result.EncodedSize)
		snap.OriginalSize = int64(result.OriginalSize)
		snap.SizeReductionPercent = result.SizeReductionPercent
		snap.AverageSpeed = float64(result.EncodingSpeed)

		item.EncodingDetailsJSON = snap.Marshal()
		if err := h.store.UpdateProgress(item); err != nil {
			logger.Warn("failed to persist final snapshot",
				"event_type", "progress_persist_error",
				"error_hint", err.Error(),
				"impact", "final progress not reflected",
			)
		}

		// Add encoded asset to envelope.
		env.Assets.AddAsset("encoded", ripspec.Asset{
			EpisodeKey: job.episodeKey,
			Path:       result.OutputFile,
			Status:     "completed",
		})

		logger.Info("encoding completed",
			"event_type", "encode_complete",
			"episode_key", job.episodeKey,
			"size_reduction_percent", fmt.Sprintf("%.1f", result.SizeReductionPercent),
			"validation_passed", result.ValidationPassed,
		)

		if !result.ValidationPassed {
			item.AppendReviewReason(fmt.Sprintf("validation failed for %s", job.episodeKey))
		}
	}

	// Persist envelope.
	if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
		return err
	}

	if encodeErrors > 0 {
		return fmt.Errorf("encoding failed for %d of %d jobs", encodeErrors, len(jobs))
	}

	// Notification.
	if h.notifier != nil {
		_ = h.notifier.Send(ctx, notify.EventEncodeComplete,
			"Encode Complete",
			fmt.Sprintf("Encoded %s (%d files)", item.DiscTitle, len(jobs)),
		)
	}

	logger.Info("encoding stage completed", "event_type", "stage_complete", "stage", "encoding")
	return nil
}

// throttleInterval is the minimum interval between progress persists.
const throttleInterval = 2 * time.Second

// spindleReporter implements drapto.Reporter, adapting Drapto progress events
// into encodingstate.Snapshot updates on the queue item. Progress persistence
// is throttled to every 2 seconds.
type spindleReporter struct {
	item     *queue.Item
	store    *queue.Store
	logger   *slog.Logger
	lastPush time.Time
	now      func() time.Time // injectable clock for testing
}

func newSpindleReporter(item *queue.Item, store *queue.Store, logger *slog.Logger) *spindleReporter {
	return &spindleReporter{
		item:   item,
		store:  store,
		logger: logger,
		now:    time.Now,
	}
}

func (r *spindleReporter) EncodingProgress(p drapto.ProgressSnapshot) {
	now := r.now()
	if now.Sub(r.lastPush) < throttleInterval {
		return
	}
	r.lastPush = now

	snap, err := encodingstate.Unmarshal(r.item.EncodingDetailsJSON)
	if err != nil {
		snap = encodingstate.Snapshot{}
	}

	snap.Substage = "encoding"
	snap.Percent = float64(p.Percent)
	snap.FPS = float64(p.FPS)
	snap.AverageSpeed = float64(p.Speed)
	snap.ETASeconds = p.ETA.Seconds()
	snap.CurrentFrame = int64(p.CurrentFrame)
	snap.TotalFrames = int64(p.TotalFrames)

	r.item.EncodingDetailsJSON = snap.Marshal()
	r.item.ProgressPercent = float64(p.Percent)
	if err := r.store.UpdateProgress(r.item); err != nil {
		r.logger.Warn("failed to persist encoding progress",
			"event_type", "progress_persist_error",
			"error_hint", err.Error(),
			"impact", "progress display may be stale",
		)
	}
}

func (r *spindleReporter) EncodingStarted(totalFrames uint64) {
	snap, err := encodingstate.Unmarshal(r.item.EncodingDetailsJSON)
	if err != nil {
		snap = encodingstate.Snapshot{}
	}
	snap.Substage = "encoding"
	snap.TotalFrames = int64(totalFrames)
	r.item.EncodingDetailsJSON = snap.Marshal()
	if err := r.store.UpdateProgress(r.item); err != nil {
		r.logger.Warn("failed to persist encoding started",
			"event_type", "progress_persist_error",
			"error_hint", err.Error(),
			"impact", "total frames not persisted",
		)
	}
}

func (r *spindleReporter) Initialization(s drapto.InitializationSummary) {
	snap, err := encodingstate.Unmarshal(r.item.EncodingDetailsJSON)
	if err != nil {
		snap = encodingstate.Snapshot{}
	}
	snap.InputFile = s.InputFile
	snap.Resolution = s.Resolution
	snap.DynamicRange = s.DynamicRange
	snap.Substage = "initializing"
	r.item.EncodingDetailsJSON = snap.Marshal()
	_ = r.store.UpdateProgress(r.item)
}

func (r *spindleReporter) EncodingConfig(s drapto.EncodingConfigSummary) {
	snap, err := encodingstate.Unmarshal(r.item.EncodingDetailsJSON)
	if err != nil {
		snap = encodingstate.Snapshot{}
	}
	snap.Encoder = s.Encoder
	snap.Preset = s.Preset
	snap.Quality = s.Quality
	snap.Tune = s.Tune
	snap.AudioCodec = s.AudioCodec
	snap.DraptoPreset = s.DraptoPreset
	snap.Substage = "configuring"
	r.item.EncodingDetailsJSON = snap.Marshal()
	_ = r.store.UpdateProgress(r.item)
}

func (r *spindleReporter) CropResult(s drapto.CropSummary) {
	snap, err := encodingstate.Unmarshal(r.item.EncodingDetailsJSON)
	if err != nil {
		snap = encodingstate.Snapshot{}
	}
	snap.CropFilter = s.Crop
	snap.CropRequired = s.Required
	snap.CropMessage = s.Message
	snap.Substage = "crop_detection"
	if s.Required {
		if w, h, parseErr := encodingstate.ParseCropFilter(s.Crop); parseErr == nil {
			snap.Resolution = fmt.Sprintf("%dx%d", w, h)
		}
	}
	r.item.EncodingDetailsJSON = snap.Marshal()
	_ = r.store.UpdateProgress(r.item)
}

func (r *spindleReporter) ValidationComplete(s drapto.ValidationSummary) {
	snap, err := encodingstate.Unmarshal(r.item.EncodingDetailsJSON)
	if err != nil {
		snap = encodingstate.Snapshot{}
	}
	snap.Substage = "validation"
	steps := make([]encodingstate.ValidationStep, len(s.Steps))
	for i, step := range s.Steps {
		steps[i] = encodingstate.ValidationStep{
			Name:    step.Name,
			Passed:  step.Passed,
			Details: step.Details,
		}
	}
	snap.Validation = &encodingstate.Validation{
		Passed: s.Passed,
		Steps:  steps,
	}
	r.item.EncodingDetailsJSON = snap.Marshal()
	_ = r.store.UpdateProgress(r.item)
}

func (r *spindleReporter) EncodingComplete(s drapto.EncodingOutcome) {
	snap, err := encodingstate.Unmarshal(r.item.EncodingDetailsJSON)
	if err != nil {
		snap = encodingstate.Snapshot{}
	}
	snap.Substage = "complete"
	snap.Percent = 100
	snap.EncodedSize = int64(s.EncodedSize)
	snap.OriginalSize = int64(s.OriginalSize)
	snap.AverageSpeed = float64(s.AverageSpeed)
	snap.EncodeDurationSeconds = s.TotalTime.Seconds()
	r.item.EncodingDetailsJSON = snap.Marshal()
	_ = r.store.UpdateProgress(r.item)
}

func (r *spindleReporter) Warning(message string) {
	snap, err := encodingstate.Unmarshal(r.item.EncodingDetailsJSON)
	if err != nil {
		snap = encodingstate.Snapshot{}
	}
	snap.Warning = message
	r.item.EncodingDetailsJSON = snap.Marshal()
	_ = r.store.UpdateProgress(r.item)

	r.logger.Warn("drapto warning",
		"event_type", "drapto_warning",
		"error_hint", message,
		"impact", "encoding may produce suboptimal results",
	)
}

func (r *spindleReporter) Error(e drapto.ReporterError) {
	snap, err := encodingstate.Unmarshal(r.item.EncodingDetailsJSON)
	if err != nil {
		snap = encodingstate.Snapshot{}
	}
	snap.Error = &encodingstate.Issue{
		Title:      e.Title,
		Message:    e.Message,
		Context:    e.Context,
		Suggestion: e.Suggestion,
	}
	r.item.EncodingDetailsJSON = snap.Marshal()
	_ = r.store.UpdateProgress(r.item)
}

// No-op methods for Reporter interface methods we don't need.
func (r *spindleReporter) Hardware(drapto.HardwareSummary)             {}
func (r *spindleReporter) StageProgress(drapto.StageProgress)          {}
func (r *spindleReporter) OperationComplete(string)                    {}
func (r *spindleReporter) BatchStarted(drapto.BatchStartInfo)          {}
func (r *spindleReporter) FileProgress(drapto.FileProgressContext)     {}
func (r *spindleReporter) BatchComplete(drapto.BatchSummary)           {}
