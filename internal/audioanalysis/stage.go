package audioanalysis

import (
	"context"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/encoding"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/stage"
)

const (
	progressStageAnalyzing = "Audio analyzing"
	progressPercentPrep    = 5.0
	progressPercentRefine  = 40.0
)

// Analyzer integrates audio analysis with the workflow manager.
type Analyzer struct {
	store  *queue.Store
	cfg    *config.Config
	logger *slog.Logger
}

// NewAnalyzer constructs a workflow stage that performs audio analysis for queue items.
func NewAnalyzer(cfg *config.Config, store *queue.Store, logger *slog.Logger) *Analyzer {
	return &Analyzer{
		cfg:    cfg,
		store:  store,
		logger: logging.NewComponentLogger(logger, "audio-analysis"),
	}
}

// SetLogger allows the workflow manager to route stage logs into the item-scoped log.
func (a *Analyzer) SetLogger(logger *slog.Logger) {
	if a == nil {
		return
	}
	a.logger = logging.NewComponentLogger(logger, "audio-analysis")
}

// Prepare primes queue progress fields before executing the stage.
func (a *Analyzer) Prepare(ctx context.Context, item *queue.Item) error {
	if a == nil || a.cfg == nil {
		return services.Wrap(services.ErrConfiguration, "audioanalysis", "prepare", "Audio analysis stage is not configured", nil)
	}
	if a.store == nil {
		return services.Wrap(services.ErrConfiguration, "audioanalysis", "prepare", "Queue store unavailable", nil)
	}
	item.InitProgress(progressStageAnalyzing, "Phase 1/2 - Preparing audio analysis")
	return a.store.UpdateProgress(ctx, item)
}

// Execute performs audio analysis for the queue item.
// This includes primary audio selection and (when enabled) commentary track detection.
func (a *Analyzer) Execute(ctx context.Context, item *queue.Item) error {
	stageStart := time.Now()

	if a == nil || a.cfg == nil {
		return services.Wrap(services.ErrConfiguration, "audioanalysis", "execute", "Audio analysis stage is not configured", nil)
	}
	if item == nil {
		return services.Wrap(services.ErrValidation, "audioanalysis", "execute", "Queue item is nil", nil)
	}
	if a.store == nil {
		return services.Wrap(services.ErrConfiguration, "audioanalysis", "execute", "Queue store unavailable", nil)
	}

	logger := logging.WithContext(ctx, a.logger)
	logger.Debug("starting audio analysis")

	// Parse rip spec to get asset paths
	env, err := stage.ParseRipSpec(item.RipSpecData)
	if err != nil {
		return err
	}

	// Build list of targets to analyze (from encoded files)
	targets := buildAnalysisTargets(&env, item)
	if len(targets) == 0 {
		return services.Wrap(services.ErrValidation, "audioanalysis", "execute",
			"No encoded assets available for audio analysis", nil)
	}

	// Phase 1: Commentary detection (when enabled)
	// Must run BEFORE audio refinement so we can identify commentary tracks
	// before they get stripped from the file.
	var commentaryResult *CommentaryResult
	var commentaryIndices []int
	specDirty := false

	if a.cfg.Commentary.Enabled {
		if err := a.updateProgress(ctx, item, fmt.Sprintf("Phase 1/2 - Detecting commentary (%d file(s))", len(targets)), progressPercentPrep); err != nil {
			return err
		}

		commentaryResult, err = a.detectCommentary(ctx, item, &env, targets)
		if err != nil {
			// Commentary detection failure is non-fatal - log and continue
			logger.Warn("commentary detection failed; continuing without commentary",
				logging.Error(err),
				logging.String(logging.FieldEventType, "commentary_detection_failed"),
				logging.String(logging.FieldErrorHint, "check WhisperX and LLM configuration"),
				logging.String(logging.FieldImpact, "commentary tracks will not be preserved"),
			)
		} else if commentaryResult != nil {
			specDirty = true
			storeCommentaryResult(&env, commentaryResult)

			// Collect commentary track indices for audio refinement
			for _, track := range commentaryResult.CommentaryTracks {
				commentaryIndices = append(commentaryIndices, track.Index)
			}

			logger.Info("commentary detection complete",
				logging.Int("commentary_tracks", len(commentaryResult.CommentaryTracks)),
				logging.Int("excluded_tracks", len(commentaryResult.ExcludedTracks)),
			)
		}
	} else {
		logger.Debug("commentary detection disabled")
		if err := a.updateProgress(ctx, item, fmt.Sprintf("Phase 1/2 - Analyzing audio (%d file(s))", len(targets)), progressPercentPrep); err != nil {
			return err
		}
	}

	// Phase 2: Primary audio selection with commentary preservation
	if err := a.updateProgress(ctx, item, "Phase 2/2 - Selecting audio tracks", progressPercentRefine); err != nil {
		return err
	}

	refineResult, err := RefineAudioTargets(ctx, a.cfg, a.logger, targets, commentaryIndices)
	if err != nil {
		return services.Wrap(services.ErrExternalTool, "audioanalysis", "refine audio tracks",
			"Failed to optimize ripped audio tracks with ffmpeg", err)
	}

	// Store audio info in RipSpec attributes
	if refineResult.PrimaryAudioDescription != "" {
		env.Attributes.PrimaryAudioDescription = refineResult.PrimaryAudioDescription
		specDirty = true
	}

	// Apply "comment" disposition to commentary tracks for Jellyfin recognition
	// Must happen AFTER refinement since disposition is set on the remuxed file.
	// Remap commentary indices to reflect post-refinement audio stream positions.
	if commentaryResult != nil && len(commentaryResult.CommentaryTracks) > 0 {
		RemapCommentaryIndices(commentaryResult, refineResult.KeptIndices)
		if err := ApplyCommentaryDisposition(ctx, a.cfg.FFprobeBinary(), a.logger, targets, commentaryResult); err != nil {
			logger.Warn("failed to set commentary disposition; tracks may not be labeled",
				logging.Error(err),
				logging.String(logging.FieldEventType, "commentary_disposition_failed"),
				logging.String(logging.FieldErrorHint, "commentary tracks will still be present but unlabeled"),
				logging.String(logging.FieldImpact, "Jellyfin may not recognize commentary tracks"),
			)
		} else {
			// Validate that commentary labeling was applied correctly
			expectedCount := len(commentaryResult.CommentaryTracks)
			if err := ValidateCommentaryLabeling(ctx, a.cfg.FFprobeBinary(), targets, expectedCount, a.logger); err != nil {
				return err
			}
		}
	}

	// Episode consistency check runs here (after audio refinement + commentary
	// disposition) so audio stream counts reflect the final output.
	if len(env.Episodes) > 1 {
		encoding.ValidateEpisodeConsistency(ctx, item, &env, logger)
	}

	// Persist updated RipSpec
	if specDirty {
		if encoded, encodeErr := env.Encode(); encodeErr == nil {
			item.RipSpecData = encoded
		} else {
			logger.Warn("failed to encode rip spec after audio analysis; metadata may be stale",
				logging.Error(encodeErr),
				logging.String(logging.FieldEventType, "rip_spec_encode_failed"),
				logging.String(logging.FieldErrorHint, "rerun identification if rip spec data looks wrong"),
				logging.String(logging.FieldImpact, "audio analysis metadata may not reflect latest state"),
			)
		}
	}

	// Set final progress
	item.ProgressStage = "Audio analyzed"
	item.ProgressPercent = 100
	item.ProgressMessage = buildCompletionMessage(refineResult, commentaryResult)

	if err := a.store.UpdateProgress(ctx, item); err != nil {
		return services.Wrap(services.ErrTransient, "audioanalysis", "persist progress",
			"Failed to persist audio analysis progress", err)
	}

	// Log stage summary
	summaryAttrs := []logging.Attr{
		logging.String(logging.FieldEventType, "stage_complete"),
		logging.Duration("stage_duration", time.Since(stageStart)),
		logging.Int("files_analyzed", len(targets)),
		logging.Bool("commentary_enabled", a.cfg.Commentary.Enabled),
	}
	if commentaryResult != nil {
		summaryAttrs = append(summaryAttrs, logging.Int("commentary_tracks", len(commentaryResult.CommentaryTracks)))
	}
	logger.Info("audio analysis stage summary", logging.Args(summaryAttrs...)...)

	return nil
}

// HealthCheck reports readiness for the audio analysis stage.
func (a *Analyzer) HealthCheck(ctx context.Context) stage.Health {
	const name = "audioanalysis"
	if a == nil || a.cfg == nil {
		return stage.Unhealthy(name, "stage not configured")
	}
	if a.store == nil {
		return stage.Unhealthy(name, "queue store unavailable")
	}
	return stage.Healthy(name)
}

func (a *Analyzer) updateProgress(ctx context.Context, item *queue.Item, message string, percent float64) error {
	item.ProgressStage = progressStageAnalyzing
	if strings.TrimSpace(message) != "" {
		item.ProgressMessage = message
	}
	if percent >= 0 {
		item.ProgressPercent = percent
	}
	if err := a.store.UpdateProgress(ctx, item); err != nil {
		return services.Wrap(services.ErrTransient, "audioanalysis", "persist progress",
			"Failed to persist audio analysis progress", err)
	}
	return nil
}

// buildAnalysisTargets extracts file paths to analyze from encoded assets.
// Audio analysis now runs post-encoding to operate on smaller files.
func buildAnalysisTargets(env *ripspec.Envelope, item *queue.Item) []string {
	if env == nil {
		return nil
	}

	var targets []string
	for _, asset := range env.Assets.Encoded {
		if path := strings.TrimSpace(asset.Path); path != "" {
			targets = append(targets, path)
		}
	}

	// Fall back to item's encoded file if no assets in envelope
	if len(targets) == 0 && item != nil {
		if path := strings.TrimSpace(item.EncodedFile); path != "" {
			targets = append(targets, path)
		}
	}

	return targets
}

func buildCompletionMessage(refine AudioRefinementResult, commentary *CommentaryResult) string {
	parts := []string{"Audio analysis complete"}
	if refine.PrimaryAudioDescription != "" {
		parts = append(parts, fmt.Sprintf("Primary: %s", refine.PrimaryAudioDescription))
	}
	if commentary != nil && len(commentary.CommentaryTracks) > 0 {
		parts = append(parts, fmt.Sprintf("Commentary: %d track(s)", len(commentary.CommentaryTracks)))
	}
	return strings.Join(parts, " | ")
}

// storeCommentaryResult adds commentary detection results to the RipSpec.
func storeCommentaryResult(env *ripspec.Envelope, result *CommentaryResult) {
	if env == nil || result == nil {
		return
	}

	data := &ripspec.AudioAnalysisData{
		PrimaryTrack: ripspec.AudioTrackRef{Index: result.PrimaryTrack.Index},
	}

	if len(result.CommentaryTracks) > 0 {
		data.CommentaryTracks = make([]ripspec.CommentaryTrackRef, 0, len(result.CommentaryTracks))
		for _, t := range result.CommentaryTracks {
			data.CommentaryTracks = append(data.CommentaryTracks, ripspec.CommentaryTrackRef{
				Index:      t.Index,
				Confidence: t.Confidence,
				Reason:     t.Reason,
			})
		}
	}

	if len(result.ExcludedTracks) > 0 {
		data.ExcludedTracks = make([]ripspec.ExcludedTrackRef, 0, len(result.ExcludedTracks))
		for _, t := range result.ExcludedTracks {
			data.ExcludedTracks = append(data.ExcludedTracks, ripspec.ExcludedTrackRef{
				Index:      t.Index,
				Reason:     t.Reason,
				Similarity: t.Similarity,
			})
		}
	}

	env.Attributes.AudioAnalysis = data
}
