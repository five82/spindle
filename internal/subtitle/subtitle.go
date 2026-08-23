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
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/srtutil"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/transcription"
)

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

	if !h.cfg.Subtitles.Enabled {
		logger.Info("subtitles disabled, skipping",
			"decision_type", logs.DecisionSubtitleSkip,
			"decision_result", "skipped",
			"decision_reason", "subtitles.enabled = false",
		)
		return nil
	}

	jobs, skippedCompleted := planSubtitleJobs(sess)
	logger.Info("subtitle plan",
		"event_type", "subtitle_plan",
		"jobs", len(jobs),
		"skipped_completed", len(skippedCompleted),
	)
	logSkippedSubtitleJobs(logger, skippedCompleted)

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

	if summary.attempted > 0 && summary.failed == summary.attempted {
		return fmt.Errorf("all %d subtitle job(s) failed", summary.attempted)
	}
	return nil
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

func planSubtitleJobs(sess *stage.Session) ([]stage.AssetJob, []string) {
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

func logSkippedSubtitleJobs(logger *slog.Logger, skippedCompleted []string) {
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

	startSubtitleJob(sess, job)

	candidates, skipReason := h.listSubtitleCandidates(ctx, sess, key)
	if len(candidates) == 0 {
		return subtitleOutcomeSkipped, recordSubtitleSkip(sess, key, skipReason)
	}

	// Adopted SRTs land in staging; the apply stage places them next to the
	// encoded output and muxes them after the encoding branch joins.
	workDir, err := sess.StageDir(h.cfg.Paths.StagingDir, "subtitles", job.Key+".work")
	if err != nil {
		return subtitleOutcomeFailed, err
	}
	subtitleDir := filepath.Dir(workDir)

	reference, err := h.ensureSyncReference(ctx, sess, job)
	if err != nil {
		recordSubtitleFailure(logger, sess, key, fmt.Sprintf("sync reference transcript: %v", err))
		return subtitleOutcomeFailed, nil
	}
	jobLogger := logger.With("episode_key", key)
	adopt, err := buildAdoptContext(ctx, jobLogger, reference, job.Input.Path, workDir)
	if err != nil {
		recordSubtitleFailure(logger, sess, key, err.Error())
		return subtitleOutcomeFailed, nil
	}

	sess.Progress(job.Percent(92), job.PhaseMessage("Syncing subtitles ("+key+")"))
	adopted, _, err := h.adoptFirstCandidate(ctx, jobLogger, candidates, adopt, filepath.Join(subtitleDir, key+".mkv"))
	if err != nil {
		return subtitleOutcomeFailed, err
	}
	if adopted == nil {
		return subtitleOutcomeSkipped, recordSubtitleSkip(sess, key, "no downloaded subtitle passed verification")
	}

	logSubtitleValidation(logger, key, adopted.Validation)
	applySubtitleReviewIssues(logger, sess, key, adopted.Validation)

	record := ripspec.SubtitleGenRecord{
		EpisodeKey:       key,
		Source:           "opensubtitles",
		SubtitlePath:     adopted.DisplayPath,
		Segments:         adopted.Segments,
		DurationSec:      adopt.VideoSeconds,
		Language:         adopted.Language,
		ValidationResult: subtitleValidationResult(adopted.Validation),
		QCObservations:   adopted.Validation.Issues,
		ReviewIssues:     adopted.Validation.ReviewIssues,
	}
	if err := sess.MergeSave(func(env *ripspec.Envelope) error {
		upsertSubtitleGenRecord(&env.Attributes.SubtitleGenerationResults, record)
		return nil
	}); err != nil {
		return subtitleOutcomeFailed, err
	}

	logger.Info("subtitle adopted from download",
		"decision_type", logs.DecisionSubtitleSource,
		"decision_result", subtitleOutcomeAdopted,
		"decision_reason", fmt.Sprintf("verified against WhisperX transcript (%s)", adopted.Check.Metrics()),
		"episode_key", key,
		"candidate", adopted.Candidate.label(),
		"subtitle_path", adopted.DisplayPath,
		"segments", adopted.Segments,
	)
	return subtitleOutcomeAdopted, nil
}

// recordSubtitleSkip records the no-subtitle outcome for an episode. The item
// completes normally; the warning is the operator's cue to run the manual
// `spindle subtitle` command, and itemaudit surfaces it as an anomaly.
func recordSubtitleSkip(sess *stage.Session, key, reason string) error {
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

func startSubtitleJob(sess *stage.Session, job stage.AssetJob) {
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
	sess.Progress(job.Percent(5), job.PhaseMessage("Preparing subtitles ("+key+")"))
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
	stagingRoot, err := sess.StagingRoot(h.cfg.Paths.StagingDir)
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
		sess.Progress(job.Percent(subtitlePhasePercent(phase, elapsed)), message)
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

func logSubtitleValidation(logger *slog.Logger, key string, validation validationResult) {
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

func applySubtitleReviewIssues(logger *slog.Logger, sess *stage.Session, key string, validation validationResult) {
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
		persistReviewReason(logger, sess, key, "Subtitle validation: "+issue, "srt_validation: "+issue+" ("+key+")")
	}
}

// persistReviewReason appends envReason to the episode matching key and
// queueReason to the queue item, logging persistence failures without
// failing the stage.
func persistReviewReason(logger *slog.Logger, sess *stage.Session, key, envReason, queueReason string) {
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

func recordSubtitleFailure(logger *slog.Logger, sess *stage.Session, key, errMsg string) {
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
		return nil
	}); mergeErr != nil {
		logger.Error("subtitle failure persistence failed",
			"event_type", "subtitle_failure_persist_failed",
			"error", mergeErr,
		)
	}
	persistReviewReason(logger, sess, key, "Subtitle generation failed: "+errMsg, "subtitle_failure: "+errMsg+" ("+key+")")
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
