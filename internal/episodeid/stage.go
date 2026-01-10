package episodeid

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"spindle/internal/config"
	"spindle/internal/contentid"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/stage"
)

// EpisodeIdentifier matches ripped TV episode files to definitive episode numbers
// using WhisperX transcription and OpenSubtitles reference comparison.
type EpisodeIdentifier struct {
	cfg     *config.Config
	store   *queue.Store
	logger  *slog.Logger
	matcher *contentid.Matcher
}

// NewEpisodeIdentifier constructs a new episode identification stage handler.
func NewEpisodeIdentifier(cfg *config.Config, store *queue.Store, logger *slog.Logger) *EpisodeIdentifier {
	var matcher *contentid.Matcher
	if cfg != nil && cfg.Subtitles.OpenSubtitlesEnabled {
		matcher = contentid.NewMatcher(cfg, logger)
	}
	id := &EpisodeIdentifier{
		cfg:     cfg,
		store:   store,
		matcher: matcher,
	}
	id.SetLogger(logger)
	return id
}

// SetLogger updates the episode identifier's logging destination.
func (e *EpisodeIdentifier) SetLogger(logger *slog.Logger) {
	e.logger = logging.NewComponentLogger(logger, "episodeid")
	if e.matcher != nil {
		e.matcher.SetLogger(logger)
	}
}

// Prepare initializes progress messaging prior to Execute.
func (e *EpisodeIdentifier) Prepare(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, e.logger)

	// Check if this is a TV show - skip for movies
	metadata := queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)
	if metadata.IsMovie() {
		logger.Debug("episode identification decision",
			logging.String(logging.FieldDecisionType, "episode_identification"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "movie_content"),
			logging.String("decision_options", "identify, skip"),
		)
		item.ProgressStage = "Episode Identification"
		item.ProgressMessage = "Skipped (movie content)"
		item.ProgressPercent = 100
		return nil
	}

	item.InitProgress("Episode Identification", "Analyzing episode content")
	logger.Debug("starting episode identification")
	return nil
}

// Execute performs episode matching using WhisperX and OpenSubtitles.
func (e *EpisodeIdentifier) Execute(ctx context.Context, item *queue.Item) error {
	stageStart := time.Now()
	logger := logging.WithContext(ctx, e.logger)

	// Check if this is a TV show - skip for movies
	metadata := queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)
	if metadata.IsMovie() {
		logger.Debug("episode identification decision",
			logging.String(logging.FieldDecisionType, "episode_identification"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "movie_content"),
			logging.String("decision_options", "identify, skip"),
		)
		item.Status = queue.StatusEpisodeIdentified
		item.ProgressStage = "Episode Identified"
		item.ProgressMessage = "Skipped (movie content)"
		item.ProgressPercent = 100
		item.ActiveEpisodeKey = ""
		return nil
	}

	// Decode rip spec
	if strings.TrimSpace(item.RipSpecData) == "" {
		logger.Debug("episode identification decision",
			logging.String(logging.FieldDecisionType, "episode_identification"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "no_rip_spec"),
			logging.String("decision_options", "identify, skip"),
		)
		item.Status = queue.StatusEpisodeIdentified
		item.ProgressStage = "Episode Identified"
		item.ProgressMessage = "Skipped (no rip spec)"
		item.ProgressPercent = 100
		item.ActiveEpisodeKey = ""
		return nil
	}

	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		logger.Debug("episode identification decision",
			logging.String(logging.FieldDecisionType, "episode_identification"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "invalid_rip_spec"),
			logging.String("decision_options", "identify, skip"),
			logging.Error(err),
		)
		item.Status = queue.StatusEpisodeIdentified
		item.ProgressStage = "Episode Identified"
		item.ProgressMessage = "Skipped (invalid rip spec)"
		item.ProgressPercent = 100
		item.ActiveEpisodeKey = ""
		return nil
	}

	// Check if we have episodes to match
	if len(env.Episodes) == 0 {
		logger.Debug("episode identification decision",
			logging.String(logging.FieldDecisionType, "episode_identification"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "no_episodes"),
			logging.String("decision_options", "identify, skip"),
		)
		item.Status = queue.StatusEpisodeIdentified
		item.ProgressStage = "Episode Identified"
		item.ProgressMessage = "Skipped (no episodes)"
		item.ProgressPercent = 100
		item.ActiveEpisodeKey = ""
		return nil
	}

	// Check if content matcher is available
	if e.matcher == nil {
		reason := "content matcher unavailable"
		if e.cfg == nil {
			reason = "configuration unavailable"
		} else if !e.cfg.Subtitles.OpenSubtitlesEnabled {
			reason = "opensubtitles disabled"
		}
		logger.Debug("episode identification decision",
			logging.String(logging.FieldDecisionType, "episode_identification"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", reason),
			logging.String("decision_options", "identify, skip"),
		)
		item.Status = queue.StatusEpisodeIdentified
		item.ProgressStage = "Episode Identified"
		item.ProgressMessage = "Skipped (OpenSubtitles disabled)"
		item.ProgressPercent = 100
		item.ActiveEpisodeKey = ""
		return nil
	}

	// Perform episode matching
	logger.Debug("correlating episodes with OpenSubtitles references",
		logging.Int("episode_count", len(env.Episodes)))

	item.ProgressPercent = 5
	item.ProgressMessage = "Phase 1/3 - Generating transcripts"
	if e.store != nil {
		_ = e.store.UpdateProgress(ctx, item)
	}

	updated, err := e.matcher.MatchWithProgress(ctx, item, &env, func(phase string, current, total int, episodeKey string) {
		if e.store == nil || item == nil {
			return
		}
		normalizedKey := strings.ToLower(strings.TrimSpace(episodeKey))
		if normalizedKey != "" {
			item.ActiveEpisodeKey = normalizedKey
		}
		episodeKey = strings.ToUpper(strings.TrimSpace(episodeKey))
		switch phase {
		case "transcribe":
			item.ProgressMessage = fmt.Sprintf("Phase 1/3 - Generating transcripts (%d/%d – %s)", current, total, episodeKey)
			item.ProgressPercent = 10 + 40*(float64(current)/float64(max(1, total)))
		case "reference":
			item.ProgressMessage = fmt.Sprintf("Phase 2/3 - Downloading references (%d/%d – %s)", current, total, episodeKey)
			item.ProgressPercent = 50 + 30*(float64(current)/float64(max(1, total)))
		case "apply":
			item.ProgressMessage = fmt.Sprintf("Phase 3/3 - Applying matches (%d/%d – %s)", current, total, episodeKey)
			item.ProgressPercent = 80 + 15*(float64(current)/float64(max(1, total)))
			if encoded, encodeErr := env.Encode(); encodeErr == nil {
				copy := *item
				copy.RipSpecData = encoded
				if err := e.store.Update(ctx, &copy); err == nil {
					*item = copy
				}
			}
		}
		_ = e.store.UpdateProgress(ctx, item)
	})
	if err != nil {
		return services.Wrap(
			services.ErrTransient,
			"episode identification",
			"content id",
			"Failed to correlate episodes with OpenSubtitles; retry once the service is reachable",
			err,
		)
	}

	// Update rip spec if matches were found
	if updated {
		encoded, encodeErr := env.Encode()
		if encodeErr != nil {
			logger.Error("failed to encode rip spec after episode identification",
				logging.Error(encodeErr),
				logging.String(logging.FieldEventType, "rip_spec_encode_failed"),
				logging.String(logging.FieldImpact, "episode matches cannot be persisted to metadata"),
				logging.String(logging.FieldErrorHint, "Retry episode identification or inspect rip spec serialization errors"))
			return services.Wrap(
				services.ErrValidation,
				"episode_identification",
				"persist episode matches",
				"Failed to encode episode identification results; episode matches would be lost",
				encodeErr,
			)
		}
		item.RipSpecData = encoded

		// Check for low-confidence episode matches
		const minMatchConfidence = 0.70
		var lowConfidenceEpisodes []string
		for _, ep := range env.Episodes {
			if ep.MatchConfidence > 0 && ep.MatchConfidence < minMatchConfidence {
				lowConfidenceEpisodes = append(lowConfidenceEpisodes, ep.Key)
			}
		}
		if len(lowConfidenceEpisodes) > 0 {
			logger.Warn("low confidence episode matches detected",
				logging.Int("count", len(lowConfidenceEpisodes)),
				logging.String("episodes", strings.Join(lowConfidenceEpisodes, ", ")),
				logging.Float64("threshold", minMatchConfidence),
				logging.String(logging.FieldEventType, "episode_match_low_confidence"),
				logging.String(logging.FieldErrorHint, "verify episode numbers manually before encoding"),
				logging.String(logging.FieldImpact, "episodes may be incorrectly identified"),
			)
			item.NeedsReview = true
			if item.ReviewReason == "" {
				item.ReviewReason = fmt.Sprintf("low episode match confidence (%d episodes below %.0f%%)", len(lowConfidenceEpisodes), minMatchConfidence*100)
			}
		}
	}

	item.Status = queue.StatusEpisodeIdentified
	item.ProgressStage = "Episode Identified"
	item.ProgressMessage = "Episodes correlated with OpenSubtitles"
	item.ProgressPercent = 100
	item.ActiveEpisodeKey = ""

	logger.Info("episode identification stage summary",
		logging.String(logging.FieldEventType, "stage_complete"),
		logging.Duration("stage_duration", time.Since(stageStart)),
		logging.Int("episode_count", len(env.Episodes)),
		logging.Bool("rip_spec_updated", updated),
	)

	return nil
}

// HealthCheck reports the stage's operational readiness.
func (e *EpisodeIdentifier) HealthCheck(ctx context.Context) stage.Health {
	const name = "episodeid"

	switch {
	case e.cfg == nil:
		return stage.Unhealthy(name, "configuration unavailable")
	case !e.cfg.Subtitles.OpenSubtitlesEnabled:
		return stage.Health{Name: name, Ready: true, Detail: "opensubtitles disabled (will skip TV episode matching)"}
	case e.matcher == nil:
		return stage.Unhealthy(name, "content matcher unavailable")
	default:
		return stage.Healthy(name)
	}
}
