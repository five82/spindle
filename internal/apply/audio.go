package apply

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/audio"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
)

// applyPostRefinementAudio selects the primary audio track after refinement,
// remaps commentary indices, and applies commentary metadata. Disposition and
// validation failures are degraded because preserving an unlabeled track is
// safer than dropping it.
func applyPostRefinementAudio(
	ctx context.Context,
	logger *slog.Logger,
	path string,
	refinement *audioRefinementResult,
	comms []ripspec.CommentaryTrackRef,
) (ripspec.AudioTrackRef, string, []ripspec.CommentaryTrackRef, error) {
	result, err := ffprobe.Inspect(ctx, "", path)
	if err != nil {
		return ripspec.AudioTrackRef{}, "", nil, fmt.Errorf("ffprobe post-refinement %s: %w", path, err)
	}

	selection := audio.Select(result.Streams, logger)
	primary := ripspec.AudioTrackRef{Index: selection.PrimaryIndex}

	logger.Info("primary audio selected",
		"decision_type", logs.DecisionAudioSelection,
		"decision_result", selection.PrimaryLabel(),
		"decision_reason", fmt.Sprintf("score-based selection from %d tracks", result.AudioStreamCount()),
	)

	remapped := comms
	if len(comms) > 0 && refinement != nil {
		remapped = remapCommentaryIndices(logger, comms, refinement.KeptIndices)
		if len(remapped) > 0 {
			audioStreams := result.AudioStreams()
			var targets []commentaryTarget
			for _, r := range remapped {
				var title string
				if r.Index < len(audioStreams) {
					title = audioStreams[r.Index].Tags["title"]
				}
				targets = append(targets, commentaryTarget{Index: r.Index, Title: title})
			}
			if err := applyCommentaryDisposition(ctx, logger, path, targets); err != nil {
				logger.Warn("commentary disposition failed",
					"event_type", "commentary_disposition_error",
					"error_hint", err.Error(),
					"impact", "commentary tracks not labeled",
				)
			} else {
				var remappedIndices []int
				for _, t := range targets {
					remappedIndices = append(remappedIndices, t.Index)
				}
				if err := validateCommentaryLabeling(ctx, logger, path, remappedIndices); err != nil {
					logger.Warn("commentary labeling validation failed",
						"event_type", "commentary_validation_error",
						"error_hint", err.Error(),
						"impact", "commentary labels may be incorrect",
					)
				}
			}
		}
	}
	return primary, selection.PrimaryLabel(), remapped, nil
}
