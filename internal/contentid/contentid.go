package contentid

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/services"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/textutil"
	"github.com/five82/spindle/internal/tmdb"
	"github.com/five82/spindle/internal/transcription"
)

// Handler implements stage.Handler for episode identification.
type Handler struct {
	cfg         *config.Config
	store       *queue.Store
	llmClient   *llm.Client
	osClient    *opensubtitles.Client
	tmdbClient  *tmdb.Client
	transcriber *transcription.Service
	policy      Policy
}

// New creates an episode identification handler.
func New(
	cfg *config.Config,
	store *queue.Store,
	llmClient *llm.Client,
	osClient *opensubtitles.Client,
	tmdbClient *tmdb.Client,
	transcriber *transcription.Service,
) *Handler {
	return &Handler{
		cfg:         cfg,
		store:       store,
		llmClient:   llmClient,
		osClient:    osClient,
		tmdbClient:  tmdbClient,
		transcriber: transcriber,
		policy:      policyFromConfig(cfg),
	}
}

// Compile-time check that Handler implements stage.Handler.
var _ stage.Handler = (*Handler)(nil)

// Run executes the episode identification stage.
// Returns immediately for movies (no-op).
func (h *Handler) Run(ctx context.Context, item *queue.Item) error {
	logger := stage.LoggerFromContext(ctx)

	env, err := stage.ParseRipSpec(item.RipSpecData)
	if err != nil {
		return err
	}

	if env.Metadata.MediaType == "movie" {
		logger.Info("skipping episode identification for movie",
			"decision_type", logs.DecisionEpisodeIDSkip,
			"decision_result", "skipped",
			"decision_reason", "media type is movie",
		)
		return nil
	}

	logger.Info("episode identification stage started", "event_type", "stage_start", "stage", "episode_identification")

	if h.transcriber == nil || h.osClient == nil || h.tmdbClient == nil {
		env.Attributes.ContentID = newDegradedContentIDSummary(h.policy, 0, 0)
		item.AppendReviewReason("Episode ID: content matcher unavailable")
		if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
			return err
		}
		return &services.ErrDegraded{Msg: "content matcher unavailable"}
	}

	seasonNum := env.Metadata.SeasonNumber
	if seasonNum <= 0 {
		seasonNum = 1
	}
	season, err := h.tmdbClient.GetSeason(ctx, env.Metadata.ID, seasonNum)
	if err != nil {
		logger.Error("tmdb season lookup failed",
			"event_type", "tmdb_season_error",
			"error_hint", err.Error(),
			"impact", "episode identification stopped; retry required",
		)
		return fmt.Errorf("episode identification tmdb season acquisition: %w", err)
	}
	if season == nil || len(season.Episodes) == 0 {
		env.Attributes.ContentID = newDegradedContentIDSummary(h.policy, 0, 0)
		item.AppendReviewReason("Episode ID: TMDB season contains no episodes")
		if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
			return err
		}
		return &services.ErrDegraded{Msg: "tmdb season contains no episodes"}
	}

	item.ActiveEpisodeKey = ""
	item.ProgressPercent = 10
	item.ProgressMessage = "Phase 1/3 - Transcribing episodes"
	_ = h.store.UpdateProgress(item)

	ripPrints, err := h.generateEpisodeFingerprints(ctx, item, &env)
	if err != nil {
		return err
	}
	if len(ripPrints) == 0 {
		logger.Warn("no valid transcriptions for episode ID",
			"event_type", "episode_id_no_transcripts",
			"error_hint", "all transcriptions produced empty fingerprints",
			"impact", "episodes remain unresolved",
		)
		env.Attributes.ContentID = newDegradedContentIDSummary(h.policy, 0, 0)
		item.AppendReviewReason("Episode ID: no valid transcriptions")
		if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
			return err
		}
		return &services.ErrDegraded{Msg: "no valid transcriptions"}
	}

	item.ActiveEpisodeKey = ""
	item.ProgressPercent = 50
	item.ProgressMessage = "Phase 2/3 - Fetching reference subtitles"
	_ = h.store.UpdateProgress(item)

	plan := deriveCandidateEpisodes(&env, season, env.Metadata.DiscNumber, h.policy)
	allSeasonEpisodes := seasonEpisodeNumbers(season)
	refCache := make(map[int]referenceFingerprint)
	allSeasonRefs := make([]referenceFingerprint, 0)
	selectedAnchor, hasAnchor := anchorSelection{}, false

	passes := buildEpisodePasses(plan, season, len(env.Episodes))
	for idx, pass := range passes {
		refs, fetchErr := h.fetchReferenceFingerprints(ctx, item, seasonNum, env.Metadata.ID, season, pass, refCache)
		if fetchErr != nil {
			logger.Warn("content id anchor reference fetch failed",
				"event_type", "contentid_anchor_fetch_failed",
				"error_hint", fetchErr.Error(),
				"impact", "falling back to heuristic candidate ranges",
			)
			break
		}
		allSeasonRefs = mergeReferences(allSeasonRefs, refs)
		if anchor, ok := selectAnchorWindow(ripPrints, allSeasonRefs, len(season.Episodes), h.policy.AnchorMinScore, h.policy.AnchorMinScoreMargin); ok {
			selectedAnchor = anchor
			hasAnchor = true
			logger.Info("content id anchor selected",
				"decision_type", "contentid_anchor",
				"decision_result", "selected",
				"decision_reason", anchor.Reason,
				"anchor_rip_index", anchor.RipIndex,
				"anchor_episode", anchor.TargetEpisode,
				"anchor_score", anchor.BestScore,
				"anchor_second_score", anchor.SecondBestScore,
				"anchor_margin", anchor.ScoreMargin,
				"window_start", anchor.WindowStart,
				"window_end", anchor.WindowEnd,
				"pass_index", idx+1,
			)
			break
		}
	}

	attempts := buildStrategyAttempts(plan, selectedAnchor, hasAnchor, allSeasonEpisodes)
	if len(attempts) == 0 {
		item.AppendReviewReason("Episode ID: no candidate strategies available")
		env.Attributes.ContentID = newDegradedContentIDSummary(h.policy, len(ripPrints), len(allSeasonRefs))
		if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
			return err
		}
		return &services.ErrDegraded{Msg: "no candidate strategies available"}
	}

	var outcomes []strategyOutcome
	var selected strategyOutcome
	haveSelection := false
	for _, attempt := range attempts {
		outcome, evalErr := h.evaluateStrategy(ctx, item, seasonNum, env.Metadata.ID, season, env.Metadata.DiscNumber, ripPrints, allSeasonRefs, refCache, attempt)
		if evalErr != nil {
			return evalErr
		}
		outcomes = append(outcomes, outcome)
		if !haveSelection || betterOutcome(outcome, selected) {
			selected = outcome
			haveSelection = true
		}
	}
	logStrategySummary(logger, outcomes, selected)

	matches := selected.Matches
	if len(matches) == 0 {
		env.Attributes.ContentID = newDegradedContentIDSummary(h.policy, len(ripPrints), len(selected.References))
		item.AppendReviewReason("Episode ID: no reference subtitles found")
		if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
			return err
		}
		return &services.ErrDegraded{Msg: "no episode matches resolved"}
	}
	if selected.Diagnostics.NeedsReview && selected.Diagnostics.ReviewReason != "" {
		item.AppendReviewReason("Episode ID: " + selected.Diagnostics.ReviewReason)
	}

	verifiedMatches, verifyResult := verifyMatches(ctx, h.llmClient, matches, ripPrints, selected.References, logger, h.policy.LLMVerifyThreshold)
	matches = verifiedMatches
	if verifyResult != nil && verifyResult.NeedsReview && verifyResult.ReviewReason != "" {
		item.AppendReviewReason("Episode ID: " + verifyResult.ReviewReason)
	}

	item.ProgressPercent = 80
	item.ProgressMessage = "Phase 3/3 - Matching episodes"
	_ = h.store.UpdateProgress(item)

	h.applyMatches(logger, &env, seasonNum, season, matches, item)
	env.Attributes.ContentID = buildContentIDSummary(&env, matches, len(ripPrints), len(selected.References), h.policy.LowConfidenceReviewThreshold)

	if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
		return err
	}

	item.ActiveEpisodeKey = ""
	item.ProgressPercent = 95
	item.ProgressMessage = "Phase 3/3 - Episode identification complete"
	_ = h.store.UpdateProgress(item)

	logger.Info("episode identification stage completed", "event_type", "stage_complete", "stage", "episode_identification")
	return nil
}

func (h *Handler) generateEpisodeFingerprints(ctx context.Context, item *queue.Item, env *ripspec.Envelope) ([]ripFingerprint, error) {
	logger := stage.LoggerFromContext(ctx)
	episodeCount := max(len(env.Episodes), 1)
	stagingRoot, err := item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		return nil, err
	}
	episodeDir := filepath.Join(stagingRoot, "contentid")
	if err := os.MkdirAll(episodeDir, 0o755); err != nil {
		return nil, fmt.Errorf("create contentid dir: %w", err)
	}

	prints := make([]ripFingerprint, 0, len(env.Episodes))
	for idx, ep := range env.Episodes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		asset, ok := env.Assets.FindAsset(ripspec.AssetKindRipped, ep.Key)
		if !ok || !asset.IsCompleted() {
			continue
		}

		item.ActiveEpisodeKey = ep.Key
		item.ProgressPercent = 10 + (40 * float64(idx+1) / float64(episodeCount))
		item.ProgressMessage = fmt.Sprintf("Phase 1/3 - Transcribing (%s)", ep.Key)
		_ = h.store.UpdateProgress(item)

		workDir := filepath.Join(episodeDir, ep.Key)
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return nil, fmt.Errorf("create workdir %s: %w", workDir, err)
		}
		selectedAudio, err := h.transcriber.SelectPrimaryAudioTrack(ctx, asset.Path, "en")
		if err != nil {
			return nil, fmt.Errorf("select audio %s: %w", ep.Key, err)
		}
		contentKey := fmt.Sprintf("%s:%s:%d", item.DiscFingerprint, ep.Key, selectedAudio.Index)
		result, err := h.transcriber.Transcribe(ctx, transcription.TranscribeRequest{
			InputPath:  asset.Path,
			AudioIndex: selectedAudio.Index,
			Language:   selectedAudio.Language,
			OutputDir:  workDir,
			ContentKey: contentKey,
		})
		if err != nil {
			return nil, fmt.Errorf("transcribe %s: %w", ep.Key, err)
		}
		text := readSRTText(result.SRTPath)
		fp := textutil.NewFingerprint(text)
		if fp == nil {
			continue
		}
		prints = append(prints, ripFingerprint{
			EpisodeKey: ep.Key,
			TitleID:    ep.TitleID,
			Path:       result.SRTPath,
			Vector:     fp,
			RawVector:  fp,
		})
		logger.Debug("content id whisperx transcript ready",
			"episode_key", ep.Key,
			"subtitle_file", result.SRTPath,
			"token_count", len(fp.Terms),
		)
	}
	return prints, nil
}

func (h *Handler) evaluateStrategy(
	ctx context.Context,
	item *queue.Item,
	seasonNum int,
	tmdbID int,
	season *tmdb.Season,
	discNumber int,
	ripPrints []ripFingerprint,
	allSeasonRefs []referenceFingerprint,
	refCache map[int]referenceFingerprint,
	attempt strategyAttempt,
) (strategyOutcome, error) {
	out := strategyOutcome{Attempt: attempt}
	refs := filterReferencesByEpisodes(allSeasonRefs, attempt.Episodes)
	missing := missingEpisodesForReferences(refs, attempt.Episodes)
	if len(missing) > 0 {
		fetched, err := h.fetchReferenceFingerprints(ctx, item, seasonNum, tmdbID, season, missing, refCache)
		if err != nil {
			return out, fmt.Errorf("fetch strategy references: %w", err)
		}
		refs = mergeReferences(refs, fetched)
	}
	if len(refs) == 0 {
		return out, nil
	}

	weightedRips := cloneRipFingerprints(ripPrints)
	weightedRefs := cloneReferenceFingerprints(refs)
	applyIDFWeighting(weightedRips, weightedRefs)
	matches, diagnostics := decodeOrderedEpisodeMatches(weightedRips, weightedRefs, discNumber, len(season.Episodes), h.policy)
	out.References = weightedRefs
	out.Matches = matches
	out.Diagnostics = diagnostics
	out.AverageScore = averageMatchScore(matches)
	out.AverageConfidence = averageMatchConfidence(matches)
	return out, nil
}

func mergeReferences(existing, additional []referenceFingerprint) []referenceFingerprint {
	merged := make(map[int]referenceFingerprint, len(existing)+len(additional))
	for _, ref := range existing {
		merged[ref.EpisodeNumber] = ref
	}
	for _, ref := range additional {
		merged[ref.EpisodeNumber] = ref
	}
	episodes := make([]int, 0, len(merged))
	for ep := range merged {
		episodes = append(episodes, ep)
	}
	sort.Ints(episodes)
	out := make([]referenceFingerprint, 0, len(episodes))
	for _, ep := range episodes {
		out = append(out, merged[ep])
	}
	return out
}

// newDegradedContentIDSummary builds a ContentIDSummary for failure/early-exit
// paths where episode identification did not complete. Callers pass whatever
// counts they have so far (0 when nothing has happened yet).
func newDegradedContentIDSummary(policy Policy, transcribed, references int) *ripspec.ContentIDSummary {
	return &ripspec.ContentIDSummary{
		Method:               "whisperx_tfidf_ordered_dp",
		ReferenceSource:      "opensubtitles",
		ReviewThreshold:      policy.LowConfidenceReviewThreshold,
		TranscribedEpisodes:  transcribed,
		ReferenceEpisodes:    references,
		EpisodesSynchronized: false,
		Completed:            false,
	}
}

func buildContentIDSummary(env *ripspec.Envelope, matches []matchResult, transcribedCount, referenceCount int, reviewThreshold float64) *ripspec.ContentIDSummary {
	if env == nil {
		return nil
	}
	summary := &ripspec.ContentIDSummary{
		Method:               "whisperx_tfidf_ordered_dp",
		ReferenceSource:      "opensubtitles",
		ReferenceEpisodes:    referenceCount,
		TranscribedEpisodes:  transcribedCount,
		ReviewThreshold:      reviewThreshold,
		SequenceContiguous:   checkContiguity(matches),
		EpisodesSynchronized: true,
		Completed:            true,
	}
	for _, ep := range env.Episodes {
		if ep.Episode > 0 {
			summary.MatchedEpisodes++
		} else {
			summary.UnresolvedEpisodes++
		}
		if ep.MatchConfidence > 0 && ep.MatchConfidence < reviewThreshold {
			summary.LowConfidenceCount++
		}
	}
	return summary
}

func (h *Handler) applyMatches(
	logger *slog.Logger,
	env *ripspec.Envelope,
	seasonNum int,
	season *tmdb.Season,
	matches []matchResult,
	item *queue.Item,
) {
	matchMap := make(map[string]matchResult, len(matches))
	for _, m := range matches {
		matchMap[strings.ToLower(m.EpisodeKey)] = m
	}

	episodeDetails := make(map[int]tmdb.Episode, len(season.Episodes))
	for _, ep := range season.Episodes {
		episodeDetails[ep.EpisodeNumber] = ep
	}

	unresolvedCount := 0
	lowConfCount := 0
	assetKeyRemap := make(map[string]string)
	for i := range env.Episodes {
		ep := &env.Episodes[i]
		originalKey := ep.Key
		m, ok := matchMap[strings.ToLower(ep.Key)]
		if !ok {
			unresolvedCount++
			ep.AppendReviewReason("Episode ID: unresolved")
			continue
		}
		details := episodeDetails[m.TargetEpisode]
		ep.Season = seasonNum
		ep.Episode = m.TargetEpisode
		ep.EpisodeTitle = strings.TrimSpace(details.Name)
		ep.EpisodeAirDate = strings.TrimSpace(details.AirDate)
		ep.MatchScore = m.Score
		ep.MatchConfidence = m.Confidence
		ep.Key = ripspec.EpisodeKey(seasonNum, m.TargetEpisode)
		if ep.Key != "" && ep.Key != originalKey {
			assetKeyRemap[originalKey] = ep.Key
		}
		logger.Info("episode matched",
			"decision_type", logs.DecisionEpisodeMatch,
			"decision_result", fmt.Sprintf("%s -> E%02d", originalKey, m.TargetEpisode),
			"decision_reason", fmt.Sprintf("ordered path %s window %d-%d", m.Orientation, m.WindowStart, m.WindowEnd),
			"match_score", m.Score,
			"match_confidence", m.Confidence,
			"confidence_quality", m.ConfidenceQuality,
			"runner_up_episode", m.RunnerUpEpisode,
			"runner_up_score", m.RunnerUpScore,
			"score_margin", m.ScoreMargin,
			"neighbor_runner_up_episode", m.NeighborRunnerUpEpisode,
			"neighbor_runner_up_score", m.NeighborRunnerUpScore,
			"neighbor_score_margin", m.NeighborScoreMargin,
			"reverse_runner_up_key", m.ReverseRunnerUpKey,
			"reverse_runner_up_score", m.ReverseRunnerUpScore,
			"reverse_score_margin", m.ReverseScoreMargin,
			"path_score", m.PathScore,
			"path_margin", m.PathMargin,
			"internal_gap_count", m.InternalGapCount,
			"unresolved_count", m.UnresolvedCount,
			"sequence_contiguous", m.SequenceContiguous,
		)
		if m.Confidence < h.policy.LowConfidenceReviewThreshold {
			lowConfCount++
			ep.AppendReviewReason(fmt.Sprintf("Episode ID: confidence %.3f below threshold %.2f", m.Confidence, h.policy.LowConfidenceReviewThreshold))
			logger.Warn("low confidence episode match",
				"event_type", "low_confidence_match",
				"error_hint", fmt.Sprintf("%s matched E%02d with confidence %.3f and score %.3f (runner-up E%02d %.3f, neighbor E%02d %.3f, path margin %.3f)", ep.Key, m.TargetEpisode, m.Confidence, m.Score, m.RunnerUpEpisode, m.RunnerUpScore, m.NeighborRunnerUpEpisode, m.NeighborRunnerUpScore, m.PathMargin),
				"impact", "match may be incorrect",
				"confidence_quality", m.ConfidenceQuality,
				"match_score", m.Score,
				"match_confidence", m.Confidence,
				"runner_up_episode", m.RunnerUpEpisode,
				"runner_up_score", m.RunnerUpScore,
				"score_margin", m.ScoreMargin,
				"neighbor_runner_up_episode", m.NeighborRunnerUpEpisode,
				"neighbor_runner_up_score", m.NeighborRunnerUpScore,
				"neighbor_score_margin", m.NeighborScoreMargin,
				"reverse_runner_up_key", m.ReverseRunnerUpKey,
				"reverse_runner_up_score", m.ReverseRunnerUpScore,
				"reverse_score_margin", m.ReverseScoreMargin,
				"path_margin", m.PathMargin,
			)
		}
		if m.NeedsVerification && m.VerificationReason != "" {
			ep.AppendReviewReason("Episode ID: ambiguity requires verification")
			if item != nil {
				item.AppendReviewReason("Episode ID: ambiguity requires verification")
			}
		}
	}
	applyOpeningDoubleEpisode(logger, env, seasonNum, env.Metadata.DiscNumber, episodeDetails, assetKeyRemap)
	env.Assets.RemapEpisodeKeys(assetKeyRemap)

	if item != nil && unresolvedCount > 0 {
		item.AppendReviewReason(fmt.Sprintf("Episode ID: %d of %d episodes unresolved", unresolvedCount, len(env.Episodes)))
	}
	if item != nil && lowConfCount > 0 {
		item.AppendReviewReason(fmt.Sprintf("Episode ID: %d matches below confidence threshold %.2f", lowConfCount, h.policy.LowConfidenceReviewThreshold))
	}
}

func applyOpeningDoubleEpisode(logger *slog.Logger, env *ripspec.Envelope, seasonNum, discNumber int, details map[int]tmdb.Episode, assetKeyRemap map[string]string) {
	if discNumber != 1 || !probableOpeningDoubleEpisode(env.Episodes) || len(env.Episodes) < 3 {
		return
	}
	for _, ep := range env.Episodes {
		if ep.Episode <= 0 {
			return
		}
	}
	start := env.Episodes[0].Episode
	if start != 1 && start != 2 {
		return
	}
	for i := 1; i < len(env.Episodes); i++ {
		if env.Episodes[i].Episode != start+i {
			return
		}
	}

	originalKey := env.Episodes[0].Key
	env.Episodes[0].Episode = 1
	env.Episodes[0].EpisodeEnd = 2
	env.Episodes[0].Key = ripspec.EpisodeRangeKey(seasonNum, 1, 2)
	if ep1, ok1 := details[1]; ok1 {
		if ep2, ok2 := details[2]; ok2 {
			env.Episodes[0].EpisodeTitle = strings.TrimSpace(ep1.Name + " / " + ep2.Name)
		}
	}
	if env.Episodes[0].Key != originalKey {
		assetKeyRemap[originalKey] = env.Episodes[0].Key
		for old, next := range assetKeyRemap {
			if next == originalKey {
				assetKeyRemap[old] = env.Episodes[0].Key
			}
		}
	}
	if start == 1 {
		for i := 1; i < len(env.Episodes); i++ {
			oldKey := env.Episodes[i].Key
			env.Episodes[i].Episode++
			env.Episodes[i].Key = ripspec.EpisodeKey(seasonNum, env.Episodes[i].Episode)
			if title, ok := details[env.Episodes[i].Episode]; ok {
				env.Episodes[i].EpisodeTitle = strings.TrimSpace(title.Name)
				env.Episodes[i].EpisodeAirDate = strings.TrimSpace(title.AirDate)
			}
			if env.Episodes[i].Key != oldKey {
				assetKeyRemap[oldKey] = env.Episodes[i].Key
				for old, next := range assetKeyRemap {
					if next == oldKey {
						assetKeyRemap[old] = env.Episodes[i].Key
					}
				}
			}
		}
	}
	logger.Info("opening double-length episode inferred",
		"decision_type", logs.DecisionEpisodeMatch,
		"decision_result", env.Episodes[0].Key,
		"decision_reason", "disc 1 opening title runtime matches double-episode profile",
	)
}
