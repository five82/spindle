// Package subtitle implements the subtitle generation stage (Layer 4).
//
// Subtitle generation: canonical WhisperX transcription reuse, hallucination
// filtering, Stable-TS display formatting, SRT validation, MKV muxing, and
// resume support.
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
	"github.com/five82/spindle/internal/media/ffprobe"
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
}

// New creates a subtitle handler.
func New(cfg *config.Config, transcriber *transcription.Service) *Handler {
	return &Handler{
		cfg:         cfg,
		transcriber: transcriber,
	}
}

// DisplaySubtitleError reports which display-subtitle generation step failed.
type DisplaySubtitleError struct {
	Op  string
	Err error
}

func (e *DisplaySubtitleError) Error() string { return fmt.Sprintf("%s: %v", e.Op, e.Err) }
func (e *DisplaySubtitleError) Unwrap() error { return e.Err }

// GenerateDisplaySubtitleRequest describes one display subtitle generation run.
type GenerateDisplaySubtitleRequest struct {
	VideoPath       string
	DisplayBasePath string
	DisplayPath     string
	WorkDir         string
	Language        string
	ItemID          int64
	EpisodeKey      string
	Purpose         string
	Transcriber     interface {
		SelectPrimaryAudioTrack(context.Context, string, string) (transcription.SelectedAudio, error)
		Transcribe(context.Context, transcription.TranscribeRequest, ...transcription.ProgressFunc) (*transcription.TranscribeResult, error)
	}
	// Transcript, when non-nil, is a pre-existing canonical WhisperX result
	// (the shared per-episode transcript artifact) reused instead of running
	// WhisperX again. Audio selection still runs for language and labeling.
	Transcript              *transcription.TranscribeResult
	Progress                transcription.ProgressFunc
	OnAudioSelected         func(transcription.SelectedAudio)
	OnTranscriptionComplete func(*transcription.TranscribeResult)
	OnDurationSelected      func(videoSeconds float64, source string, transcriptSeconds float64)
	OnFormattingStart       func()
	OnFormattingComplete    func(FormatResult)
}

// GenerateDisplaySubtitleResult describes the generated primary display SRT.
type GenerateDisplaySubtitleResult struct {
	SelectedAudio  transcription.SelectedAudio
	Formatting     FormatResult
	VideoSeconds   float64
	DurationSource string

	formatting formatResult
}

// GenerateDisplaySubtitle selects primary audio, creates canonical WhisperX
// artifacts, resolves video duration, and formats the primary display SRT.
func GenerateDisplaySubtitle(ctx context.Context, req GenerateDisplaySubtitleRequest) (*GenerateDisplaySubtitleResult, error) {
	if req.Transcriber == nil {
		return nil, fmt.Errorf("generate display subtitle: nil transcriber")
	}
	if strings.TrimSpace(req.VideoPath) == "" {
		return nil, fmt.Errorf("generate display subtitle: missing video path")
	}
	if strings.TrimSpace(req.WorkDir) == "" {
		return nil, fmt.Errorf("generate display subtitle: missing work dir")
	}

	preferredLanguage := req.Language
	if preferredLanguage == "" {
		preferredLanguage = "en"
	}
	selectedAudio, err := req.Transcriber.SelectPrimaryAudioTrack(ctx, req.VideoPath, preferredLanguage)
	if err != nil {
		return nil, &DisplaySubtitleError{Op: "select audio", Err: err}
	}
	if req.OnAudioSelected != nil {
		req.OnAudioSelected(selectedAudio)
	}

	transcript := req.Transcript
	if transcript == nil {
		transcript, err = req.Transcriber.Transcribe(ctx, transcription.TranscribeRequest{
			InputPath:  req.VideoPath,
			AudioIndex: selectedAudio.Index,
			Language:   selectedAudio.Language,
			OutputDir:  req.WorkDir,
			ItemID:     req.ItemID,
			EpisodeKey: req.EpisodeKey,
			Purpose:    req.Purpose,
		}, req.Progress)
		if err != nil {
			return nil, &DisplaySubtitleError{Op: "transcribe", Err: err}
		}
	}
	if req.OnTranscriptionComplete != nil {
		req.OnTranscriptionComplete(transcript)
	}

	videoSeconds, durationSource := resolveSubtitleVideoDuration(ctx, req.VideoPath, transcript.Duration)
	if req.OnDurationSelected != nil {
		req.OnDurationSelected(videoSeconds, durationSource, transcript.Duration)
	}

	displayPath := req.DisplayPath
	if displayPath == "" {
		displayBasePath := req.DisplayBasePath
		if displayBasePath == "" {
			displayBasePath = req.VideoPath
		}
		displayPath = displaySubtitlePath(displayBasePath, selectedAudio.Language)
	}
	if req.OnFormattingStart != nil {
		req.OnFormattingStart()
	}
	formatting, err := formatSubtitleFromCanonical(ctx, transcriptionArtifacts{JSONPath: transcript.JSONPath}, req.WorkDir, displayPath, videoSeconds, selectedAudio.Language)
	if err != nil {
		return nil, &DisplaySubtitleError{Op: "format subtitle", Err: err}
	}
	publicFormatting := FormatResult{
		DisplayPath:      formatting.DisplayPath,
		OriginalSegments: formatting.OriginalSegments,
		FilteredSegments: formatting.FilteredSegments,
		SplitCues:        formatting.SplitCues,
		WrappedCues:      formatting.WrappedCues,
		RetimedCues:      formatting.RetimedCues,
	}
	if req.OnFormattingComplete != nil {
		req.OnFormattingComplete(publicFormatting)
	}

	return &GenerateDisplaySubtitleResult{
		SelectedAudio:  selectedAudio,
		Formatting:     publicFormatting,
		VideoSeconds:   videoSeconds,
		DurationSource: durationSource,
		formatting:     formatting,
	}, nil
}

// Run executes the subtitle generation stage.
func (h *Handler) Run(ctx context.Context, sess *stage.Session) error {
	logger := sess.Logger
	logger.Info("subtitle stage started", "event_type", "stage_start", "stage", "subtitling")

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

	summary, err := h.processSubtitleJobs(ctx, sess, jobs)
	if err != nil {
		return err
	}

	return h.finishSubtitleStage(sess, summary)
}

type subtitleRunSummary struct {
	attempted int
	succeeded int
	failed    int
}

func (h *Handler) planSubtitleJobs(sess *stage.Session) ([]stage.AssetJob, []string) {
	return sess.PendingKeyedAssetJobs(ripspec.AssetKindEncoded, ripspec.AssetKindSubtitled)
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

func (h *Handler) processSubtitleJobs(ctx context.Context, sess *stage.Session, jobs []stage.AssetJob) (subtitleRunSummary, error) {
	var summary subtitleRunSummary
	for _, job := range jobs {
		if ctx.Err() != nil {
			return summary, ctx.Err()
		}

		summary.attempted++
		succeeded, err := h.processSubtitleJob(ctx, sess, job)
		if err != nil {
			return summary, err
		}
		if succeeded {
			summary.succeeded++
		} else {
			summary.failed++
		}
	}
	return summary, nil
}

func (h *Handler) processSubtitleJob(ctx context.Context, sess *stage.Session, job stage.AssetJob) (bool, error) {
	logger := sess.Logger
	key := job.Key
	asset := job.Input

	h.startSubtitleJob(sess, job)

	result, err := h.generateDisplaySubtitle(ctx, sess, job)
	if err != nil {
		h.recordSubtitleFailure(logger, sess, key, err.Error())
		return false, nil
	}

	record, err := h.createDisplaySubtitleRecord(sess, job, result)
	if err != nil {
		h.recordSubtitleFailure(logger, sess, key, err.Error())
		return false, nil
	}
	if len(record.SevereIssues) > 0 {
		severeReason := strings.Join(record.SevereIssues, ", ")
		upsertSubtitleGenRecord(&sess.Env.Attributes.SubtitleGenerationResults, record)
		logger.Warn("subtitle validation failed",
			"decision_type", logs.DecisionSRTValidation,
			"decision_result", "failed",
			"decision_reason", severeReason,
			"episode_key", key,
			"impact", "subtitle job failed; mux skipped",
		)
		h.recordSubtitleFailure(logger, sess, key, "severe subtitle validation: "+severeReason)
		return false, nil
	}

	upsertSubtitleGenRecord(&sess.Env.Attributes.SubtitleGenerationResults, record)

	subtitledPath, subtitlesMuxed := h.resolveSubtitledOutput(ctx, logger, asset.Path, record.SubtitlePath, key, result.SelectedAudio.Language)
	if err := h.saveSubtitleJobSuccess(logger, sess, job, subtitledPath, subtitlesMuxed); err != nil {
		return false, err
	}
	return true, nil
}

func (h *Handler) startSubtitleJob(sess *stage.Session, job stage.AssetJob) {
	logger := sess.Logger
	item := sess.Item
	key := job.Key

	logger.Info("encoded asset selected for transcription",
		"decision_type", logs.DecisionTranscriptionAsset,
		"decision_result", job.Input.Path,
		"decision_reason", fmt.Sprintf("episode_key=%s", key),
	)

	item.ProgressMessage = job.PhaseMessage("Generating subtitles (" + key + ")")
	logger.Info(item.ProgressMessage,
		"event_type", "subtitle_start",
	)
	_ = sess.Progress(job.Percent(0), item.ProgressMessage, stage.WithActiveEpisode(key))
}

func (h *Handler) generateDisplaySubtitle(ctx context.Context, sess *stage.Session, job stage.AssetJob) (*GenerateDisplaySubtitleResult, error) {
	item := sess.Item
	asset := job.Input
	key := job.Key
	workDir := filepath.Join(os.TempDir(), fmt.Sprintf("spindle-subtitle-%s-%s", item.DiscFingerprint, key))

	return GenerateDisplaySubtitle(ctx, GenerateDisplaySubtitleRequest{
		VideoPath:   asset.Path,
		WorkDir:     workDir,
		Language:    "en",
		ItemID:      item.ID,
		EpisodeKey:  key,
		Purpose:     "subtitle_generation",
		Transcript:  transcriptArtifact(sess, key),
		Transcriber: h.transcriber,
		Progress: func(phase transcription.Phase, elapsed time.Duration) {
			message := item.ProgressMessage
			switch phase {
			case transcription.PhaseExtract:
				if elapsed == 0 {
					message = job.PhaseMessage("Extracting audio (" + key + ")")
				}
			case transcription.PhaseTranscribe:
				if elapsed == 0 {
					message = job.PhaseMessage("Transcribing audio (" + key + ")")
				}
			}
			_ = sess.Progress(job.Percent(subtitlePhasePercent(phase, elapsed)), message)
		},
		OnTranscriptionComplete: func(result *transcription.TranscribeResult) {
			sess.Logger.Info("transcription complete",
				"event_type", "transcription_complete",
				"episode_key", key,
				"segments", result.Segments,
				"content_duration_s", result.Duration,
				"extract_time_ms", result.ExtractTime.Milliseconds(),
				"transcribe_time_ms", result.TranscribeTime.Milliseconds(),
			)
		},
		OnDurationSelected: func(videoSeconds float64, source string, transcriptSeconds float64) {
			sess.Logger.Info("subtitle duration selected",
				"decision_type", "subtitle_duration_source",
				"decision_result", source,
				"decision_reason", fmt.Sprintf("video_seconds=%.3f transcript_seconds=%.3f", videoSeconds, transcriptSeconds),
				"episode_key", key,
			)
		},
	})
}

// transcriptArtifact returns the episode's shared WhisperX transcript
// artifact (recorded by episode identification, or commentary analysis for
// movies) when both its SRT and JSON still exist, so subtitle generation can
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

func (h *Handler) createDisplaySubtitleRecord(sess *stage.Session, job stage.AssetJob, result *GenerateDisplaySubtitleResult) (ripspec.SubtitleGenRecord, error) {
	logger := sess.Logger
	key := job.Key
	formatting := result.formatting

	h.logSubtitleFormatting(logger, key, formatting)

	formattedCues, readErr := srtutil.ParseFile(formatting.DisplayPath)
	if readErr != nil {
		return ripspec.SubtitleGenRecord{}, fmt.Errorf("read formatted subtitle: %w", readErr)
	}
	if len(formattedCues) == 0 {
		return ripspec.SubtitleGenRecord{}, fmt.Errorf("formatted subtitle produced zero cues")
	}

	validation := validateCuesDetailed(formattedCues, result.VideoSeconds)
	h.logSubtitleValidation(logger, key, validation, formatting)
	h.applySubtitleReviewIssues(logger, sess, key, validation)

	record := ripspec.SubtitleGenRecord{
		EpisodeKey:       key,
		Source:           "whisperx",
		SubtitlePath:     formatting.DisplayPath,
		Segments:         len(formattedCues),
		DurationSec:      result.VideoSeconds,
		Language:         result.SelectedAudio.Language,
		ValidationResult: subtitleValidationResult(validation),
		QCObservations:   validation.Issues,
		ReviewIssues:     validation.ReviewIssues,
		SevereIssues:     validation.SevereIssues,
	}

	return record, nil
}

func (h *Handler) logSubtitleFormatting(logger *slog.Logger, key string, formatting formatResult) {
	logger.Info("subtitle formatting complete",
		"decision_type", logs.DecisionSubtitleFormatting,
		"decision_result", formatting.FormatterDecision,
		"decision_reason", fmt.Sprintf("original_segments=%d filtered_segments=%d text_rules_removed=%d heuristic_removed=%d split_cues=%d wrapped_cues=%d retimed_cues=%d", formatting.OriginalSegments, formatting.FilteredSegments, formatting.RemovedByTextRules, formatting.RemovedBySegmentHeuristics, formatting.SplitCues, formatting.WrappedCues, formatting.RetimedCues),
		"episode_key", key,
		"subtitle_file", formatting.DisplayPath,
	)
	logger.Info("hallucination filter applied",
		"decision_type", logs.DecisionHallucinationFilter,
		"decision_result", "filtered",
		"decision_reason", fmt.Sprintf("original=%d filtered=%d text_rules_removed=%d heuristic_removed=%d", formatting.OriginalSegments, formatting.FilteredSegments, formatting.RemovedByTextRules, formatting.RemovedBySegmentHeuristics),
		"episode_key", key,
	)
}

func (h *Handler) logSubtitleValidation(logger *slog.Logger, key string, validation validationResult, formatting formatResult) {
	stats := validation.Stats
	logger.Info("SRT validation QC summary",
		"decision_type", logs.DecisionSRTValidation,
		"decision_result", "qc_summary",
		"decision_reason", fmt.Sprintf("cue_count=%d max_cps=%.2f p95_cps=%.2f high_cps_cues=%d short_duration_cues=%d long_duration_cues=%d overlong_line_cues=%d unbalanced_line_break_cues=%d split_cues=%d wrapped_cues=%d retimed_cues=%d", stats.CueCount, stats.MaxCPS, stats.P95CPS, stats.HighCPSCues, stats.ShortDurationCues, stats.LongDurationCues, stats.OverlongLineCues, stats.UnbalancedLineBreakCues, formatting.SplitCues, formatting.WrappedCues, formatting.RetimedCues),
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
		"split_cues", formatting.SplitCues,
		"wrapped_cues", formatting.WrappedCues,
		"retimed_cues", formatting.RetimedCues,
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
			logger.Info("SRT validation observation",
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
		sess.AddEpisodeReviewReason(key, "Subtitle validation: "+issue)
		sess.AddReviewReason("srt_validation: " + issue + " (" + key + ")")
	}
}

func (h *Handler) resolveSubtitledOutput(ctx context.Context, logger *slog.Logger, assetPath, srtPath, key, language string) (string, bool) {
	subtitledPath := assetPath
	subtitlesMuxed := false
	if !h.cfg.Subtitles.MuxIntoMKV {
		logger.Info("subtitle mux skipped",
			"decision_type", logs.DecisionSubtitleMux,
			"decision_result", "skipped",
			"decision_reason", "mux_into_mkv is disabled",
		)
		return subtitledPath, subtitlesMuxed
	}

	muxedPath, err := h.muxSubtitles(ctx, logger, assetPath, srtPath, key, language)
	if err != nil {
		logger.Warn("subtitle mux failed",
			"event_type", "mux_error",
			"error_hint", err.Error(),
			"impact", "subtitle remains as sidecar",
		)
		return subtitledPath, subtitlesMuxed
	}
	return muxedPath, true
}

func (h *Handler) saveSubtitleJobSuccess(logger *slog.Logger, sess *stage.Session, job stage.AssetJob, subtitledPath string, subtitlesMuxed bool) error {
	key := job.Key
	if err := sess.Progress(job.CompletionPercent(), job.PhaseMessage("Generated subtitles ("+key+")")); err != nil {
		logger.Warn("progress persistence failed",
			"event_type", "progress_persist_failed",
			"error_hint", "subtitle completion progress not persisted",
			"impact", "subtitle progress not reflected in queue",
			"error", err,
		)
	}
	return sess.SaveAssetSuccess(ripspec.AssetKindSubtitled, ripspec.Asset{
		EpisodeKey:     key,
		Path:           subtitledPath,
		SubtitlesMuxed: subtitlesMuxed,
	})
}

func (h *Handler) finishSubtitleStage(sess *stage.Session, summary subtitleRunSummary) error {
	if err := sess.Save(); err != nil {
		return err
	}
	if summary.attempted > 0 && summary.succeeded == 0 && summary.failed > 0 {
		return fmt.Errorf("all %d subtitle job(s) failed", summary.attempted)
	}

	sess.Logger.Info("subtitle stage completed",
		"event_type", "stage_complete",
		"stage", "subtitling",
		"attempted", summary.attempted,
		"succeeded", summary.succeeded,
		"failed", summary.failed,
	)
	return nil
}

func resolveSubtitleVideoDuration(ctx context.Context, videoPath string, fallback float64) (seconds float64, source string) {
	probe, err := inspectSubtitleMedia(ctx, "", videoPath)
	if err == nil {
		if duration := probe.DurationSeconds(); duration > 0 {
			return duration, "media_probe"
		}
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
	sess.RecordAssetFailure(ripspec.AssetKindSubtitled, key, errMsg)
	sess.AddEpisodeReviewReason(key, "Subtitle generation failed: "+errMsg)
	sess.AddReviewReason("subtitle_failure: " + errMsg + " (" + key + ")")
	_ = sess.Save()
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

// muxSubtitles runs mkvmerge to add an SRT subtitle track to the MKV file.
// It writes to a temp file and renames on success.
func (h *Handler) muxSubtitles(
	ctx context.Context,
	logger *slog.Logger,
	videoPath string,
	srtPath string,
	key string,
	subtitleLanguage string,
) (string, error) {
	dir := filepath.Dir(videoPath)
	ext := filepath.Ext(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), ext)
	outPath := filepath.Join(dir, base+".subtitled"+ext)

	logger.Info("subtitle mux started",
		"event_type", "mux_start",
		"episode_key", key,
		"video_path", videoPath,
		"subtitle_path", srtPath,
		"output_path", outPath,
	)
	muxStart := time.Now()
	muxedPath, err := MuxSubtitleTrack(ctx, MuxRequest{
		VideoPath:  videoPath,
		OutputPath: outPath,
		Track:      MuxTrack{Path: srtPath, Language: subtitleLanguage},
	})
	if err != nil {
		return "", fmt.Errorf("mux subtitles %s: %w", key, err)
	}

	logger.Info("subtitles muxed into MKV",
		"event_type", "mux_complete",
		"episode_key", key,
		"output_path", muxedPath,
		"duration_ms", time.Since(muxStart).Milliseconds(),
	)

	return muxedPath, nil
}
