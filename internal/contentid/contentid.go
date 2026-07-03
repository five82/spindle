package contentid

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/textutil"
	"github.com/five82/spindle/internal/tmdb"
	"github.com/five82/spindle/internal/transcription"
)

// Handler implements stage.Handler for episode identification.
type Handler struct {
	cfg         *config.Config
	llmClient   *llm.Client
	osClient    *opensubtitles.Client
	tmdbClient  *tmdb.Client
	transcriber *transcription.Service
	policy      Policy
}

// New creates an episode identification handler.
func New(
	cfg *config.Config,
	llmClient *llm.Client,
	osClient *opensubtitles.Client,
	tmdbClient *tmdb.Client,
	transcriber *transcription.Service,
) *Handler {
	return &Handler{
		cfg:         cfg,
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
func (h *Handler) Run(ctx context.Context, sess *stage.Session) error {
	item := sess.Item
	logger := sess.Logger
	env := sess.Env

	mediaType := strings.ToLower(strings.TrimSpace(env.Metadata.MediaType))
	switch mediaType {
	case "tv":
	case "movie":
		logger.Info("skipping episode identification for movie",
			"decision_type", logs.DecisionEpisodeIDSkip,
			"decision_result", "skipped",
			"decision_reason", "media type is movie",
		)
		return nil
	default:
		if mediaType == "" {
			mediaType = "unknown"
		}
		logger.Info("skipping episode identification for non-TV content",
			"decision_type", logs.DecisionEpisodeIDSkip,
			"decision_result", "skipped",
			"decision_reason", fmt.Sprintf("media type is %s", mediaType),
		)
		return nil
	}

	logger.Info("episode identification stage started", "event_type", "stage_start", "stage", "episode_identification")
	logger.Info("episode identification plan",
		"event_type", "episode_identification_plan",
		"episodes", len(env.Episodes),
		"season", env.Metadata.SeasonNumber,
		"disc_number", env.Metadata.DiscNumber,
	)

	if h.transcriber == nil || h.osClient == nil || h.tmdbClient == nil {
		env.Attributes.ContentID = newDegradedContentIDSummary(h.policy, 0, 0)
		sess.AddReviewReason("Episode ID: content matcher unavailable")
		if err := sess.Save(); err != nil {
			return err
		}
		return &stage.ErrDegraded{Msg: "content matcher unavailable"}
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
		sess.AddReviewReason("Episode ID: TMDB season contains no episodes")
		if err := sess.Save(); err != nil {
			return err
		}
		return &stage.ErrDegraded{Msg: "tmdb season contains no episodes"}
	}

	_ = sess.Progress(10, "Phase 1/3 - Transcribing episodes", stage.WithActiveEpisode(""))

	// The initial reference fetch needs only the envelope and TMDB season, so
	// it runs concurrently with transcription: the fetch loop is network-bound
	// and internally rate-limited while transcription is GPU-bound. The
	// buffered channel lets the goroutine finish even when an early return
	// abandons the result; cancelFetch stops it from outliving the stage.
	plan := deriveCandidateEpisodes(env, season, env.Metadata.DiscNumber)
	refCache := make(map[int]referenceFingerprint)
	fetchCtx, cancelFetch := context.WithCancel(ctx)
	defer cancelFetch()
	type refFetchOutcome struct {
		refs []referenceFingerprint
		err  error
	}
	refFetched := make(chan refFetchOutcome, 1)
	logger.Info("reference subtitle fetch started",
		"decision_type", logs.DecisionContentIDCandidates,
		"decision_result", "fetch_overlapped",
		"decision_reason", "network-bound reference fetch runs during GPU-bound transcription",
		"initial_episode_count", len(plan.InitialEpisodes),
	)
	go func() {
		refs, err := h.fetchReferenceFingerprints(fetchCtx, logger, item, seasonNum, env.Metadata.ID, season, plan.InitialEpisodes, refCache)
		refFetched <- refFetchOutcome{refs: refs, err: err}
	}()

	ripPrints, err := h.generateEpisodeFingerprints(ctx, sess, env)
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
		sess.AddReviewReason("Episode ID: no valid transcriptions")
		if err := sess.Save(); err != nil {
			return err
		}
		return &stage.ErrDegraded{Msg: "no valid transcriptions"}
	}

	_ = sess.Progress(50, "Phase 2/3 - Fetching reference subtitles", stage.WithActiveEpisode(""))

	fetched := <-refFetched
	refs, err := fetched.refs, fetched.err
	if err != nil {
		return fmt.Errorf("fetch initial references: %w", err)
	}
	if len(refs) == 0 {
		env.Attributes.ContentID = newDegradedContentIDSummary(h.policy, len(ripPrints), 0)
		sess.AddReviewReason("Episode ID: no reference subtitles found")
		if err := sess.Save(); err != nil {
			return err
		}
		return &stage.ErrDegraded{Msg: "no reference subtitles found"}
	}

	matches, refs, err := h.matchEpisodes(ctx, sess, env, season, seasonNum, plan, ripPrints, refs, refCache)
	if err != nil {
		return err
	}

	_ = sess.Progress(95, "Phase 3/3 - Episode identification complete", stage.WithActiveEpisode(""))

	logger.Info("episode identification stage completed",
		"event_type", "stage_complete",
		"stage", "episode_identification",
		"transcribed_episodes", len(ripPrints),
		"reference_episodes", len(refs),
		"matched_episodes", len(matches),
		"needs_review", item.NeedsReview == 1,
	)
	return nil
}

// matchEpisodes resolves rip-to-episode claims against the reference
// fingerprints, expanding the reference scope and re-fetching when the
// initial candidates are insufficient, verifying ambiguous pairs via LLM,
// and applying the accepted matches to the envelope (task: episode_match).
// It returns the accepted matches and the (possibly expanded) reference set.
func (h *Handler) matchEpisodes(
	ctx context.Context,
	sess *stage.Session,
	env *ripspec.Envelope,
	season *tmdb.Season,
	seasonNum int,
	plan candidateEpisodePlan,
	ripPrints []ripFingerprint,
	refs []referenceFingerprint,
	refCache map[int]referenceFingerprint,
) ([]matchResult, []referenceFingerprint, error) {
	logger := sess.Logger
	item := sess.Item

	resolution := resolveEpisodeClaims(ripPrints, refs, h.policy)
	if expand, reason := shouldExpandCandidateScope(plan, resolution, len(ripPrints)); expand {
		logger.Info("content ID reference scope expanded",
			"decision_type", logs.DecisionContentIDCandidates,
			"decision_result", "expanded",
			"decision_reason", reason,
			"initial_episode_count", len(plan.InitialEpisodes),
			"expanded_episode_count", len(plan.ExpandedEpisodes),
		)
		expandedRefs, fetchErr := h.fetchReferenceFingerprints(ctx, logger, item, seasonNum, env.Metadata.ID, season, plan.ExpandedEpisodes, refCache)
		if fetchErr != nil {
			return nil, nil, fmt.Errorf("fetch expanded references: %w", fetchErr)
		}
		if len(expandedRefs) > 0 {
			refs = expandedRefs
			resolution = resolveEpisodeClaims(ripPrints, refs, h.policy)
		}
	}

	logger.Info("content ID match resolution computed",
		"decision_type", logs.DecisionContentIDMatches,
		"decision_result", "resolved",
		"decision_reason", "content_first_claim_ranking",
		"clear_matches", resolution.ClearMatchCount,
		"ambiguous_rips", resolution.AmbiguousCount,
		"decisive_low_similarity_rips", resolution.DecisiveLowSimilarityCount,
		"contested_rips", resolution.ContestedCount,
		"suspect_references", resolution.SuspectReferenceCount,
	)

	matches := append([]matchResult(nil), resolution.Accepted...)
	verifiedMatches, remainingPending, verifyResult := verifyMatches(ctx, h.llmClient, matches, resolution.PendingByRip, ripPrints, refs, logger)
	matches = verifiedMatches
	if verifyResult != nil && verifyResult.NeedsReview && verifyResult.ReviewReason != "" {
		sess.AddReviewReason("Episode ID: " + verifyResult.ReviewReason)
	}

	if reconciled, ok := reconcileSingleHole(matches, remainingPending, refs, h.policy); ok {
		matches = reconciled
		logger.Info("content ID single-hole reconciliation applied",
			"decision_type", logs.DecisionContentIDMatches,
			"decision_result", "reconciled",
			"decision_reason", "single_unresolved_rip_and_single_missing_episode",
		)
	}

	for _, reason := range structuralReviewReasons(matches, env.Metadata.DiscNumber) {
		sess.AddReviewReason("Episode ID: " + reason)
	}
	if hasSuspectAcceptedMatch(matches) {
		sess.AddReviewReason("Episode ID: one or more matches rely on suspect references")
	}

	_ = sess.Progress(80, "Phase 3/3 - Matching episodes")

	h.applyMatches(logger, env, seasonNum, season, matches, sess)
	env.Attributes.ContentID = buildContentIDSummary(env, matches, len(ripPrints), len(refs), h.policy.LowConfidenceReviewThreshold)

	if err := sess.Save(); err != nil {
		return nil, nil, err
	}
	return matches, refs, nil
}

func (h *Handler) generateEpisodeFingerprints(ctx context.Context, sess *stage.Session, env *ripspec.Envelope) ([]ripFingerprint, error) {
	logger := sess.Logger
	item := sess.Item
	episodeCount := max(len(env.Episodes), 1)
	stagingRoot, err := item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		return nil, err
	}
	// Transcripts are shared artifacts: commentary analysis and subtitle
	// generation reuse them later via ripspec.AssetKindTranscript.
	episodeDir := filepath.Join(stagingRoot, "transcripts")
	if err := os.MkdirAll(episodeDir, 0o755); err != nil {
		return nil, fmt.Errorf("create transcripts dir: %w", err)
	}

	// Select the primary audio track per episode (cheap ffprobe), then
	// transcribe every episode in ONE WhisperX invocation so uvx startup and
	// model load are paid once per disc instead of once per episode.
	var batched []ripspec.Episode
	var reqs []transcription.TranscribeRequest
	for _, ep := range env.Episodes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		asset, ok := env.Assets.FindAsset(ripspec.AssetKindRipped, ep.Key)
		if !ok || !asset.IsCompleted() {
			continue
		}
		workDir := filepath.Join(episodeDir, ep.Key)
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return nil, fmt.Errorf("create workdir %s: %w", workDir, err)
		}
		selectedAudio, err := h.transcriber.SelectPrimaryAudioTrack(ctx, asset.Path, "en")
		if err != nil {
			return nil, fmt.Errorf("select audio %s: %w", ep.Key, err)
		}
		batched = append(batched, ep)
		reqs = append(reqs, transcription.TranscribeRequest{
			InputPath:  asset.Path,
			AudioIndex: selectedAudio.Index,
			Language:   selectedAudio.Language,
			OutputDir:  workDir,
			ItemID:     item.ID,
			EpisodeKey: ep.Key,
			Purpose:    "episode_identification",
		})
	}
	if len(reqs) == 0 {
		return nil, nil
	}

	_ = sess.Progress(15, fmt.Sprintf("Phase 1/3 - Transcribing %d of %d episodes (batched)", len(reqs), episodeCount), stage.WithActiveEpisode(""))
	results, err := h.transcriber.TranscribeBatch(ctx, reqs)
	if err != nil {
		return nil, fmt.Errorf("transcribe episode batch: %w", err)
	}

	prints := make([]ripFingerprint, 0, len(batched))
	for i, ep := range batched {
		result := results[i]
		if err := sess.SaveAssetSuccess(ripspec.AssetKindTranscript, ripspec.Asset{
			EpisodeKey: ep.Key,
			TitleID:    ep.TitleID,
			Path:       result.SRTPath,
			Status:     ripspec.AssetStatusCompleted,
		}); err != nil {
			return nil, fmt.Errorf("record transcript asset %s: %w", ep.Key, err)
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
		logger.Info("content ID WhisperX transcript ready",
			"event_type", "contentid_transcript_ready",
			"episode_key", ep.Key,
			"subtitle_file", result.SRTPath,
			"token_count", len(fp.Terms),
			"segments", result.Segments,
			"duration_ms", result.TranscribeTime.Milliseconds(),
		)
	}
	return prints, nil
}

func newDegradedContentIDSummary(policy Policy, transcribed, references int) *ripspec.ContentIDSummary {
	return &ripspec.ContentIDSummary{
		Method:               "whisperx_tfidf_content_matcher",
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
		Method:               "whisperx_tfidf_content_matcher",
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
	sess *stage.Session,
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
		recordAssetKeyRemap(assetKeyRemap, originalKey, ep.Key)
		logger.Info("episode matched",
			"decision_type", logs.DecisionEpisodeMatch,
			"decision_result", fmt.Sprintf("%s -> E%02d", originalKey, m.TargetEpisode),
			"decision_reason", m.AcceptedBy,
			"match_score", m.Score,
			"weighted_match_score", m.WeightedScore,
			"raw_match_score", m.RawScore,
			"match_confidence", m.Confidence,
			"confidence_quality", m.ConfidenceQuality,
			"verification_reason", m.VerificationReason,
			"rip_runner_up_episode", m.RunnerUpEpisode,
			"rip_runner_up_score", m.RunnerUpScore,
			"rip_score_margin", m.ScoreMargin,
			"episode_runner_up_key", m.EpisodeRunnerUpKey,
			"episode_runner_up_score", m.EpisodeRunnerUpScore,
			"episode_score_margin", m.EpisodeScoreMargin,
			"neighbor_runner_up_episode", m.NeighborRunnerUpEpisode,
			"neighbor_runner_up_score", m.NeighborRunnerUpScore,
			"neighbor_score_margin", m.NeighborScoreMargin,
			"reference_suspect", m.ReferenceSuspect,
			"reference_suspect_reason", m.ReferenceSuspectReason,
		)
		if m.Confidence < h.policy.LowConfidenceReviewThreshold {
			lowConfCount++
			ep.AppendReviewReason(fmt.Sprintf("Episode ID: confidence %.3f below threshold %.2f", m.Confidence, h.policy.LowConfidenceReviewThreshold))
			logger.Warn("low confidence episode match",
				"event_type", "low_confidence_match",
				"error_hint", fmt.Sprintf("%s matched E%02d with confidence %.3f and score %.3f", ep.Key, m.TargetEpisode, m.Confidence, m.Score),
				"impact", "match may be incorrect",
				"confidence_quality", m.ConfidenceQuality,
				"match_score", m.Score,
				"weighted_match_score", m.WeightedScore,
				"raw_match_score", m.RawScore,
				"match_confidence", m.Confidence,
				"rip_runner_up_episode", m.RunnerUpEpisode,
				"rip_runner_up_score", m.RunnerUpScore,
				"rip_score_margin", m.ScoreMargin,
				"episode_runner_up_key", m.EpisodeRunnerUpKey,
				"episode_runner_up_score", m.EpisodeRunnerUpScore,
				"episode_score_margin", m.EpisodeScoreMargin,
				"neighbor_runner_up_episode", m.NeighborRunnerUpEpisode,
				"neighbor_runner_up_score", m.NeighborRunnerUpScore,
				"neighbor_score_margin", m.NeighborScoreMargin,
				"reference_suspect", m.ReferenceSuspect,
			)
		}
	}
	applyOpeningDoubleEpisode(logger, env, seasonNum, env.Metadata.DiscNumber, episodeDetails, assetKeyRemap)
	env.Assets.RemapEpisodeKeys(assetKeyRemap)

	if unresolvedCount > 0 {
		sess.AddReviewReason(fmt.Sprintf("Episode ID: %d of %d episodes unresolved", unresolvedCount, len(env.Episodes)))
	}
	if lowConfCount > 0 {
		sess.AddReviewReason(fmt.Sprintf("Episode ID: %d matches below confidence threshold %.2f", lowConfCount, h.policy.LowConfidenceReviewThreshold))
	}
}

func structuralReviewReasons(matches []matchResult, discNumber int) []string {
	if len(matches) == 0 {
		return nil
	}
	episodes := assignedEpisodes(matches)
	if len(episodes) == 0 {
		return nil
	}
	reasons := make([]string, 0, 2)
	if discNumber == 1 && episodes[0] > 1 {
		reasons = append(reasons, fmt.Sprintf("disc 1 matched subset starts at episode %d", episodes[0]))
	}
	if fragmentedEpisodeSubset(episodes) {
		reasons = append(reasons, "accepted episode subset is fragmented")
	}
	return reasons
}

func fragmentedEpisodeSubset(episodes []int) bool {
	if len(episodes) < 3 {
		return false
	}
	gaps := 0
	for i := 1; i < len(episodes); i++ {
		if episodes[i]-episodes[i-1] > 1 {
			gaps++
		}
	}
	return gaps > 1
}

func hasSuspectAcceptedMatch(matches []matchResult) bool {
	for _, match := range matches {
		if match.ReferenceSuspect {
			return true
		}
	}
	return false
}

func recordAssetKeyRemap(remap map[string]string, oldKey, newKey string) {
	if oldKey == "" || newKey == "" || oldKey == newKey {
		return
	}
	remap[oldKey] = newKey
	for priorOld, priorNew := range remap {
		if priorNew == oldKey {
			remap[priorOld] = newKey
		}
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
	recordAssetKeyRemap(assetKeyRemap, originalKey, env.Episodes[0].Key)
	if start == 1 {
		for i := 1; i < len(env.Episodes); i++ {
			oldKey := env.Episodes[i].Key
			env.Episodes[i].Episode++
			env.Episodes[i].Key = ripspec.EpisodeKey(seasonNum, env.Episodes[i].Episode)
			if title, ok := details[env.Episodes[i].Episode]; ok {
				env.Episodes[i].EpisodeTitle = strings.TrimSpace(title.Name)
				env.Episodes[i].EpisodeAirDate = strings.TrimSpace(title.AirDate)
			}
			recordAssetKeyRemap(assetKeyRemap, oldKey, env.Episodes[i].Key)
		}
	}
	logger.Info("opening double-length episode inferred",
		"decision_type", logs.DecisionEpisodeMatch,
		"decision_result", env.Episodes[0].Key,
		"decision_reason", "disc 1 opening title runtime matches double-episode profile",
	)
}
