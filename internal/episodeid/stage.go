package episodeid

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

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
		logger.Info("episode identification decision",
			logging.String(logging.FieldDecisionType, "episode_identification"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "movie_content"),
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
	logger := logging.WithContext(ctx, e.logger)

	// Check if this is a TV show - skip for movies
	metadata := queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)
	if metadata.IsMovie() {
		logger.Info("episode identification decision",
			logging.String(logging.FieldDecisionType, "episode_identification"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "movie_content"),
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
		logger.Info("episode identification decision",
			logging.String(logging.FieldDecisionType, "episode_identification"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "no_rip_spec"),
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
		logger.Info("episode identification decision",
			logging.String(logging.FieldDecisionType, "episode_identification"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "invalid_rip_spec"),
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
		logger.Info("episode identification decision",
			logging.String(logging.FieldDecisionType, "episode_identification"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "no_episodes"),
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
		logger.Info("episode identification decision",
			logging.String(logging.FieldDecisionType, "episode_identification"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", reason),
		)
		item.Status = queue.StatusEpisodeIdentified
		item.ProgressStage = "Episode Identified"
		item.ProgressMessage = "Skipped (OpenSubtitles disabled)"
		item.ProgressPercent = 100
		item.ActiveEpisodeKey = ""
		return nil
	}

	// Perform episode matching
	logger.Info("correlating episodes with OpenSubtitles references",
		logging.Int("episode_count", len(env.Episodes)))

	item.ProgressPercent = 5
	item.ProgressMessage = "Generating WhisperX transcripts"
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
			item.ProgressMessage = fmt.Sprintf("Generating WhisperX transcripts %d/%d – %s", current, total, episodeKey)
			item.ProgressPercent = 10 + 40*(float64(current)/float64(max(1, total)))
		case "reference":
			item.ProgressMessage = fmt.Sprintf("Downloading OpenSubtitles references %d/%d – %s", current, total, episodeKey)
			item.ProgressPercent = 50 + 30*(float64(current)/float64(max(1, total)))
		case "apply":
			item.ProgressMessage = fmt.Sprintf("Applying episode matches %d/%d – %s", current, total, episodeKey)
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
		if encoded, encodeErr := env.Encode(); encodeErr == nil {
			item.RipSpecData = encoded
			logger.Info("episode identification complete, rip spec updated",
				logging.Int("episode_count", len(env.Episodes)))
		} else {
			logger.Warn("failed to encode rip spec after episode identification",
				logging.Error(encodeErr))
		}
	} else {
		logger.Info("episode identification complete, no changes needed")
	}

	item.Status = queue.StatusEpisodeIdentified
	item.ProgressStage = "Episode Identified"
	item.ProgressMessage = "Episodes correlated with OpenSubtitles"
	item.ProgressPercent = 100
	item.ActiveEpisodeKey = ""

	return nil
}

// HealthCheck reports the stage's operational readiness.
func (e *EpisodeIdentifier) HealthCheck(ctx context.Context) stage.Health {
	if e.cfg == nil {
		return stage.Health{
			Name:   "episodeid",
			Ready:  false,
			Detail: "configuration unavailable",
		}
	}

	if !e.cfg.Subtitles.OpenSubtitlesEnabled {
		return stage.Health{
			Name:   "episodeid",
			Ready:  true,
			Detail: "opensubtitles disabled (will skip TV episode matching)",
		}
	}

	if e.matcher == nil {
		return stage.Health{
			Name:   "episodeid",
			Ready:  false,
			Detail: "content matcher unavailable",
		}
	}

	return stage.Health{
		Name:   "episodeid",
		Ready:  true,
		Detail: "ready",
	}
}
