package encoder

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codeberg.org/five82/reel"

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

// planJobs determines the encoding jobs from the envelope's ripped assets.
// Movies produce one job; TV produces one job per episode.
func planJobs(env *ripspec.Envelope) []stage.AssetJob {
	return stage.CompletedAssetJobs(env, ripspec.AssetKindRipped)
}

// Run executes the encoding stage.
func (h *Handler) Run(ctx context.Context, sess *stage.Session) error {
	item := sess.Item
	logger := sess.Logger
	logger.Info("encoding stage started", "event_type", "stage_start", "stage", "encoding")
	env := sess.Env

	stagingRoot, err := item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		return fmt.Errorf("staging root: %w", err)
	}
	encodedDir := filepath.Join(stagingRoot, "encoded")

	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		return fmt.Errorf("create encoded dir: %w", err)
	}

	jobs := planJobs(env)
	if len(jobs) == 0 {
		return fmt.Errorf("no ripped assets to encode")
	}

	logger.Info("encoding plan",
		"decision_type", logs.DecisionEncodingPlan,
		"decision_result", fmt.Sprintf("%d jobs", len(jobs)),
		"decision_reason", fmt.Sprintf("media_type=%s", env.Metadata.MediaType),
	)

	encoder, err := h.newEncoder(logger)
	if err != nil {
		return err
	}

	summary, err := h.encodeJobs(ctx, sess, encoder, encodedDir, jobs)
	if err != nil {
		return err
	}

	// Persist envelope.
	if err := sess.Save(); err != nil {
		return err
	}

	if summary.errors > 0 {
		return fmt.Errorf("encoding failed for %d of %d jobs", summary.errors, len(jobs))
	}

	// Notification.
	snap, _ := encodingstate.Unmarshal(item.EncodingDetailsJSON)
	msg := fmt.Sprintf("Encoded %s (%d files", item.DisplayTitle(), len(jobs))
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
		"item_id", item.ID,
	)

	logger.Info("encoding stage completed",
		"event_type", "stage_complete",
		"stage", "encoding",
		"jobs", len(jobs),
		"encoded_size_bytes", summary.encodedSize,
		"original_size_bytes", summary.originalSize,
	)
	return nil
}

func (h *Handler) newEncoder(logger *slog.Logger) (*reel.Encoder, error) {
	logger.Info("Reel target-quality mode selected",
		"decision_type", logs.DecisionEncodingConfig,
		"decision_result", "target",
		"decision_reason", "spindle always uses Reel target-quality mode",
	)

	encoder, err := reel.New(reel.WithQualityMode("target"))
	if err != nil {
		return nil, fmt.Errorf("create reel encoder: %w", err)
	}
	return encoder, nil
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

func (h *Handler) encodeJobs(ctx context.Context, sess *stage.Session, encoder *reel.Encoder, encodedDir string, jobs []stage.AssetJob) (encodeSummary, error) {
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

		result, err := h.encodeJob(ctx, sess, encoder, encodedDir, job)
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

func (h *Handler) encodeJob(ctx context.Context, sess *stage.Session, encoder *reel.Encoder, encodedDir string, job stage.AssetJob) (encodeJobResult, error) {
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

	item.ProgressMessage = job.PhaseMessage("Encoding " + filepath.Base(job.Input.Path))
	logger.Info(item.ProgressMessage,
		"event_type", "encode_start",
		"episode_key", job.Key,
	)
	_ = sess.Progress(job.Percent(0), item.ProgressMessage, stage.WithActiveEpisode(job.Key))

	// Reset encoding snapshot and force-persist.
	snap := h.initialEncodingSnapshot(ctx, logger, job)
	item.EncodingDetailsJSON = snap.Marshal()
	if err := sess.Progress(item.ProgressPercent, item.ProgressMessage, stage.WithEncodingDetails(item.EncodingDetailsJSON)); err != nil {
		logger.Warn("failed to persist initial snapshot",
			"event_type", "progress_persist_error",
			"error_hint", err.Error(),
			"impact", "progress display may be stale",
		)
	}

	reporter := newSpindleReporter(sess, logger, job.Key, job.ProgressIndex, job.ProgressTotal)
	result, encErr := encoder.EncodeWithReporter(ctx, job.Input.Path, encodedDir, reporter)
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
		"decision_reason", fmt.Sprintf("resolution=%s codecs=%s original_size=%d", resolution, strings.Join(codecs, ","), snap.OriginalSize),
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
	if persistErr := sess.Progress(job.CompletionPercent(), item.ProgressMessage, stage.WithEncodingDetails(item.EncodingDetailsJSON)); persistErr != nil {
		logger.Warn("failed to persist error snapshot",
			"event_type", "progress_persist_error",
			"error_hint", persistErr.Error(),
			"impact", "error state not reflected in progress",
		)
	}
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
	if err := sess.Progress(job.CompletionPercent(), item.ProgressMessage, stage.WithEncodingDetails(item.EncodingDetailsJSON)); err != nil {
		logger.Warn("failed to persist final snapshot",
			"event_type", "progress_persist_error",
			"error_hint", err.Error(),
			"impact", "final progress not reflected",
		)
	}

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
		sess.AddEpisodeReviewReason(job.Key, "Encoding validation failed")
		sess.AddReviewReason(fmt.Sprintf("validation failed for %s", job.Key))
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

func (r *spindleReporter) updateSnapshot(mutate func(*encodingstate.Snapshot)) error {
	snap, err := encodingstate.Unmarshal(r.item.EncodingDetailsJSON)
	if err != nil {
		snap = encodingstate.Snapshot{}
	}
	mutate(&snap)
	r.item.EncodingDetailsJSON = snap.Marshal()
	return r.sess.Progress(r.item.ProgressPercent, r.item.ProgressMessage, stage.WithEncodingDetails(r.item.EncodingDetailsJSON))
}

func (r *spindleReporter) EncodingProgress(p reel.ProgressSnapshot) {
	now := r.now()
	if now.Sub(r.lastPush) < throttleInterval {
		return
	}
	r.lastPush = now

	if err := r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.Substage = "encoding"
		snap.Percent = float64(p.Percent)
		snap.FPS = float64(p.FPS)
		snap.AverageSpeed = float64(p.Speed)
		snap.ETASeconds = p.ETA.Seconds()
		snap.CurrentFrame = int64(p.CurrentFrame)
		snap.TotalFrames = int64(p.TotalFrames)
		r.item.ProgressPercent = stage.OverallPercent(r.completedJobs, r.totalJobs, float64(p.Percent))
	}); err != nil {
		r.logger.Warn("failed to persist encoding progress",
			"event_type", "progress_persist_error",
			"error_hint", err.Error(),
			"impact", "progress display may be stale",
		)
	}

	if r.lastLog.IsZero() || now.Sub(r.lastLog) >= encodingProgressLogInterval || p.Percent >= 100 {
		r.lastLog = now
		r.logger.Info("encoding progress",
			"event_type", "encoding_progress",
			"episode_key", r.episodeKey,
			"percent", p.Percent,
			"fps", p.FPS,
			"speed", p.Speed,
			"eta_seconds", p.ETA.Seconds(),
			"current_frame", p.CurrentFrame,
			"total_frames", p.TotalFrames,
		)
	}
}

func (r *spindleReporter) EncodingStarted(totalFrames uint64) {
	if err := r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.Substage = "encoding"
		snap.TotalFrames = int64(totalFrames)
	}); err != nil {
		r.logger.Warn("failed to persist encoding started",
			"event_type", "progress_persist_error",
			"error_hint", err.Error(),
			"impact", "total frames not persisted",
		)
	}
}

func (r *spindleReporter) Initialization(s reel.InitializationSummary) {
	if err := r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.InputFile = s.InputFile
		snap.Resolution = s.Resolution
		snap.DynamicRange = s.DynamicRange
		snap.Substage = "initializing"
	}); err != nil {
		r.logger.Warn("progress persistence failed",
			"event_type", "progress_persist_failed",
			"error_hint", "initialization state not persisted to queue",
			"impact", "encoding progress not reflected in queue",
			"error", err,
		)
	}
}

func (r *spindleReporter) EncodingConfig(s reel.EncodingConfigSummary) {
	if err := r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.Encoder = s.Encoder
		snap.Preset = s.Preset
		snap.Quality = s.Quality
		snap.Tune = s.Tune
		snap.AudioCodec = s.AudioCodec
		snap.Substage = "configuring"
	}); err != nil {
		r.logger.Warn("progress persistence failed",
			"event_type", "progress_persist_failed",
			"error_hint", "encoding config state not persisted to queue",
			"impact", "encoding progress not reflected in queue",
			"error", err,
		)
	}
}

func (r *spindleReporter) CropResult(s reel.CropSummary) {
	if err := r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.CropFilter = s.Crop
		snap.CropRequired = s.Required
		snap.CropMessage = s.Message
		snap.Substage = "crop_detection"
		if s.Required {
			if w, h, parseErr := encodingstate.ParseCropFilter(s.Crop); parseErr == nil {
				snap.Resolution = fmt.Sprintf("%dx%d", w, h)
			}
		}
	}); err != nil {
		r.logger.Warn("progress persistence failed",
			"event_type", "progress_persist_failed",
			"error_hint", "crop result not persisted to queue",
			"impact", "encoding progress not reflected in queue",
			"error", err,
		)
	}

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
	if err := r.updateSnapshot(func(snap *encodingstate.Snapshot) {
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
	}); err != nil {
		r.logger.Warn("progress persistence failed",
			"event_type", "progress_persist_failed",
			"error_hint", "validation result not persisted to queue",
			"impact", "encoding progress not reflected in queue",
			"error", err,
		)
	}

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
	if err := r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.Substage = "complete"
		snap.Percent = 100
		snap.EncodedSize = int64(s.EncodedSize)
		snap.OriginalSize = int64(s.OriginalSize)
		snap.AverageSpeed = float64(s.AverageSpeed)
		snap.EncodeDurationSeconds = s.TotalTime.Seconds()
	}); err != nil {
		r.logger.Warn("progress persistence failed",
			"event_type", "progress_persist_failed",
			"error_hint", "encoding completion not persisted to queue",
			"impact", "encoding progress not reflected in queue",
			"error", err,
		)
	}
}

func (r *spindleReporter) Warning(message string) {
	if err := r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.Warning = message
	}); err != nil {
		r.logger.Warn("progress persistence failed",
			"event_type", "progress_persist_failed",
			"error_hint", "warning state not persisted to queue",
			"impact", "encoding progress not reflected in queue",
			"error", err,
		)
	}

	r.logger.Warn("reel warning",
		"event_type", "reel_warning",
		"error_hint", message,
		"impact", "encoding may produce suboptimal results",
	)
}

func (r *spindleReporter) Error(e reel.ReporterError) {
	r.logger.Error("reel encoding error",
		"event_type", "reel_error",
		"error_hint", e.Message,
		"error", e.Title,
	)

	if err := r.updateSnapshot(func(snap *encodingstate.Snapshot) {
		snap.Error = &encodingstate.Issue{
			Title:      e.Title,
			Message:    e.Message,
			Context:    e.Context,
			Suggestion: e.Suggestion,
		}
	}); err != nil {
		r.logger.Warn("progress persistence failed",
			"event_type", "progress_persist_failed",
			"error_hint", "encoding error state not persisted to queue",
			"impact", "encoding error may not be visible in queue",
			"error", err,
		)
	}
}
