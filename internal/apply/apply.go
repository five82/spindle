// Package apply owns all encoded-file rewrites after the pipeline branches
// join, preventing audio and subtitle remux operations from racing.
package apply

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/fileutil"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/srtutil"
	"github.com/five82/spindle/internal/stage"
)

// Handler owns the apply stage. It serializes audio refinement, commentary
// disposition, duration validation, and subtitle muxing after the encoding
// and analysis branches join, then verifies the files the organizer will
// deliver. Keeping every encoded-file writer here prevents concurrent
// in-place rewrites of the same MKV, and makes this the only stage that can
// see the finished output.
type Handler struct {
	cfg *config.Config
}

// New creates an apply-stage handler.
func New(cfg *config.Config) *Handler {
	return &Handler{cfg: cfg}
}

// Run executes the apply stage.
func (h *Handler) Run(ctx context.Context, sess *stage.Session) error {
	logger := sess.Logger
	env := sess.Env

	inputs := sess.CompletedAssetJobs(ripspec.AssetKindEncoded)
	if len(inputs) == 0 {
		return fmt.Errorf("no encoded assets available for apply")
	}

	analysisData := env.Attributes.AudioAnalysis
	if analysisData == nil {
		analysisData = &ripspec.AudioAnalysisData{}
	}
	// Snapshot the recorded primary audio index before this stage overwrites
	// it with the post-refinement one: the final A/V sync comparison needs the
	// index as it applies to the ripped source.
	sourceAudioIndex := -1
	if env.Attributes.AudioAnalysis != nil {
		sourceAudioIndex = env.Attributes.AudioAnalysis.PrimaryTrack.Index
	}

	// Phase 1: per-file audio refinement and commentary disposition, using
	// the episode's own commentary indices from the analysis stage.
	sess.Progress(10, "Phase 1/4 - Audio refinement")
	logger.Info("Phase 1/4 - Audio refinement")
	var aggregateComms []ripspec.CommentaryTrackRef
	expectations := make([]finalExpectation, 0, len(inputs))
	for i, in := range inputs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var comms []ripspec.CommentaryTrackRef
		epAnalysis := analysisData.EpisodeAnalysis(in.Key)
		if epAnalysis != nil {
			comms = epAnalysis.CommentaryTracks
		} else if len(analysisData.PerEpisode) == 0 {
			// No per-episode data (single-file movies recorded pre-split, or
			// commentary disabled): fall back to the aggregate list.
			comms = analysisData.CommentaryTracks
		}
		var keep []int
		for _, c := range comms {
			keep = append(keep, c.Index)
		}

		refinement, refErr := refineAudioTargets(ctx, logger, []string{in.Input.Path}, keep)
		if refErr != nil {
			logger.Warn("audio refinement failed",
				"event_type", "audio_refinement_error",
				"error_hint", refErr.Error(),
				"impact", "unrefined audio shipped; episode routed to review",
				"episode_key", in.Key,
			)
			if err := flagForReview(sess, in.Key, "audio_refinement: "+refErr.Error()); err != nil {
				return err
			}
			refinement = nil
		}

		primary, primaryLabel, remapped, err := applyPostRefinementAudio(ctx, logger, in.Input.Path, refinement, comms)
		if err != nil {
			return err
		}
		if epAnalysis != nil {
			epAnalysis.CommentaryTracks = remapped
		}
		aggregateComms = append(aggregateComms, remapped...)

		// Record what this file was supposed to end up with. Commentary
		// disposition only runs when refinement produced a plan, so an
		// unrefined file expects no comment flags at all.
		expectation := finalExpectation{key: in.Key, encodedPath: in.Input.Path}
		if refinement != nil {
			expectation.keptAudio = len(refinement.KeptIndices)
			for _, ref := range remapped {
				expectation.commentary = append(expectation.commentary, ref.Index)
			}
		}
		expectations = append(expectations, expectation)
		if i == 0 {
			analysisData.PrimaryTrack = primary
			if refinement != nil && refinement.PrimaryAudioDescription != "" {
				analysisData.PrimaryDescription = refinement.PrimaryAudioDescription
			}
			if analysisData.PrimaryDescription == "" {
				analysisData.PrimaryDescription = primaryLabel
			}
		}
	}
	analysisData.CommentaryTracks = aggregateComms

	// Phase 2: duration validation across all encoded outputs.
	sess.Progress(45, "Phase 2/4 - Audio validation")
	logger.Info("Phase 2/4 - Audio validation")
	var allPaths []string
	for _, in := range inputs {
		allPaths = append(allPaths, in.Input.Path)
	}
	encodedDurations, durErr := validateAudioTargetDurations(ctx, allPaths)
	for i := range expectations {
		expectations[i].encodedDuration = encodedDurations[expectations[i].encodedPath]
	}
	if durErr != nil {
		reason := "audio_validation: " + durErr.Error()
		sess.AddReviewReason(reason)
		logger.Warn("audio validation failed",
			"event_type", "audio_validation_failed",
			"error_hint", durErr.Error(),
			"impact", "item routed to review",
		)
		logger.Info("validation failure flagged for review",
			"decision_type", logs.DecisionValidationFailureRoute,
			"decision_result", "flagged_for_review",
			"decision_reason", "audio duration validation did not pass",
		)
	}

	// Phase 3: subtitle placement and muxing from the analysis branch's
	// generated SRTs.
	sess.Progress(70, "Phase 3/4 - Subtitle muxing")
	logger.Info("Phase 3/4 - Subtitle muxing")
	if h.cfg.Subtitles.Enabled {
		for _, in := range inputs {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := h.applySubtitles(ctx, sess, in.Key, in.Input.Path); err != nil {
				return err
			}
		}
	} else {
		logger.Info("subtitle muxing skipped",
			"decision_type", logs.DecisionSubtitleMux,
			"decision_result", "skipped",
			"decision_reason", "subtitles.enabled = false",
		)
	}

	// Phase 4: probe what the organizer will actually deliver. This is the
	// only check that sees the file after every rewrite, so it owns the
	// A/V sync, subtitle layout, commentary label, and audio layout
	// invariants for the delivered output.
	sess.Progress(85, "Phase 4/4 - Final validation")
	logger.Info("Phase 4/4 - Final validation")
	verdict, err := verifyFinalOutputs(ctx, sess, expectations, sourceAudioIndex)
	if err != nil {
		return err
	}

	env.Attributes.AudioAnalysis = analysisData
	env.Attributes.FinalValidation = verdict
	sess.Progress(95, "Phase 4/4 - Persisting results")
	return sess.Save()
}

// applySubtitles places the episode's generated SRT next to the encoded
// file and muxes it when configured, recording the subtitled asset. A
// missing, skipped (Source "none"), or severe-issue generation record means
// the episode has no subtitle; the subtitle stage already logged why, so
// apply just skips it.
func (h *Handler) applySubtitles(ctx context.Context, sess *stage.Session, key, encodedPath string) error {
	logger := sess.Logger
	record := findSubtitleGenRecord(sess.Env, key)
	if record == nil || len(record.SevereIssues) > 0 || strings.TrimSpace(record.SubtitlePath) == "" {
		logger.Info("subtitle apply skipped",
			"decision_type", logs.DecisionSubtitleMux,
			"decision_result", "skipped",
			"decision_reason", "no usable subtitle generation record",
			"episode_key", key,
		)
		return nil
	}
	if _, err := os.Stat(record.SubtitlePath); err != nil {
		logger.Warn("generated subtitle missing",
			"event_type", "subtitle_apply_error",
			"error_hint", err.Error(),
			"impact", "episode has no subtitle",
			"episode_key", key,
		)
		return nil
	}

	// Place the sidecar next to the encoded file so the organizer's sidecar
	// glob finds it when muxing is disabled.
	sidecarPath := srtutil.DisplaySubtitlePath(encodedPath, record.Language)
	if err := fileutil.CopyFile(record.SubtitlePath, sidecarPath); err != nil {
		return fmt.Errorf("place subtitle sidecar %s: %w", key, err)
	}

	subtitledPath := encodedPath
	subtitlesMuxed := false
	if h.cfg.Subtitles.MuxIntoMKV {
		muxedPath, err := muxDisplaySubtitle(ctx, logger, encodedPath, sidecarPath, key, record.Language)
		if err != nil {
			// Loom ignores sidecar SRTs, so the fallback ships a library file
			// with no visible subtitles: route it to review rather than
			// letting it pass as a clean import.
			logger.Warn("subtitle mux failed",
				"event_type", "mux_error",
				"error_hint", err.Error(),
				"impact", "subtitle remains a sidecar Loom cannot see; episode routed to review",
				"episode_key", key,
			)
			if mergeErr := flagForReview(sess, key, "subtitle_mux: "+err.Error()); mergeErr != nil {
				return mergeErr
			}
		} else {
			subtitledPath = muxedPath
			subtitlesMuxed = true
		}
	} else {
		logger.Info("subtitle mux skipped",
			"decision_type", logs.DecisionSubtitleMux,
			"decision_result", "skipped",
			"decision_reason", "mux_into_mkv is disabled",
			"episode_key", key,
		)
	}

	return sess.SaveAssetSuccess(ripspec.AssetKindSubtitled, ripspec.Asset{
		EpisodeKey:     key,
		Path:           subtitledPath,
		SubtitlesMuxed: subtitlesMuxed,
	})
}

// flagForReview records reason against the episode and against the queue
// item. The item-level copy carries the episode key, because that is the
// string the review directory is named after.
func flagForReview(sess *stage.Session, key, reason string) error {
	itemReason := reason
	if sess.AddEpisodeReviewReason(key, reason) {
		itemReason = reason + " (" + key + ")"
	}
	return sess.MergeAddReviewReason(itemReason)
}

// findSubtitleGenRecord returns the generation record for key, or nil.
func findSubtitleGenRecord(env *ripspec.Envelope, key string) *ripspec.SubtitleGenRecord {
	records := env.Attributes.SubtitleGenerationResults
	for i := range records {
		if strings.EqualFold(records[i].EpisodeKey, key) {
			return &records[i]
		}
	}
	return nil
}
