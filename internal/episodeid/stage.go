package episodeid

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"spindle/internal/config"
	"spindle/internal/contentid"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/services/llm"
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
		opts := []contentid.Option{contentid.WithPolicy(contentIDPolicyFromConfig(cfg))}
		if llmCfg := cfg.GetLLM(); llmCfg.APIKey != "" {
			opts = append(opts, contentid.WithLLMClient(llm.NewClientFrom(llmCfg)))
		}
		matcher = contentid.NewMatcher(cfg, logger, opts...)
	}
	id := &EpisodeIdentifier{
		cfg:     cfg,
		store:   store,
		matcher: matcher,
	}
	id.SetLogger(logger)
	return id
}

func contentIDPolicyFromConfig(cfg *config.Config) contentid.Policy {
	if cfg == nil {
		return contentid.DefaultPolicy()
	}
	return contentid.Policy{
		MinSimilarityScore:           cfg.ContentID.MinSimilarityScore,
		LowConfidenceReviewThreshold: cfg.ContentID.LowConfidenceReviewThreshold,
		LLMVerifyThreshold:           cfg.ContentID.LLMVerifyThreshold,
		AnchorMinScore:               cfg.ContentID.AnchorMinScore,
		AnchorMinScoreMargin:         cfg.ContentID.AnchorMinScoreMargin,
		BlockHighConfidenceDelta:     cfg.ContentID.BlockHighConfidenceDelta,
		BlockHighConfidenceTopRatio:  cfg.ContentID.BlockHighConfidenceTopRatio,
		DiscBlockPaddingMin:          cfg.ContentID.DiscBlockPaddingMin,
		DiscBlockPaddingDivisor:      cfg.ContentID.DiscBlockPaddingDivisor,
		Disc1MustStartAtEpisode1:     cfg.ContentID.Disc1MustStartAtEpisode1,
		Disc2PlusMinStartEpisode:     cfg.ContentID.Disc2PlusMinStartEpisode,
	}
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

	metadata := queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)
	if metadata.IsMovie() {
		logSkipDecision(logger, "movie_content")
		setSkipProgress(item, "Skipped (movie content)")
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

	metadata := queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)
	if metadata.IsMovie() {
		logSkipDecision(logger, "movie_content")
		setSkipProgress(item, "Skipped (movie content)")
		return nil
	}

	if strings.TrimSpace(item.RipSpecData) == "" {
		logSkipDecision(logger, "no_rip_spec")
		setSkipProgress(item, "Skipped (no rip spec)")
		return nil
	}

	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		logSkipDecision(logger, "invalid_rip_spec", logging.Error(err))
		setSkipProgress(item, "Skipped (invalid rip spec)")
		return nil
	}

	if len(env.Episodes) == 0 {
		logSkipDecision(logger, "no_episodes")
		setSkipProgress(item, "Skipped (no episodes)")
		return nil
	}

	if e.matcher == nil {
		reason := "content_matcher_unavailable"
		if e.cfg == nil {
			reason = "configuration_unavailable"
		} else if !e.cfg.Subtitles.OpenSubtitlesEnabled {
			reason = "opensubtitles_disabled"
		}
		logSkipDecision(logger, reason)
		if ripspec.HasUnresolvedEpisodes(env.Episodes) {
			flagForReview(logger, item, "episode numbers unresolved; content matching unavailable", reason)
		}
		setSkipProgress(item, "Skipped (OpenSubtitles disabled)")
		return nil
	}

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
		case contentid.PhaseTranscribe:
			item.ProgressMessage = fmt.Sprintf("Phase 1/3 - Generating transcripts (%d/%d - %s)", current, total, episodeKey)
			item.ProgressPercent = 10 + 40*(float64(current)/float64(max(1, total)))
		case contentid.PhaseReference:
			item.ProgressMessage = fmt.Sprintf("Phase 2/3 - Downloading references (%d/%d - %s)", current, total, episodeKey)
			item.ProgressPercent = 50 + 30*(float64(current)/float64(max(1, total)))
		case contentid.PhaseApply:
			item.ProgressMessage = fmt.Sprintf("Phase 3/3 - Applying matches (%d/%d - %s)", current, total, episodeKey)
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

	if !updated && ripspec.HasUnresolvedEpisodes(env.Episodes) {
		flagForReview(logger, item, "episode numbers unresolved; no matching subtitles found", "no_matching_subtitles")
	}

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

		// Propagate content_id_needs_review from matcher
		if env.Attributes.ContentIDNeedsReview {
			reason := "content id flagged for review"
			if r := strings.TrimSpace(env.Attributes.ContentIDReviewReason); r != "" {
				reason = r
			}
			flagForReview(logger, item, reason, "content_id_needs_review")
		}

		// Check for low-confidence episode matches
		minMatchConfidence := contentid.DefaultPolicy().LowConfidenceReviewThreshold
		if e.matcher != nil {
			minMatchConfidence = e.matcher.Policy().LowConfidenceReviewThreshold
		}
		var lowConfidenceEpisodes []string
		for _, ep := range env.Episodes {
			if ep.MatchConfidence > 0 && ep.MatchConfidence < minMatchConfidence {
				lowConfidenceEpisodes = append(lowConfidenceEpisodes, ep.Key)
			}
		}
		if len(lowConfidenceEpisodes) > 0 {
			flagForReview(logger, item,
				fmt.Sprintf("low episode match confidence (%d episodes below %.0f%%)", len(lowConfidenceEpisodes), minMatchConfidence*100),
				"low_confidence",
				logging.Int("count", len(lowConfidenceEpisodes)),
				logging.String("episodes", strings.Join(lowConfidenceEpisodes, ", ")),
				logging.Float64("threshold", minMatchConfidence),
				logging.String(logging.FieldEventType, "episode_match_low_confidence"),
				logging.String(logging.FieldErrorHint, "verify episode numbers manually before encoding"),
				logging.String(logging.FieldImpact, "episodes may be incorrectly identified"),
			)
		}

		// Check for partial resolution: some episodes matched but others still unresolved
		if unresolvedCount := ripspec.CountUnresolvedEpisodes(env.Episodes); unresolvedCount > 0 {
			flagForReview(logger, item,
				fmt.Sprintf("partial episode resolution: %d of %d episodes unresolved", unresolvedCount, len(env.Episodes)),
				"partial_resolution",
				logging.Int("unresolved_count", unresolvedCount),
				logging.Int("total_episodes", len(env.Episodes)))
		}

		// Check episode sequence contiguity: resolved numbers should form
		// an unbroken sequence (e.g. 1,2,3 not 1,2,5,6).
		var resolvedNums []int
		for _, ep := range env.Episodes {
			if ep.Episode > 0 {
				resolvedNums = append(resolvedNums, ep.Episode)
			}
		}
		if len(resolvedNums) > 1 {
			sort.Ints(resolvedNums)
			contiguous := true
			for i := 1; i < len(resolvedNums); i++ {
				if resolvedNums[i] != resolvedNums[i-1]+1 {
					contiguous = false
					break
				}
			}
			if !contiguous {
				flagForReview(logger, item,
					fmt.Sprintf("non-contiguous episode sequence: %d-%d", resolvedNums[0], resolvedNums[len(resolvedNums)-1]),
					"sequence_gap",
					logging.String(logging.FieldEventType, "episode_sequence_gap"),
					logging.String(logging.FieldErrorHint, "verify disc contains expected episodes"),
					logging.String(logging.FieldImpact, "episodes may be from different parts of the season"),
					logging.Int("resolved_count", len(resolvedNums)),
					logging.Int("range_start", resolvedNums[0]),
					logging.Int("range_end", resolvedNums[len(resolvedNums)-1]),
				)
			} else {
				logger.Info("episode contiguity decision",
					logging.Args(logging.DecisionAttrs("episode_contiguity", "contiguous", "unbroken_sequence")...)...)
			}
		}
	}

	item.SetProgressComplete("Episode Identified", "Episodes correlated with OpenSubtitles")
	item.ActiveEpisodeKey = ""

	logger.Info("episode identification stage summary",
		logging.String(logging.FieldEventType, "stage_complete"),
		logging.Duration("stage_duration", time.Since(stageStart)),
		logging.Int("episode_count", len(env.Episodes)),
		logging.Bool("rip_spec_updated", updated),
	)

	return nil
}

// flagForReview marks an item for manual review and logs the decision.
func flagForReview(logger *slog.Logger, item *queue.Item, reason, logReason string, extraAttrs ...logging.Attr) {
	item.NeedsReview = true
	if item.ReviewReason == "" {
		item.ReviewReason = reason
	}
	attrs := append(logging.DecisionAttrs("episode_review", "needs_review", logReason), extraAttrs...)
	logger.Info("flagging item for review", logging.Args(attrs...)...)
}

// logSkipDecision logs an episode identification skip decision with consistent fields.
func logSkipDecision(logger *slog.Logger, reason string, extraAttrs ...logging.Attr) {
	attrs := append(
		logging.DecisionAttrsWithOptions("episode_identification", "skipped", reason, "identify, skip"),
		extraAttrs...,
	)
	logger.Info("episode identification decision", logging.Args(attrs...)...)
}

// setSkipProgress updates item fields for a skipped stage.
func setSkipProgress(item *queue.Item, message string) {
	item.SetProgressComplete("Episode Identified", message)
	item.ActiveEpisodeKey = ""
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
