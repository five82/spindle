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
	"github.com/five82/spindle/internal/stage"
)

// Handler owns the apply stage. It serializes audio refinement, commentary
// disposition, duration validation, and subtitle muxing after the encoding
// and analysis branches join. Keeping every encoded-file writer here prevents
// concurrent in-place rewrites of the same MKV.
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
	logger.Info("apply stage started", "event_type", "stage_start", "stage", "apply")
	env := sess.Env

	keys := env.AssetKeys()
	type encodedInput struct {
		key  string
		path string
	}
	var inputs []encodedInput
	for _, key := range keys {
		asset, ok := env.Assets.FindAsset(ripspec.AssetKindEncoded, key)
		if ok && asset.IsCompleted() {
			inputs = append(inputs, encodedInput{key: key, path: asset.Path})
		}
	}
	if len(inputs) == 0 {
		return fmt.Errorf("no encoded assets available for apply")
	}

	analysisData := env.Attributes.AudioAnalysis
	if analysisData == nil {
		analysisData = &ripspec.AudioAnalysisData{}
	}

	// Phase 1: per-file audio refinement and commentary disposition, using
	// the episode's own commentary indices from the analysis stage.
	_ = sess.Progress(10, "Phase 1/3 - Audio refinement")
	logger.Info("Phase 1/3 - Audio refinement")
	var aggregateComms []ripspec.CommentaryTrackRef
	for i, in := range inputs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var comms []ripspec.CommentaryTrackRef
		epAnalysis := analysisData.EpisodeAnalysis(in.key)
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

		refinement, refErr := refineAudioTargets(ctx, logger, []string{in.path}, keep)
		if refErr != nil {
			logger.Warn("audio refinement failed",
				"event_type", "audio_refinement_error",
				"error_hint", refErr.Error(),
				"impact", "audio refinement skipped, proceeding with all tracks",
				"episode_key", in.key,
			)
			refinement = nil
		}

		primary, primaryLabel, remapped, err := applyPostRefinementAudio(ctx, logger, in.path, refinement, comms)
		if err != nil {
			return err
		}
		if epAnalysis != nil {
			epAnalysis.CommentaryTracks = remapped
		}
		aggregateComms = append(aggregateComms, remapped...)
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
	_ = sess.Progress(45, "Phase 2/3 - Audio validation")
	logger.Info("Phase 2/3 - Audio validation")
	var allPaths []string
	for _, in := range inputs {
		allPaths = append(allPaths, in.path)
	}
	if err := validateAudioTargetDurations(ctx, allPaths); err != nil {
		reason := "audio_validation: " + err.Error()
		sess.AddReviewReason(reason)
		logger.Warn("audio validation failed",
			"event_type", "audio_validation_failed",
			"error_hint", err.Error(),
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
	_ = sess.Progress(75, "Phase 3/3 - Subtitle muxing")
	logger.Info("Phase 3/3 - Subtitle muxing")
	if h.cfg.Subtitles.Enabled {
		for _, in := range inputs {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := h.applySubtitles(ctx, sess, in.key, in.path); err != nil {
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

	env.Attributes.AudioAnalysis = analysisData
	_ = sess.Progress(95, "Phase 3/3 - Persisting results")
	if err := sess.Save(); err != nil {
		return err
	}

	logger.Info("apply stage completed",
		"event_type", "stage_complete",
		"stage", "apply",
		"primary_audio_index", analysisData.PrimaryTrack.Index,
		"primary_audio", analysisData.PrimaryDescription,
		"commentary_tracks", len(analysisData.CommentaryTracks),
		"encoded_assets", len(inputs),
	)
	return nil
}

// applySubtitles places the episode's generated SRT next to the encoded
// file and muxes it when configured, recording the subtitled asset. A
// missing or severe-issue generation record means the episode has no
// subtitle: the generation stage already flagged it for review, so apply
// just skips it.
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
	// glob finds it (and Jellyfin when muxing is disabled).
	sidecarPath := DisplaySubtitlePath(encodedPath, record.Language)
	if err := fileutil.CopyFile(record.SubtitlePath, sidecarPath); err != nil {
		return fmt.Errorf("place subtitle sidecar %s: %w", key, err)
	}

	subtitledPath := encodedPath
	subtitlesMuxed := false
	if h.cfg.Subtitles.MuxIntoMKV {
		muxedPath, err := muxDisplaySubtitle(ctx, logger, encodedPath, sidecarPath, key, record.Language)
		if err != nil {
			logger.Warn("subtitle mux failed",
				"event_type", "mux_error",
				"error_hint", err.Error(),
				"impact", "subtitle remains as sidecar",
				"episode_key", key,
			)
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
