// Package subtitle produces Jellyfin display SRTs by adopting cleaned,
// retimed OpenSubtitles downloads verified against the rip's WhisperX
// transcript. The pipeline stage skips the episode (warning, not failure)
// when no candidate survives verification; the same adoption process backs
// the manual `spindle subtitle` command via AdoptForFile. WhisperX subtitle
// generation is not maintained here — the whisperx-subtitles agent skill
// covers it. The package never rewrites encoded media; the apply stage owns
// placement and optional MKV muxing after the pipeline branches join.
package subtitle

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/language"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/srtutil"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/transcription"
)

var inspectSubtitleMedia = ffprobe.Inspect

// Handler implements stage.Handler for subtitle generation.
type Handler struct {
	cfg         *config.Config
	transcriber *transcription.Service
	osClient    *opensubtitles.Client
}

// New creates a subtitle handler.
func New(cfg *config.Config, transcriber *transcription.Service, osClient *opensubtitles.Client) *Handler {
	return &Handler{
		cfg:         cfg,
		transcriber: transcriber,
		osClient:    osClient,
	}
}

// Run executes the subtitle stage: each pending episode either adopts a
// verified OpenSubtitles download or is skipped with a warning.
func (h *Handler) Run(ctx context.Context, sess *stage.Session) error {
	logger := sess.Logger
	logger.Debug("subtitle stage started", "event_type", "stage_start", "stage", "subtitling")

	if !h.cfg.Subtitles.Enabled {
		logger.Info("subtitles disabled, skipping",
			"decision_type", logs.DecisionSubtitleSkip,
			"decision_result", "skipped",
			"decision_reason", "subtitles.enabled = false",
		)
		return nil
	}

	jobs, skippedCompleted := h.planSubtitleJobs(sess)
	logger.Info("subtitle plan",
		"event_type", "subtitle_plan",
		"jobs", len(jobs),
		"skipped_completed", len(skippedCompleted),
	)
	h.logSkippedSubtitleJobs(logger, skippedCompleted)

	var summary subtitleRunSummary
	for _, job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		summary.attempted++
		outcome, err := h.processSubtitleJob(ctx, sess, job)
		if err != nil {
			return err
		}
		switch outcome {
		case subtitleOutcomeAdopted:
			summary.adopted++
		case subtitleOutcomeSkipped:
			summary.skipped++
		default:
			summary.failed++
		}
	}

	return h.finishSubtitleStage(sess, summary)
}

const (
	subtitleOutcomeAdopted = "adopted"
	subtitleOutcomeSkipped = "skipped"
	subtitleOutcomeFailed  = "failed"
)

type subtitleRunSummary struct {
	attempted int
	adopted   int
	skipped   int
	failed    int
}

func (h *Handler) planSubtitleJobs(sess *stage.Session) ([]stage.AssetJob, []string) {
	jobs, skipped := sess.PendingKeyedAssetJobs(ripspec.AssetKindRipped, ripspec.AssetKindSubtitled)
	// Also skip keys that already have a clean generation record (resume
	// after a retry that recompiled the analysis branch). Skip records
	// (Source "none") count: the skip decision was already made and warned.
	var pending []stage.AssetJob
	for _, job := range jobs {
		if rec := findGenRecord(sess.Env, job.Key); rec != nil && len(rec.SevereIssues) == 0 {
			skipped = append(skipped, job.Key)
			continue
		}
		pending = append(pending, job)
	}
	return pending, skipped
}

func findGenRecord(env *ripspec.Envelope, key string) *ripspec.SubtitleGenRecord {
	records := env.Attributes.SubtitleGenerationResults
	for i := range records {
		if strings.EqualFold(records[i].EpisodeKey, key) {
			return &records[i]
		}
	}
	return nil
}

func (h *Handler) logSkippedSubtitleJobs(logger *slog.Logger, skippedCompleted []string) {
	for _, key := range skippedCompleted {
		logger.Info("subtitle already completed, skipping",
			"decision_type", logs.DecisionSubtitleResume,
			"decision_result", "skipped",
			"decision_reason", "already completed",
			"episode_key", key,
		)
	}
}

// processSubtitleJob adopts the best verified OpenSubtitles candidate for one
// episode, or records a skip. Returned errors are persistence/context
// failures that abort the stage; candidate problems never fail the job.
func (h *Handler) processSubtitleJob(ctx context.Context, sess *stage.Session, job stage.AssetJob) (string, error) {
	logger := sess.Logger
	key := job.Key

	h.startSubtitleJob(sess, job)

	candidates, skipReason := h.listSubtitleCandidates(ctx, sess, key)
	if len(candidates) == 0 {
		return subtitleOutcomeSkipped, h.recordSubtitleSkip(sess, key, skipReason)
	}

	stagingRoot, err := sess.Item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		return subtitleOutcomeFailed, err
	}
	// Adopted SRTs land in staging; the apply stage places them next to the
	// encoded output and muxes them after the encoding branch joins.
	subtitleDir := filepath.Join(stagingRoot, "subtitles")
	workDir := filepath.Join(subtitleDir, job.Key+".work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return subtitleOutcomeFailed, fmt.Errorf("create subtitles work dir: %w", err)
	}

	reference, err := h.ensureSyncReference(ctx, sess, job)
	if err != nil {
		h.recordSubtitleFailure(logger, sess, key, fmt.Sprintf("sync reference transcript: %v", err))
		return subtitleOutcomeFailed, nil
	}
	referenceCues, err := srtutil.ParseFile(reference.SRTPath)
	if err != nil {
		h.recordSubtitleFailure(logger, sess, key, fmt.Sprintf("read sync reference transcript: %v", err))
		return subtitleOutcomeFailed, nil
	}
	videoSeconds, durationSource := resolveSubtitleVideoDuration(ctx, logger, job.Input.Path, reference.Duration)
	logger.Info("subtitle duration selected",
		"decision_type", "subtitle_duration_source",
		"decision_result", durationSource,
		"decision_reason", fmt.Sprintf("video_seconds=%.3f transcript_seconds=%.3f", videoSeconds, reference.Duration),
		"episode_key", key,
	)

	_ = sess.Progress(job.Percent(92), job.PhaseMessage("Syncing subtitles ("+key+")"))
	for _, candidate := range candidates {
		adopted, err := h.tryAdoptCandidate(ctx, sess, job, candidate, adoptContext{
			ReferenceSRTPath: reference.SRTPath,
			ReferenceCues:    referenceCues,
			ReferenceWords:   loadReferenceWords(logger, reference.JSONPath),
			VideoSeconds:     videoSeconds,
			SubtitleDir:      subtitleDir,
			WorkDir:          workDir,
		})
		if err != nil {
			return subtitleOutcomeFailed, err
		}
		if adopted {
			return subtitleOutcomeAdopted, nil
		}
	}
	return subtitleOutcomeSkipped, h.recordSubtitleSkip(sess, key, "no downloaded subtitle passed verification")
}

// adoptContext carries the per-job inputs candidate adoption needs.
type adoptContext struct {
	ReferenceSRTPath string
	ReferenceCues    []srtutil.Cue
	// ReferenceWords is the transcript's aligned word timestamps for the
	// word-snap pass; nil (audio.json unavailable) skips the pass.
	ReferenceWords []transcription.Word
	VideoSeconds   float64
	SubtitleDir    string
	WorkDir        string
}

// candidateEvaluation is the outcome of cleaning, syncing, and verifying one
// candidate. A non-empty RejectReason means the caller should try the next
// candidate.
type candidateEvaluation struct {
	Cues         []srtutil.Cue
	Check        adoptionCheck
	Validation   validationResult
	Clean        cleanStats
	RejectReason string
}

// evaluateCandidate fetches, cleans, syncs, and verifies one candidate
// against the reference transcript. Errors are hard failures (I/O,
// cancellation); every candidate-quality problem lands in RejectReason.
func (h *Handler) evaluateCandidate(ctx context.Context, candidate subtitleCandidate, adopt adoptContext) (candidateEvaluation, error) {
	var eval candidateEvaluation

	localPath, err := h.fetchCandidate(ctx, candidate)
	if err != nil {
		eval.RejectReason = fmt.Sprintf("download failed: %v", err)
		return eval, nil
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		eval.RejectReason = fmt.Sprintf("unreadable candidate file: %v", err)
		return eval, nil
	}
	eval.Cues, eval.Clean = cleanDownloadedSubtitle(data)
	if len(eval.Cues) == 0 {
		eval.RejectReason = "no cues remain after cleanup"
		return eval, nil
	}
	// Pre-sync span sanity: ffsubsync can stretch a wrong-length subtitle far
	// enough to fool the post-sync checks, so a candidate whose raw span is
	// nowhere near the video length is rejected before sync ever runs.
	if adopt.VideoSeconds > 0 {
		rawSpan := eval.Cues[len(eval.Cues)-1].End - eval.Cues[0].Start
		if rawSpan < adoptMinSpanCoverage*adopt.VideoSeconds {
			eval.RejectReason = fmt.Sprintf("candidate spans %.0fs of the %.0fs video before sync; likely wrong content or a different cut", rawSpan, adopt.VideoSeconds)
			return eval, nil
		}
	}

	cleanedPath := filepath.Join(adopt.WorkDir, fmt.Sprintf("%d.cleaned.srt", candidate.FileID))
	if err := writeSRTAtomic(cleanedPath, eval.Cues); err != nil {
		return eval, fmt.Errorf("write cleaned candidate: %w", err)
	}
	syncedPath := filepath.Join(adopt.WorkDir, fmt.Sprintf("%d.synced.srt", candidate.FileID))
	if err := syncSubtitleToReference(ctx, adopt.ReferenceSRTPath, cleanedPath, syncedPath); err != nil {
		if ctx.Err() != nil {
			return eval, ctx.Err()
		}
		eval.RejectReason = fmt.Sprintf("sync failed: %v", err)
		return eval, nil
	}
	if eval.Cues, err = srtutil.ParseFile(syncedPath); err != nil {
		eval.RejectReason = fmt.Sprintf("unreadable synced output: %v", err)
		return eval, nil
	}

	eval.Check = verifyAdoptionCandidate(eval.Cues, adopt.ReferenceCues, adopt.VideoSeconds)
	if !eval.Check.Passed && eval.Check.TextSimilarity >= adoptRefineMinSimilarity {
		// The text proves this is the right subtitle; give its timing one
		// deterministic repair (constant offset + linear drift) and re-gate.
		if refined, ok := refineCueTiming(eval.Cues, adopt.ReferenceCues); ok {
			if recheck := verifyAdoptionCandidate(refined, adopt.ReferenceCues, adopt.VideoSeconds); recheck.Passed {
				recheck.TimingRefined = true
				eval.Cues = refined
				eval.Check = recheck
			}
		}
	}
	if !eval.Check.Passed {
		eval.RejectReason = fmt.Sprintf("%s (%s)", eval.Check.FailureReason, eval.Check.Metrics())
		return eval, nil
	}
	// The candidate is adopted; restore per-cue precision by snapping cue
	// starts to the transcript's forced-alignment word onsets, keeping the
	// result only when it still passes the unchanged gate.
	if snapped, count := snapCuesToWords(eval.Cues, adopt.ReferenceWords); count > 0 {
		if recheck := verifyAdoptionCandidate(snapped, adopt.ReferenceCues, adopt.VideoSeconds); recheck.Passed {
			recheck.TimingRefined = eval.Check.TimingRefined
			recheck.SnappedCues = count
			eval.Cues = snapped
			eval.Check = recheck
		}
	}
	eval.Validation = validateCuesDetailed(eval.Cues, adopt.VideoSeconds)
	if len(eval.Validation.SevereIssues) > 0 {
		eval.RejectReason = "severe validation: " + strings.Join(eval.Validation.SevereIssues, ", ")
		return eval, nil
	}
	for i := range eval.Cues {
		eval.Cues[i].Index = i + 1
	}
	return eval, nil
}

// tryAdoptCandidate evaluates one candidate and (on success) records it as
// the episode's display subtitle. A false return with nil error means the
// candidate was rejected and the caller should try the next one.
func (h *Handler) tryAdoptCandidate(ctx context.Context, sess *stage.Session, job stage.AssetJob, candidate subtitleCandidate, adopt adoptContext) (bool, error) {
	logger := sess.Logger
	key := job.Key

	eval, err := h.evaluateCandidate(ctx, candidate, adopt)
	if err != nil {
		return false, err
	}
	logger.Debug("subtitle candidate cleaned",
		"event_type", "subtitle_candidate_cleaned",
		"episode_key", key,
		"candidate", candidate.label(),
		"original_cues", eval.Clean.OriginalCues,
		"cleaned_cues", eval.Clean.CleanedCues,
		"spam_cues", eval.Clean.SpamCues,
		"emptied_cues", eval.Clean.EmptiedCues,
	)
	if eval.RejectReason != "" {
		logger.Info("subtitle candidate rejected",
			"decision_type", logs.DecisionSubtitleSource,
			"decision_result", "candidate_rejected",
			"decision_reason", eval.RejectReason,
			"episode_key", key,
			"candidate", candidate.label(),
		)
		return false, nil
	}

	synced := eval.Cues
	validation := eval.Validation
	check := eval.Check
	language := candidate.Language
	if language == "" {
		language = "en"
	}
	displayPath := displaySubtitlePath(filepath.Join(adopt.SubtitleDir, key+".mkv"), language)
	if err := writeSRTAtomic(displayPath, synced); err != nil {
		return false, fmt.Errorf("write adopted subtitle: %w", err)
	}

	h.logSubtitleValidation(logger, key, validation)
	h.applySubtitleReviewIssues(logger, sess, key, validation)

	record := ripspec.SubtitleGenRecord{
		EpisodeKey:       key,
		Source:           "opensubtitles",
		SubtitlePath:     displayPath,
		Segments:         len(synced),
		DurationSec:      adopt.VideoSeconds,
		Language:         language,
		ValidationResult: subtitleValidationResult(validation),
		QCObservations:   validation.Issues,
		ReviewIssues:     validation.ReviewIssues,
	}
	if err := sess.MergeSave(func(env *ripspec.Envelope) error {
		upsertSubtitleGenRecord(&env.Attributes.SubtitleGenerationResults, record)
		return nil
	}); err != nil {
		return false, err
	}

	logger.Info("subtitle adopted from download",
		"decision_type", logs.DecisionSubtitleSource,
		"decision_result", subtitleOutcomeAdopted,
		"decision_reason", fmt.Sprintf("verified against WhisperX transcript (%s)", check.Metrics()),
		"episode_key", key,
		"candidate", candidate.label(),
		"subtitle_path", displayPath,
		"segments", len(synced),
	)
	return true, nil
}

// recordSubtitleSkip records the no-subtitle outcome for an episode. The item
// completes normally; the warning is the operator's cue to run the manual
// `spindle subtitle` command, and itemaudit surfaces it as an anomaly.
func (h *Handler) recordSubtitleSkip(sess *stage.Session, key, reason string) error {
	sess.Logger.Warn("subtitle generation skipped",
		"event_type", "subtitle_skipped",
		"error_hint", reason,
		"impact", "title has no subtitles; generate them with the whisperx-subtitles agent skill",
		"episode_key", key,
	)
	sess.Logger.Info("subtitle source decision",
		"decision_type", logs.DecisionSubtitleSource,
		"decision_result", subtitleOutcomeSkipped,
		"decision_reason", reason,
		"episode_key", key,
	)
	return sess.MergeSave(func(env *ripspec.Envelope) error {
		upsertSubtitleGenRecord(&env.Attributes.SubtitleGenerationResults, ripspec.SubtitleGenRecord{
			EpisodeKey:       key,
			Source:           "none",
			ValidationResult: "skipped",
		})
		return nil
	})
}

func (h *Handler) startSubtitleJob(sess *stage.Session, job stage.AssetJob) {
	logger := sess.Logger
	key := job.Key

	logger.Info("ripped asset selected as subtitle input",
		"decision_type", logs.DecisionTranscriptionAsset,
		"decision_result", ripspec.AssetKindRipped,
		"decision_reason", fmt.Sprintf("episode_key=%s", key),
		"path", job.Input.Path,
	)

	logger.Info(job.PhaseMessage("Preparing subtitles ("+key+")"),
		"event_type", "subtitle_start",
	)
	_ = sess.Progress(job.Percent(5), job.PhaseMessage("Preparing subtitles ("+key+")"))
}

// ensureSyncReference returns the episode's canonical WhisperX transcript,
// reusing the shared artifact when one exists and transcribing the ripped
// asset once (recording the artifact) when it does not.
func (h *Handler) ensureSyncReference(ctx context.Context, sess *stage.Session, job stage.AssetJob) (*transcription.TranscribeResult, error) {
	if result := transcriptArtifact(sess, job.Key); result != nil {
		return result, nil
	}
	if h.transcriber == nil {
		return nil, fmt.Errorf("transcriber not configured")
	}
	stagingRoot, err := sess.Item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		return nil, err
	}
	outDir := filepath.Join(stagingRoot, "transcripts", job.Key)
	selected, err := h.transcriber.SelectPrimaryAudioTrack(ctx, job.Input.Path, "en")
	if err != nil {
		return nil, fmt.Errorf("select audio: %w", err)
	}
	result, err := h.transcriber.Transcribe(ctx, transcription.TranscribeRequest{
		InputPath:  job.Input.Path,
		AudioIndex: selected.Index,
		Language:   selected.Language,
		OutputDir:  outDir,
		ItemID:     sess.Item.ID,
		EpisodeKey: job.Key,
		Purpose:    "subtitle_sync_reference",
	}, func(phase transcription.Phase, elapsed time.Duration) {
		message := sess.Task.ProgressMessage
		switch phase {
		case transcription.PhaseExtract:
			if elapsed == 0 {
				message = job.PhaseMessage("Extracting audio (" + job.Key + ")")
			}
		case transcription.PhaseTranscribe:
			if elapsed == 0 {
				message = job.PhaseMessage("Transcribing audio (" + job.Key + ")")
			}
		}
		_ = sess.Progress(job.Percent(subtitlePhasePercent(phase, elapsed)), message)
	})
	if err != nil {
		return nil, err
	}
	if err := sess.SaveAssetSuccess(ripspec.AssetKindTranscript, ripspec.Asset{
		EpisodeKey: job.Key,
		TitleID:    job.Input.TitleID,
		Path:       result.SRTPath,
		Status:     ripspec.AssetStatusCompleted,
	}); err != nil {
		return nil, fmt.Errorf("record transcript asset: %w", err)
	}
	return result, nil
}

// transcriptArtifact returns the episode's shared WhisperX transcript
// artifact (recorded by episode identification, or commentary analysis for
// movies) when both its SRT and JSON still exist, so the subtitle stage can
// skip its own WhisperX pass. Returns nil when there is no usable artifact.
func transcriptArtifact(sess *stage.Session, key string) *transcription.TranscribeResult {
	asset, ok := sess.Env.Assets.FindAsset(ripspec.AssetKindTranscript, key)
	if !ok || !asset.IsCompleted() {
		return nil
	}
	srtPath := asset.Path
	jsonPath := filepath.Join(filepath.Dir(srtPath), "audio.json")
	if _, err := os.Stat(srtPath); err != nil {
		return nil
	}
	if _, err := os.Stat(jsonPath); err != nil {
		return nil
	}
	cues, err := srtutil.ParseFile(srtPath)
	if err != nil {
		return nil
	}
	var duration float64
	if len(cues) > 0 {
		duration = cues[len(cues)-1].End
	}
	sess.Logger.Info("reusing transcript artifact for subtitle generation",
		"decision_type", "subtitle_transcript_source",
		"decision_result", "artifact_reused",
		"decision_reason", "canonical transcript already produced earlier in the pipeline",
		"episode_key", key,
		"srt_path", srtPath,
	)
	return &transcription.TranscribeResult{
		SRTPath:  srtPath,
		JSONPath: jsonPath,
		Duration: duration,
		Segments: len(cues),
	}
}

func (h *Handler) logSubtitleValidation(logger *slog.Logger, key string, validation validationResult) {
	stats := validation.Stats
	logger.Info("SRT validation QC summary",
		"decision_type", logs.DecisionSRTValidation,
		"decision_result", "qc_summary",
		"decision_reason", fmt.Sprintf("cue_count=%d max_cps=%.2f p95_cps=%.2f high_cps_cues=%d short_duration_cues=%d long_duration_cues=%d overlong_line_cues=%d unbalanced_line_break_cues=%d", stats.CueCount, stats.MaxCPS, stats.P95CPS, stats.HighCPSCues, stats.ShortDurationCues, stats.LongDurationCues, stats.OverlongLineCues, stats.UnbalancedLineBreakCues),
		"episode_key", key,
		"cue_count", stats.CueCount,
		"max_cps", stats.MaxCPS,
		"p95_cps", stats.P95CPS,
		"high_cps_cues", stats.HighCPSCues,
		"short_duration_cues", stats.ShortDurationCues,
		"long_duration_cues", stats.LongDurationCues,
		"overlong_line_cues", stats.OverlongLineCues,
		"unbalanced_line_break_cues", stats.UnbalancedLineBreakCues,
		"too_many_line_cues", stats.TooManyLineCues,
	)
}

func (h *Handler) applySubtitleReviewIssues(logger *slog.Logger, sess *stage.Session, key string, validation validationResult) {
	reviewIssueSet := make(map[string]bool, len(validation.ReviewIssues))
	for _, issue := range validation.ReviewIssues {
		reviewIssueSet[issue] = true
	}
	for _, issue := range validation.Issues {
		requiresReview := reviewIssueSet[issue]
		if !requiresReview {
			logger.Debug("SRT validation observation",
				"decision_type", logs.DecisionSRTValidation,
				"decision_result", issue,
				"decision_reason", "automated quality check recorded without review routing",
				"episode_key", key,
				"requires_review", false,
			)
			continue
		}
		logger.Info("SRT validation issue",
			"decision_type", logs.DecisionSRTValidation,
			"decision_result", issue,
			"decision_reason", "automated quality check requires review",
			"episode_key", key,
			"requires_review", true,
		)
		h.persistReviewReason(logger, sess, key, "Subtitle validation: "+issue, "srt_validation: "+issue+" ("+key+")")
	}
}

// persistReviewReason appends envReason to the episode matching key and
// queueReason to the queue item, logging persistence failures without
// failing the stage.
func (h *Handler) persistReviewReason(logger *slog.Logger, sess *stage.Session, key, envReason, queueReason string) {
	if mergeErr := sess.MergeSave(func(env *ripspec.Envelope) error {
		if ep := env.EpisodeByKey(key); ep != nil {
			ep.AppendReviewReason(envReason)
		}
		return nil
	}); mergeErr != nil {
		logger.Error("subtitle review persistence failed",
			"event_type", "subtitle_failure_persist_failed",
			"error", mergeErr,
		)
	}
	if mergeErr := sess.MergeAddReviewReason(queueReason); mergeErr != nil {
		logger.Error("subtitle review persistence failed",
			"event_type", "subtitle_failure_persist_failed",
			"error", mergeErr,
		)
	}
}

func (h *Handler) finishSubtitleStage(sess *stage.Session, summary subtitleRunSummary) error {
	if summary.attempted > 0 && summary.failed == summary.attempted {
		return fmt.Errorf("all %d subtitle job(s) failed", summary.attempted)
	}

	sess.Logger.Debug("subtitle stage completed",
		"event_type", "stage_complete",
		"stage", "subtitling",
		"attempted", summary.attempted,
		"adopted", summary.adopted,
		"skipped", summary.skipped,
		"failed", summary.failed,
	)
	return nil
}

func resolveSubtitleVideoDuration(ctx context.Context, logger *slog.Logger, videoPath string, fallback float64) (seconds float64, source string) {
	probe, err := inspectSubtitleMedia(ctx, "", videoPath)
	if err == nil {
		if duration := probe.DurationSeconds(); duration > 0 {
			return duration, "media_probe"
		}
	} else {
		logger.Warn("video duration probe failed",
			"event_type", "probe_error",
			"error_hint", err.Error(),
			"impact", "subtitle duration falls back to transcript length",
		)
	}
	if fallback > 0 {
		return fallback, "transcript_fallback"
	}
	return 0, "unknown"
}

func subtitlePhasePercent(phase transcription.Phase, elapsed time.Duration) float64 {
	switch phase {
	case transcription.PhaseExtract:
		if elapsed > 0 {
			return 25
		}
		return 10
	case transcription.PhaseTranscribe:
		if elapsed > 0 {
			return 90
		}
		return 35
	default:
		return 0
	}
}

func (h *Handler) recordSubtitleFailure(
	logger *slog.Logger,
	sess *stage.Session,
	key string,
	errMsg string,
) {
	errMsg = strings.TrimSpace(errMsg)
	if errMsg == "" {
		errMsg = "subtitle generation failed"
	}
	logger.Error("subtitle generation failed for episode",
		"event_type", "episode_subtitle_failed",
		"episode_key", key,
		"error_hint", errMsg,
		"error", errMsg,
		"impact", "subtitle missing for this episode; continuing with others",
	)
	if mergeErr := sess.MergeSave(func(env *ripspec.Envelope) error {
		env.Assets.AddAsset(ripspec.AssetKindSubtitled, ripspec.Asset{
			EpisodeKey: key,
			Status:     ripspec.AssetStatusFailed,
			ErrorMsg:   errMsg,
		})
		if ep := env.EpisodeByKey(key); ep != nil {
			ep.AppendReviewReason("Subtitle generation failed: " + errMsg)
		}
		return nil
	}); mergeErr != nil {
		logger.Error("subtitle failure persistence failed",
			"event_type", "subtitle_failure_persist_failed",
			"error", mergeErr,
		)
	}
	if mergeErr := sess.MergeAddReviewReason("subtitle_failure: " + errMsg + " (" + key + ")"); mergeErr != nil {
		logger.Error("subtitle review reason persistence failed",
			"event_type", "subtitle_failure_persist_failed",
			"error", mergeErr,
		)
	}
}

func upsertSubtitleGenRecord(records *[]ripspec.SubtitleGenRecord, record ripspec.SubtitleGenRecord) {
	for i := range *records {
		if strings.EqualFold((*records)[i].EpisodeKey, record.EpisodeKey) {
			(*records)[i] = record
			return
		}
	}
	*records = append(*records, record)
}

func subtitleValidationResult(validation validationResult) string {
	switch {
	case len(validation.SevereIssues) > 0:
		return "failed"
	case len(validation.ReviewIssues) > 0:
		return "needs_review"
	default:
		return "passed"
	}
}

// displaySubtitlePath derives the sidecar SRT path for a video path and
// subtitle language, e.g. movie.mkv -> movie.en.srt.
func displaySubtitlePath(videoPath, subtitleLanguage string) string {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	lang := language.ToISO2(subtitleLanguage)
	if lang == "" {
		lang = "en"
	}
	return base + "." + lang + ".srt"
}

// writeSRTAtomic renders cues and writes them to path atomically via a
// temp file in the same directory followed by rename.
func writeSRTAtomic(path string, cues []srtutil.Cue) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".subtitle-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(srtutil.Format(cues)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	_ = os.Chmod(tmpPath, 0o644)
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
