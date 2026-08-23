package subtitle

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/srtutil"
	"github.com/five82/spindle/internal/transcription"
)

var inspectSubtitleMedia = ffprobe.Inspect

// adoptContext carries the per-job inputs candidate adoption needs.
type adoptContext struct {
	ReferenceSRTPath string
	ReferenceCues    []srtutil.Cue
	// ReferenceWords is the transcript's aligned word timestamps for the
	// word-snap pass; nil (audio.json unavailable) skips the pass.
	ReferenceWords []transcription.Word
	VideoSeconds   float64
	WorkDir        string
}

// buildAdoptContext assembles the candidate-evaluation inputs from the sync
// reference transcript and the video being subtitled.
func buildAdoptContext(ctx context.Context, logger *slog.Logger, reference *transcription.TranscribeResult, videoPath, workDir string) (adoptContext, error) {
	referenceCues, err := srtutil.ParseFile(reference.SRTPath)
	if err != nil {
		return adoptContext{}, fmt.Errorf("read sync reference transcript: %w", err)
	}
	videoSeconds, durationSource := resolveSubtitleVideoDuration(ctx, logger, videoPath, reference.Duration)
	logger.Info("subtitle duration selected",
		"decision_type", "subtitle_duration_source",
		"decision_result", durationSource,
		"decision_reason", fmt.Sprintf("video_seconds=%.3f transcript_seconds=%.3f", videoSeconds, reference.Duration),
	)
	return adoptContext{
		ReferenceSRTPath: reference.SRTPath,
		ReferenceCues:    referenceCues,
		ReferenceWords:   loadReferenceWords(logger, reference.JSONPath),
		VideoSeconds:     videoSeconds,
		WorkDir:          workDir,
	}, nil
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

// adoption is the outcome of a successful candidate adoption: verified cues
// written to DisplayPath as the video's sidecar SRT.
type adoption struct {
	DisplayPath string
	Language    string
	Candidate   subtitleCandidate
	Check       adoptionCheck
	Validation  validationResult
	Segments    int
}

// adoptFirstCandidate evaluates candidates in preference order and writes the
// first one that passes the gate as videoPath's display sidecar. A nil
// adoption with nil error means every candidate was rejected (reasons in
// rejected); errors are hard failures that abort the caller.
func (h *Handler) adoptFirstCandidate(ctx context.Context, logger *slog.Logger, candidates []subtitleCandidate, adopt adoptContext, videoPath string) (*adoption, []string, error) {
	var rejected []string
	for _, candidate := range candidates {
		eval, err := h.evaluateCandidate(ctx, candidate, adopt)
		if err != nil {
			return nil, rejected, err
		}
		logger.Debug("subtitle candidate cleaned",
			"event_type", "subtitle_candidate_cleaned",
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
				"candidate", candidate.label(),
			)
			rejected = append(rejected, candidate.label()+": "+eval.RejectReason)
			continue
		}
		lang := candidate.Language
		if lang == "" {
			lang = "en"
		}
		displayPath := srtutil.DisplaySubtitlePath(videoPath, lang)
		if err := writeSRTAtomic(displayPath, eval.Cues); err != nil {
			return nil, rejected, fmt.Errorf("write adopted subtitle: %w", err)
		}
		return &adoption{
			DisplayPath: displayPath,
			Language:    lang,
			Candidate:   candidate,
			Check:       eval.Check,
			Validation:  eval.Validation,
			Segments:    len(eval.Cues),
		}, rejected, nil
	}
	return nil, rejected, nil
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
