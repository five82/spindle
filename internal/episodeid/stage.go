package episodeid

import (
	"context"
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
	logger  *slog.Logger
	matcher *contentid.Matcher
}

// NewEpisodeIdentifier constructs a new episode identification stage handler.
func NewEpisodeIdentifier(cfg *config.Config, logger *slog.Logger) *EpisodeIdentifier {
	var matcher *contentid.Matcher
	if cfg != nil && cfg.OpenSubtitlesEnabled {
		matcher = contentid.NewMatcher(cfg, logger)
	}
	id := &EpisodeIdentifier{
		cfg:     cfg,
		matcher: matcher,
	}
	id.SetLogger(logger)
	return id
}

// SetLogger updates the episode identifier's logging destination.
func (e *EpisodeIdentifier) SetLogger(logger *slog.Logger) {
	stageLogger := logger
	if stageLogger == nil {
		stageLogger = logging.NewNop()
	}
	e.logger = stageLogger.With(logging.String("component", "episodeid"))
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
		logger.Info("episode identification skipped for movie content")
		item.ProgressStage = "Episode Identification"
		item.ProgressMessage = "Skipped (movie content)"
		item.ProgressPercent = 100
		return nil
	}

	item.ProgressStage = "Episode Identification"
	item.ProgressMessage = "Analyzing episode content"
	item.ProgressPercent = 0

	logger.Debug("starting episode identification")

	return nil
}

// Execute performs episode matching using WhisperX and OpenSubtitles.
func (e *EpisodeIdentifier) Execute(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, e.logger)

	// Check if this is a TV show - skip for movies
	metadata := queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)
	if metadata.IsMovie() {
		logger.Debug("episode identification skipped for movie")
		item.Status = queue.StatusEpisodeIdentified
		item.ProgressStage = "Episode Identified"
		item.ProgressMessage = "Skipped (movie content)"
		item.ProgressPercent = 100
		return nil
	}

	// Decode rip spec
	if strings.TrimSpace(item.RipSpecData) == "" {
		logger.Warn("rip spec unavailable, skipping episode identification")
		item.Status = queue.StatusEpisodeIdentified
		item.ProgressStage = "Episode Identified"
		item.ProgressMessage = "Skipped (no rip spec)"
		item.ProgressPercent = 100
		return nil
	}

	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		logger.Warn("rip spec parse failed, skipping episode identification",
			logging.Error(err))
		item.Status = queue.StatusEpisodeIdentified
		item.ProgressStage = "Episode Identified"
		item.ProgressMessage = "Skipped (invalid rip spec)"
		item.ProgressPercent = 100
		return nil
	}

	// Check if we have episodes to match
	if len(env.Episodes) == 0 {
		logger.Info("no episodes in rip spec, skipping episode identification")
		item.Status = queue.StatusEpisodeIdentified
		item.ProgressStage = "Episode Identified"
		item.ProgressMessage = "Skipped (no episodes)"
		item.ProgressPercent = 100
		return nil
	}

	// Check if content matcher is available
	if e.matcher == nil {
		reason := "content matcher unavailable"
		if e.cfg == nil {
			reason = "configuration unavailable"
		} else if !e.cfg.OpenSubtitlesEnabled {
			reason = "opensubtitles disabled"
		}
		logger.Info("episode content identification skipped", logging.String("reason", reason))
		item.Status = queue.StatusEpisodeIdentified
		item.ProgressStage = "Episode Identified"
		item.ProgressMessage = "Skipped (OpenSubtitles disabled)"
		item.ProgressPercent = 100
		return nil
	}

	// Perform episode matching
	logger.Info("correlating episodes with OpenSubtitles references",
		logging.Int("episode_count", len(env.Episodes)))

	item.ProgressPercent = 25
	item.ProgressMessage = "Generating WhisperX transcripts"

	updated, err := e.matcher.Match(ctx, item, &env)
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

	if !e.cfg.OpenSubtitlesEnabled {
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
