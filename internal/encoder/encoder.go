package encoder

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/five82/reel"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

// Handler implements stage.Handler for encoding.
type Handler struct {
	cfg      *config.Config
	notifier *notify.Notifier
}

// New creates an encoding handler.
func New(cfg *config.Config, notifier *notify.Notifier) *Handler {
	return &Handler{cfg: cfg, notifier: notifier}
}

// Run executes the encoding stage.
func (h *Handler) Run(ctx context.Context, sess *stage.Session) error {
	item := sess.Item
	logger := sess.Logger
	logger.Debug("encoding stage started", "event_type", "stage_start", "stage", "encoding")
	env := sess.Env

	stagingRoot, err := item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		return fmt.Errorf("staging root: %w", err)
	}
	encodedDir := filepath.Join(stagingRoot, "encoded")

	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		return fmt.Errorf("create encoded dir: %w", err)
	}

	logger.Info("Reel target-quality mode selected",
		"decision_type", logs.DecisionEncodingConfig,
		"decision_result", "target",
		"decision_reason", "spindle always uses Reel target-quality mode; encodes run in per-file worker subprocesses",
	)

	logger.Info("encoding plan",
		"decision_type", logs.DecisionEncodingPlan,
		"decision_result", "streaming",
		"decision_reason", fmt.Sprintf("media_type=%s; encode ripped assets as they land, ripping owns item progress while active", env.Metadata.MediaType),
	)

	// This stage starts alongside ripping and consumes each completed asset
	// as the ripper's merge save lands. It waits when none are pending and
	// finishes once the ripping task is terminal. attemptedKeys preserves
	// run-once semantics per asset: a failed encode must not retry every
	// poll (PendingKeyedAssetJobs treats failed outputs as pending).
	var summary encodeSummary
	attempted := 0
	attemptedKeys := make(map[string]bool)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := sess.RefreshEnvelope(); err != nil {
			return err
		}
		pending, _ := sess.PendingKeyedAssetJobs(ripspec.AssetKindRipped, ripspec.AssetKindEncoded)
		jobs := pending[:0:0]
		for _, job := range pending {
			if !attemptedKeys[job.Key] {
				jobs = append(jobs, job)
			}
		}

		ripping, err := h.rippingActive(sess)
		if err != nil {
			return err
		}

		if len(jobs) == 0 {
			if !ripping {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(encodeStreamPollInterval):
			}
			continue
		}

		for _, job := range jobs {
			attemptedKeys[job.Key] = true
		}
		attempted += len(jobs)
		batch, err := h.encodeJobs(ctx, sess, encodedDir, jobs)
		summary.errors += batch.errors
		summary.originalSize += batch.originalSize
		summary.encodedSize += batch.encodedSize
		if err != nil {
			return err
		}
	}

	// No whole-envelope Save here: encoding runs concurrently with the
	// analysis branch, and every envelope write in this stage already
	// persisted through the merge-based SaveAsset helpers. A plain Save
	// would clobber the sibling branch's merged state.

	if attempted == 0 {
		return fmt.Errorf("no ripped assets to encode")
	}
	if summary.errors > 0 {
		return fmt.Errorf("encoding failed for %d of %d jobs", summary.errors, attempted)
	}

	// Notification.
	snap, _ := encodingstate.Unmarshal(item.EncodingDetailsJSON)
	msg := fmt.Sprintf("Encoded %s (%d files", item.DisplayTitle(), attempted)
	if snap.Resolution != "" {
		msg += ", " + snap.Resolution
	}
	if summary.originalSize > 0 {
		reduction := (1 - float64(summary.encodedSize)/float64(summary.originalSize)) * 100
		msg += fmt.Sprintf(", %.1f%% smaller", reduction)
	}
	msg += ")"
	msg += queue.FormatAlsoProcessing(sess.Store, item.ID)
	_ = notify.SendLogged(ctx, h.notifier, logger, notify.EventEncodeComplete,
		"Encode Complete: "+item.DisplayTitle(),
		msg,
	)

	logger.Debug("encoding stage completed",
		"event_type", "stage_complete",
		"stage", "encoding",
		"jobs", attempted,
		"encoded_size_bytes", summary.encodedSize,
		"original_size_bytes", summary.originalSize,
	)
	return nil
}

const encodeStreamPollInterval = 10 * time.Second

// rippingActive reports whether the item's ripping task is still pending or
// running. Absent task rows (e.g. recompilation windows) read as inactive so
// the streaming loop cannot deadlock waiting for rips that will never come.
func (h *Handler) rippingActive(sess *stage.Session) (bool, error) {
	tasks, err := sess.Store.TasksForItem(sess.Item.ID)
	if err != nil {
		return false, fmt.Errorf("ripping task state: %w", err)
	}
	for _, t := range tasks {
		if t.Type == queue.StageRipping {
			return t.State == queue.TaskPending || t.State == queue.TaskRunning, nil
		}
	}
	return false, nil
}

type encodeSummary struct {
	errors       int
	originalSize int64
	encodedSize  int64
}

type encodeJobResult struct {
	failed       bool
	originalSize int64
	encodedSize  int64
}

func (h *Handler) encodeJobs(ctx context.Context, sess *stage.Session, encodedDir string, jobs []stage.AssetJob) (encodeSummary, error) {
	logger := sess.Logger
	env := sess.Env
	var summary encodeSummary

	for _, job := range jobs {
		if ctx.Err() != nil {
			return summary, ctx.Err()
		}

		if existing, found := env.Assets.FindAsset(ripspec.AssetKindEncoded, job.Key); found && existing.IsCompleted() {
			logger.Info("skipping already-encoded asset",
				"decision_type", logs.DecisionEncodeResume,
				"decision_result", "skipped",
				"decision_reason", "asset already completed",
				"episode_key", job.Key,
			)
			continue
		}

		result, err := h.encodeJob(ctx, sess, encodedDir, job)
		if err != nil {
			return summary, err
		}
		if result.failed {
			summary.errors++
			continue
		}
		summary.originalSize += result.originalSize
		summary.encodedSize += result.encodedSize
	}
	return summary, nil
}

// persistProgress calls sess.Progress and warns on failure, using the
// progress_persist_error shape shared by the initial/error/final snapshot
// persists in encodeJob, handleEncodeFailure, and handleEncodeSuccess.
func persistProgress(logger *slog.Logger, sess *stage.Session, percent float64, message, warnMsg, impact string, opts ...stage.ProgressOption) {
	if err := sess.Progress(percent, message, opts...); err != nil {
		logger.Warn(warnMsg,
			"event_type", "progress_persist_error",
			"error_hint", err.Error(),
			"impact", impact,
		)
	}
}

func (h *Handler) encodeJob(ctx context.Context, sess *stage.Session, encodedDir string, job stage.AssetJob) (encodeJobResult, error) {
	item := sess.Item
	logger := sess.Logger

	// Remove stale output from a previous run. The staging directory is
	// keyed by disc fingerprint, so a re-inserted disc reuses the same
	// encoded/ directory. Reel skips outputs that already exist.
	expectedOutput := filepath.Join(encodedDir, filepath.Base(job.Input.Path))
	if err := os.Remove(expectedOutput); err == nil {
		logger.Info("removed stale encoded file",
			"decision_type", logs.DecisionEncodeCleanup,
			"decision_result", "removed",
			"decision_reason", "stale output from previous run",
			"path", expectedOutput,
		)
	}

	message := job.PhaseMessage("Encoding " + filepath.Base(job.Input.Path))
	logger.Info(message,
		"event_type", "encode_start",
		"episode_key", job.Key,
	)
	_ = sess.Progress(job.Percent(0), message, stage.WithActiveEpisode(job.Key))

	// Reset encoding snapshot and force-persist.
	snap := h.initialEncodingSnapshot(ctx, logger, job)
	item.EncodingDetailsJSON = snap.Marshal()
	persistProgress(logger, sess, sess.Task.ProgressPercent, sess.Task.ProgressMessage,
		"failed to persist initial snapshot", "progress display may be stale",
		stage.WithEncodingDetails(item.EncodingDetailsJSON))

	reporter := newSpindleReporter(sess, logger, job.Key, job.ProgressIndex, job.ProgressTotal)
	result, encErr := runWorkerProcess(ctx, logger, job.Input.Path, encodedDir, reporter)
	if encErr != nil {
		return encodeJobResult{failed: true}, h.handleEncodeFailure(logger, sess, job, encErr)
	}

	return h.handleEncodeSuccess(logger, sess, job, result)
}

func (h *Handler) initialEncodingSnapshot(ctx context.Context, logger *slog.Logger, job stage.AssetJob) encodingstate.Snapshot {
	snap := encodingstate.Snapshot{
		InputFile: filepath.Base(job.Input.Path),
		Substage:  "initializing",
	}

	probeResult, probeErr := ffprobe.Inspect(ctx, "", job.Input.Path)
	if probeErr != nil {
		logger.Warn("input probe failed",
			"event_type", "probe_error",
			"error_hint", probeErr.Error(),
			"impact", "encode proceeds without probe metadata",
			"episode_key", job.Key,
		)
		return snap
	}

	var resolution string
	var codecs []string
	for _, s := range probeResult.Streams {
		if s.CodecType == "video" && resolution == "" {
			resolution = fmt.Sprintf("%dx%d", s.Width, s.Height)
			snap.Resolution = resolution
		}
		if s.CodecName != "" {
			codecs = append(codecs, s.CodecName)
		}
	}
	snap.OriginalSize = probeResult.SizeBytes()

	logger.Info("input file probed",
		"decision_type", logs.DecisionFileProbe,
		"decision_result", "success",
		"resolution", resolution,
		"codecs", strings.Join(codecs, ","),
		"original_size_bytes", snap.OriginalSize,
		"episode_key", job.Key,
	)
	return snap
}

func (h *Handler) handleEncodeFailure(logger *slog.Logger, sess *stage.Session, job stage.AssetJob, encErr error) error {
	logger.Error("encoding failed",
		"event_type", "encode_error",
		"error_hint", encErr.Error(),
		"error", encErr,
		"episode_key", job.Key,
	)

	item := sess.Item
	snap, _ := encodingstate.Unmarshal(item.EncodingDetailsJSON)
	snap.Error = &encodingstate.Issue{
		Title:   "Encoding failed",
		Message: encErr.Error(),
	}
	item.EncodingDetailsJSON = snap.Marshal()
	persistProgress(logger, sess, job.CompletionPercent(), sess.Task.ProgressMessage,
		"failed to persist error snapshot", "error state not reflected in progress",
		stage.WithEncodingDetails(item.EncodingDetailsJSON))
	return sess.SaveAssetFailure(ripspec.AssetKindEncoded, job.Key, encErr.Error())
}

func (h *Handler) handleEncodeSuccess(logger *slog.Logger, sess *stage.Session, job stage.AssetJob, result *reel.Result) (encodeJobResult, error) {
	item := sess.Item
	snap, _ := encodingstate.Unmarshal(item.EncodingDetailsJSON)
	snap.Substage = "complete"
	snap.Percent = 100
	snap.EncodedSize = int64(result.EncodedSize)
	snap.OriginalSize = int64(result.OriginalSize)
	snap.SizeReductionPercent = result.SizeReductionPercent
	snap.AverageSpeed = float64(result.EncodingSpeed)

	item.EncodingDetailsJSON = snap.Marshal()
	persistProgress(logger, sess, job.CompletionPercent(), sess.Task.ProgressMessage,
		"failed to persist final snapshot", "final progress not reflected",
		stage.WithEncodingDetails(item.EncodingDetailsJSON))

	if err := sess.SaveAssetSuccess(ripspec.AssetKindEncoded, ripspec.Asset{
		EpisodeKey: job.Key,
		Path:       result.OutputFile,
	}); err != nil {
		return encodeJobResult{}, err
	}

	logger.Info("encoding completed",
		"event_type", "encode_complete",
		"episode_key", job.Key,
		"size_reduction_percent", fmt.Sprintf("%.1f", result.SizeReductionPercent),
		"validation_passed", result.ValidationPassed,
	)

	if !result.ValidationPassed {
		// Merge-based review flags: this stage runs concurrently with the
		// analysis branch and no longer performs a whole-envelope Save.
		if mergeErr := sess.MergeSave(func(env *ripspec.Envelope) error {
			if ep := env.EpisodeByKey(job.Key); ep != nil {
				ep.AppendReviewReason("Encoding validation failed")
			}
			return nil
		}); mergeErr != nil {
			return encodeJobResult{}, mergeErr
		}
		if mergeErr := sess.MergeAddReviewReason(fmt.Sprintf("validation failed for %s", job.Key)); mergeErr != nil {
			return encodeJobResult{}, mergeErr
		}
		logger.Info("validation failure flagged for review",
			"decision_type", logs.DecisionValidationFailureRoute,
			"decision_result", "flagged_for_review",
			"decision_reason", "encoding validation did not pass",
			"episode_key", job.Key,
		)
	}

	return encodeJobResult{
		originalSize: int64(result.OriginalSize),
		encodedSize:  int64(result.EncodedSize),
	}, nil
}

// throttleInterval is the minimum interval between progress persists.
const throttleInterval = 2 * time.Second

// encodingProgressLogInterval is the minimum interval between INFO progress logs.
const encodingProgressLogInterval = 3 * time.Minute

// spindleReporter implements reel.Reporter, adapting Reel progress events into
// encodingstate.Snapshot updates on the queue item. Progress persistence is
// throttled to every 2 seconds.
// round1 and round2 round log-attribute floats to 1 and 2 decimal places.
func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

type spindleReporter struct {
	reel.NullReporter
	sess          *stage.Session
	item          *queue.Item
	logger        *slog.Logger
	episodeKey    string
	completedJobs int
	totalJobs     int
	lastPush      time.Time
	lastLog       time.Time
	now           func() time.Time // injectable clock for testing
}

func newSpindleReporter(sess *stage.Session, logger *slog.Logger, episodeKey string, completedJobs int, totalJobs int) *spindleReporter {
	return &spindleReporter{
		sess:          sess,
		item:          sess.Item,
		logger:        logger,
		episodeKey:    episodeKey,
		completedJobs: completedJobs,
		totalJobs:     totalJobs,
		now:           time.Now,
	}
}

// updateSnapshot mutates the encoding snapshot and persists it, warning on
// failure so every call site collapses to a single statement.
//
// hint == "" selects the legacy shape used by progress-percent updates:
// event_type "progress_persist_error", error_hint is the persistence error's
// text, and no "error" attribute. hint != "" selects the shape used by
// substage/result updates: event_type "progress_persist_failed", error_hint
// is the given static hint, and the error is also logged under "error".
func (r *spindleReporter) updateSnapshot(mutate func(*encodingstate.Snapshot), warnMsg, hint, impact string) {
	snap, err := encodingstate.Unmarshal(r.item.EncodingDetailsJSON)
	if err != nil {
		snap = encodingstate.Snapshot{}
	}
	mutate(&snap)
	r.item.EncodingDetailsJSON = snap.Marshal()
	if perr := r.sess.Progress(r.sess.Task.ProgressPercent, r.sess.Task.ProgressMessage, stage.WithEncodingDetails(r.item.EncodingDetailsJSON)); perr != nil {
		if hint == "" {
			r.logger.Warn(warnMsg,
				"event_type", "progress_persist_error",
				"error_hint", perr.Error(),
				"impact", impact,
			)
			return
		}
		r.logger.Warn(warnMsg,
			"event_type", "progress_persist_failed",
			"error_hint", hint,
			"impact", impact,
			"error", perr,
		)
	}
}

func (r *spindleReporter) EncodingProgress(p reel.ProgressSnapshot) {
	now := r.now()
	if now.Sub(r.lastPush) < throttleInterval {
		return
	}
	r.lastPush = now

	r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.Substage = "encoding"
		snap.Percent = float64(p.Percent)
		snap.FPS = float64(p.FPS)
		snap.AverageSpeed = float64(p.Speed)
		snap.ETASeconds = p.ETA.Seconds()
		snap.CurrentFrame = int64(p.CurrentFrame)
		snap.TotalFrames = int64(p.TotalFrames)
		r.sess.Task.ProgressPercent = stage.OverallPercent(r.completedJobs, r.totalJobs, float64(p.Percent))
	}, "failed to persist encoding progress", "", "progress display may be stale")

	if r.lastLog.IsZero() || now.Sub(r.lastLog) >= encodingProgressLogInterval || p.Percent >= 100 {
		r.lastLog = now
		r.logger.Info("encoding progress",
			"event_type", "encoding_progress",
			"episode_key", r.episodeKey,
			"percent", round1(float64(p.Percent)),
			"speed", round2(float64(p.Speed)),
			"bitrate", p.Bitrate,
			"eta_seconds", int(p.ETA.Seconds()),
			"current_frame", p.CurrentFrame,
			"total_frames", p.TotalFrames,
			"chunks_complete", p.ChunksComplete,
			"chunks_total", p.ChunksTotal,
			"workers_active", p.ActiveWorkers,
			"workers_target", p.TargetWorkers,
		)
	}
}

func (r *spindleReporter) EncodingStarted(totalFrames uint64) {
	r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.Substage = "encoding"
		snap.TotalFrames = int64(totalFrames)
	}, "failed to persist encoding started", "", "total frames not persisted")
}

func (r *spindleReporter) Initialization(s reel.InitializationSummary) {
	r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.InputFile = s.InputFile
		snap.Resolution = s.Resolution
		snap.DynamicRange = s.DynamicRange
		snap.Substage = "initializing"
	}, "progress persistence failed", "initialization state not persisted to queue", "encoding progress not reflected in queue")

	r.logger.Info("encode input initialized",
		"event_type", "encode_init",
		"episode_key", r.episodeKey,
		"input", filepath.Base(s.InputFile),
		"resolution", s.Resolution,
		"dynamic_range", s.DynamicRange,
		"duration", s.Duration,
		"audio", s.AudioDescription,
	)
}

func (r *spindleReporter) StageProgress(s reel.StageProgress) {
	attrs := []any{
		"event_type", "encoding_substage",
		"episode_key", r.episodeKey,
		"substage", s.Stage,
		"message", s.Message,
	}
	if s.Percent > 0 {
		attrs = append(attrs, "percent", round1(float64(s.Percent)))
	}
	r.logger.Info("encoding substage", attrs...)
}

func (r *spindleReporter) Verbose(message string) {
	r.logger.Debug("reel verbose",
		"event_type", "reel_verbose",
		"episode_key", r.episodeKey,
		"message", message,
	)
}

func (r *spindleReporter) EncodingConfig(s reel.EncodingConfigSummary) {
	r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.Encoder = s.Encoder
		snap.Preset = s.Preset
		snap.Quality = s.Quality
		snap.Tune = s.Tune
		snap.AudioCodec = s.AudioCodec
		snap.Substage = "configuring"
	}, "progress persistence failed", "encoding config state not persisted to queue", "encoding progress not reflected in queue")

	r.logger.Info("encoder configured",
		"event_type", "encoder_config",
		"episode_key", r.episodeKey,
		"encoder", s.Encoder,
		"encoder_version", s.EncoderVersion,
		"preset", s.Preset,
		"quality", s.Quality,
		"tune", s.Tune,
		"pixel_format", s.PixelFormat,
		"matrix_coefficients", s.MatrixCoefficients,
		"audio_codec", s.AudioCodec,
		"svtav1_params", s.SVTAV1Params,
	)
}

func (r *spindleReporter) CropResult(s reel.CropSummary) {
	r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.CropFilter = s.Crop
		snap.CropRequired = s.Required
		snap.CropMessage = s.Message
		snap.Substage = "crop_detection"
		if s.Required {
			if w, h, parseErr := encodingstate.ParseCropFilter(s.Crop); parseErr == nil {
				snap.Resolution = fmt.Sprintf("%dx%d", w, h)
			}
		}
	}, "progress persistence failed", "crop result not persisted to queue", "encoding progress not reflected in queue")

	decisionResult := "no_crop"
	if s.Required {
		decisionResult = "crop_applied"
	}
	r.logger.Info("crop detection result",
		"decision_type", logs.DecisionCropDetection,
		"decision_result", decisionResult,
		"decision_reason", fmt.Sprintf("filter=%s", s.Crop),
		"episode_key", r.episodeKey,
	)
}

func (r *spindleReporter) ValidationComplete(s reel.ValidationSummary) {
	r.updateSnapshot(func(snap *encodingstate.Snapshot) {
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
	}, "progress persistence failed", "validation result not persisted to queue", "encoding progress not reflected in queue")

	var passed, failed int
	for _, step := range s.Steps {
		if step.Passed {
			passed++
		} else {
			failed++
		}
	}
	decisionResult := "passed"
	if !s.Passed {
		decisionResult = "failed"
	}
	r.logger.Info("encoding validation result",
		"decision_type", logs.DecisionEncodingValidation,
		"decision_result", decisionResult,
		"decision_reason", fmt.Sprintf("steps_passed=%d steps_failed=%d", passed, failed),
		"episode_key", r.episodeKey,
	)
}

func (r *spindleReporter) EncodingComplete(s reel.EncodingOutcome) {
	r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.Substage = "complete"
		snap.Percent = 100
		snap.EncodedSize = int64(s.EncodedSize)
		snap.OriginalSize = int64(s.OriginalSize)
		snap.AverageSpeed = float64(s.AverageSpeed)
		snap.EncodeDurationSeconds = s.TotalTime.Seconds()
	}, "progress persistence failed", "encoding completion not persisted to queue", "encoding progress not reflected in queue")

	r.logger.Info("encode result",
		"event_type", "encode_result",
		"episode_key", r.episodeKey,
		"original_size_bytes", s.OriginalSize,
		"encoded_size_bytes", s.EncodedSize,
		"video_original_size_bytes", s.VideoOriginalSize,
		"video_encoded_size_bytes", s.VideoEncodedSize,
		"wall_time", s.TotalTime.Round(time.Second).String(),
		"average_speed", round2(float64(s.AverageSpeed)),
	)
}

func (r *spindleReporter) Warning(message string) {
	r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.Warning = message
	}, "progress persistence failed", "warning state not persisted to queue", "encoding progress not reflected in queue")

	r.logger.Warn("reel warning",
		"event_type", "reel_warning",
		"error_hint", message,
		// Spindle cannot know what an arbitrary reel warning means for the
		// encode; target-quality mode enforces per-chunk quality regardless,
		// so most warnings (e.g. worker reductions) cost wall time, not
		// quality. The specifics live in error_hint.
		"impact", "reel reported degraded behavior; see error_hint",
	)
}

func (r *spindleReporter) Error(e reel.ReporterError) {
	r.logger.Error("reel encoding error",
		"event_type", "reel_error",
		"error_hint", e.Message,
		"error", e.Title,
	)

	r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.Error = &encodingstate.Issue{
			Title:      e.Title,
			Message:    e.Message,
			Context:    e.Context,
			Suggestion: e.Suggestion,
		}
	}, "progress persistence failed", "encoding error state not persisted to queue", "encoding error may not be visible in queue")
}
