package contentid

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/subtitles"
	"spindle/internal/subtitles/opensubtitles"
	"spindle/internal/textutil"
)

// Progress phase constants reported by MatchWithProgress.
const (
	PhaseTranscribe = "transcribe"
	PhaseReference  = "reference"
	PhaseApply      = "apply"
)

// Matcher coordinates WhisperX transcription and OpenSubtitles comparison to
// derive per-episode mappings for ripped discs.
type Matcher struct {
	cfg       *config.Config
	logger    *slog.Logger
	subs      subtitleGenerator
	openSubs  openSubtitlesClient
	tmdb      seasonFetcher
	languages []string
	cache     *opensubtitles.Cache
	llm       llmVerifier // optional: second-level episode verification
	policy    Policy
}

type subtitleGenerator interface {
	Generate(ctx context.Context, req subtitles.GenerateRequest) (subtitles.GenerateResult, error)
}

type openSubtitlesClient interface {
	Search(ctx context.Context, req opensubtitles.SearchRequest) (opensubtitles.SearchResponse, error)
	Download(ctx context.Context, fileID int64, opts opensubtitles.DownloadOptions) (opensubtitles.DownloadResult, error)
}

type seasonFetcher interface {
	GetSeasonDetails(ctx context.Context, tmdbID int64, season int) (*tmdb.SeasonDetails, error)
}

// Option customises the Matcher.
type Option func(*Matcher)

// WithSubtitleGenerator overrides the WhisperX executor (primarily for tests).
func WithSubtitleGenerator(gen subtitleGenerator) Option {
	return func(m *Matcher) {
		if gen != nil {
			m.subs = gen
		}
	}
}

// WithOpenSubtitlesClient injects a custom OpenSubtitles client.
func WithOpenSubtitlesClient(client openSubtitlesClient) Option {
	return func(m *Matcher) {
		if client != nil {
			m.openSubs = client
		}
	}
}

// WithSeasonFetcher overrides the TMDB season lookup client.
func WithSeasonFetcher(fetcher seasonFetcher) Option {
	return func(m *Matcher) {
		if fetcher != nil {
			m.tmdb = fetcher
		}
	}
}

// WithLLMClient injects an LLM client for second-level episode verification.
func WithLLMClient(client llmVerifier) Option {
	return func(m *Matcher) {
		if client != nil {
			m.llm = client
		}
	}
}

// WithLanguages overrides the preferred subtitle languages.
func WithLanguages(langs []string) Option {
	return func(m *Matcher) {
		if len(langs) > 0 {
			m.languages = append([]string(nil), langs...)
		}
	}
}

// WithPolicy overrides matching thresholds and rules.
func WithPolicy(policy Policy) Option {
	return func(m *Matcher) {
		m.policy = policy.normalized()
	}
}

// NewMatcher constructs a content identification matcher bound to the supplied configuration.
func NewMatcher(cfg *config.Config, logger *slog.Logger, opts ...Option) *Matcher {
	m := &Matcher{cfg: cfg, policy: DefaultPolicy()}
	m.SetLogger(logger)
	for _, opt := range opts {
		opt(m)
	}
	if m.languages == nil {
		if cfg != nil && len(cfg.Subtitles.OpenSubtitlesLanguages) > 0 {
			m.languages = append([]string(nil), cfg.Subtitles.OpenSubtitlesLanguages...)
		} else {
			m.languages = []string{"en"}
		}
	}
	if m.subs == nil && cfg != nil {
		m.subs = subtitles.NewService(cfg, m.logger)
	}
	if m.openSubs == nil && cfg != nil && cfg.Subtitles.OpenSubtitlesEnabled {
		client, err := opensubtitles.New(opensubtitles.Config{
			APIKey:    cfg.Subtitles.OpenSubtitlesAPIKey,
			UserAgent: cfg.Subtitles.OpenSubtitlesUserAgent,
			UserToken: cfg.Subtitles.OpenSubtitlesUserToken,
		})
		if err != nil {
			m.logger.Warn("opensubtitles client unavailable",
				logging.Error(err),
				logging.String(logging.FieldEventType, "opensubtitles_client_unavailable"),
				logging.String(logging.FieldImpact, "episode matching will skip OpenSubtitles references"),
				logging.String(logging.FieldErrorHint, "Check opensubtitles_api_key and network connectivity"))
		} else {
			m.openSubs = client
		}
	}
	if m.cache == nil && cfg != nil {
		dir := strings.TrimSpace(cfg.Paths.OpenSubtitlesCacheDir)
		if dir != "" {
			cache, err := opensubtitles.NewCache(dir, m.logger)
			if err != nil {
				m.logger.Warn("opensubtitles cache unavailable",
					logging.Error(err),
					logging.String(logging.FieldEventType, "opensubtitles_cache_unavailable"),
					logging.String(logging.FieldImpact, "OpenSubtitles cache disabled for content matching"),
					logging.String(logging.FieldErrorHint, "Check opensubtitles_cache_dir permissions"))
			} else {
				m.cache = cache
			}
		}
	}
	if m.tmdb == nil && cfg != nil {
		client, err := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language)
		if err != nil {
			m.logger.Warn("tmdb client unavailable",
				logging.Error(err),
				logging.String(logging.FieldEventType, "tmdb_client_unavailable"),
				logging.String(logging.FieldImpact, "episode matching cannot load TMDB season details"),
				logging.String(logging.FieldErrorHint, "Check tmdb_api_key and tmdb_base_url in config"))
		} else {
			m.tmdb = client
		}
	}
	return m
}

// Policy returns the matcher's effective policy.
func (m *Matcher) Policy() Policy {
	if m == nil {
		return DefaultPolicy()
	}
	return m.policy.normalized()
}

// SetLogger swaps the matcher logger and propagates the scoped logger to dependencies.
func (m *Matcher) SetLogger(logger *slog.Logger) {
	if m == nil {
		return
	}
	m.logger = logging.NewComponentLogger(logger, "contentid")
	if setter, ok := m.subs.(interface{ SetLogger(*slog.Logger) }); ok {
		setter.SetLogger(logger)
	}
}

// Match analyzes ripped episode assets with WhisperX, compares them to OpenSubtitles,
// and updates the rip specification with definitive episode mappings when possible.
// The queue item metadata is updated in-place when matches are found.
func (m *Matcher) Match(ctx context.Context, item *queue.Item, env *ripspec.Envelope) (bool, error) {
	return m.MatchWithProgress(ctx, item, env, nil)
}

// MatchWithProgress behaves like Match but reports per-episode milestones through progress.
// The callback is best-effort and must remain fast; errors are ignored.
func (m *Matcher) MatchWithProgress(ctx context.Context, item *queue.Item, env *ripspec.Envelope, progress func(phase string, current, total int, episodeKey string)) (bool, error) {
	if m == nil || env == nil || len(env.Episodes) == 0 {
		return false, nil
	}
	if err := m.ensureReady(); err != nil {
		return false, err
	}
	ctxData, err := m.buildContext(item, env)
	if err != nil {
		return false, err
	}
	if ctxData.Season <= 0 {
		return false, errors.New("season number unavailable for content id")
	}
	seasonDetails, err := m.tmdb.GetSeasonDetails(ctx, ctxData.SubtitleCtx.TMDBID, ctxData.Season)
	if err != nil {
		return false, fmt.Errorf("fetch tmdb season: %w", err)
	}
	if seasonDetails == nil || len(seasonDetails.Episodes) == 0 {
		return false, errors.New("tmdb season returned no episodes")
	}
	stagingRoot := item.StagingRoot(m.cfg.Paths.StagingDir)
	if stagingRoot == "" {
		return false, errors.New("staging root unavailable for content id")
	}
	ripPrints, err := m.generateEpisodeFingerprints(ctx, ctxData, env, stagingRoot, progress)
	if err != nil {
		return false, err
	}
	if len(ripPrints) == 0 {
		return false, errors.New("whisperx produced no transcripts for content id")
	}
	// Preserve raw (pre-IDF) vectors for rematching attempts.
	for i := range ripPrints {
		ripPrints[i].RawVector = ripPrints[i].Vector
	}

	candidatePlan := deriveCandidateEpisodes(env, seasonDetails, ctxData.DiscNumber, m.policy)
	allSeasonEpisodes := seasonEpisodeNumbers(seasonDetails)
	var allSeasonRefs []referenceFingerprint
	var selectedAnchor anchorSelection
	hasAnchor := false

	// Step 1/2: anchor attempts. First anchor uses rip #1, second anchor uses rip #2.
	// Anchor references are fetched from full-season candidates to recover from
	// incorrect disc-range assumptions.
	if len(allSeasonEpisodes) > 0 {
		refs, anchorErr := m.fetchReferenceFingerprints(ctx, ctxData, seasonDetails, allSeasonEpisodes, progress)
		if anchorErr != nil {
			m.logger.Warn("content id anchor reference fetch failed",
				logging.Error(anchorErr),
				logging.String(logging.FieldEventType, "contentid_anchor_fetch_failed"),
				logging.String(logging.FieldImpact, "falling back to heuristic candidate ranges"),
				logging.String(logging.FieldErrorHint, "check OpenSubtitles connectivity and metadata"))
		} else {
			for i := range refs {
				refs[i].RawVector = refs[i].Vector
			}
			allSeasonRefs = refs
			if anchor, ok := selectAnchorWindow(
				ripPrints,
				allSeasonRefs,
				len(seasonDetails.Episodes),
				m.policy.AnchorMinScore,
				m.policy.AnchorMinScoreMargin,
			); ok {
				selectedAnchor = anchor
				hasAnchor = true
				if m.logger != nil {
					m.logger.Info("content id anchor selected",
						logging.String(logging.FieldEventType, "decision_summary"),
						logging.String(logging.FieldDecisionType, "contentid_anchor"),
						logging.String("decision_result", "selected"),
						logging.String("decision_reason", anchor.Reason),
						logging.String("decision_options", "first_anchor, second_anchor, fallback"),
						logging.Int("anchor_rip_index", anchor.RipIndex),
						logging.Int("anchor_episode", anchor.TargetEpisode),
						logging.Float64("anchor_score", anchor.BestScore),
						logging.Float64("anchor_second_score", anchor.SecondBestScore),
						logging.Float64("anchor_margin", anchor.ScoreMargin),
						logging.Int("window_start", anchor.WindowStart),
						logging.Int("window_end", anchor.WindowEnd),
					)
				}
			} else if m.logger != nil {
				m.logger.Info("content id anchor skipped",
					logging.String(logging.FieldEventType, "decision_summary"),
					logging.String(logging.FieldDecisionType, "contentid_anchor"),
					logging.String("decision_result", "skipped"),
					logging.String("decision_reason", anchor.Reason),
					logging.String("decision_options", "first_anchor, second_anchor, fallback"),
				)
			}
		}
	}

	strategyAttempts := buildStrategyAttempts(candidatePlan, selectedAnchor, hasAnchor, allSeasonEpisodes)
	if len(strategyAttempts) == 0 {
		m.logger.Warn("no content id strategies available",
			logging.String(logging.FieldEventType, "contentid_no_strategies"),
			logging.String(logging.FieldImpact, "episode numbers remain unresolved"),
			logging.String(logging.FieldErrorHint, "verify rip spec episodes and season metadata"))
		return false, nil
	}

	var outcomes []strategyOutcome
	var selected strategyOutcome
	haveSelection := false
	for _, attempt := range strategyAttempts {
		outcome, evalErr := m.evaluateStrategy(ctx, ctxData, seasonDetails, ripPrints, allSeasonRefs, attempt, progress)
		if evalErr != nil {
			return false, evalErr
		}
		outcomes = append(outcomes, outcome)
		if !haveSelection || betterOutcome(outcome, selected) {
			selected = outcome
			haveSelection = true
		}
	}
	if !haveSelection || len(selected.Matches) == 0 {
		m.logger.Warn("no episode matches resolved",
			logging.String(logging.FieldEventType, "contentid_no_matches"),
			logging.String(logging.FieldImpact, "episode numbers remain unresolved"),
			logging.String(logging.FieldErrorHint, "check transcript quality and reference subtitle availability"))
		return false, nil
	}

	matches := selected.Matches
	refinement := selected.Refinement
	refPrints := selected.References
	logStrategySummary(m.logger, outcomes, selected)

	if len(matches) == 0 {
		m.logger.Warn("no episode matches resolved",
			logging.String(logging.FieldEventType, "contentid_no_matches"),
			logging.String(logging.FieldImpact, "episode numbers remain unresolved"),
			logging.String(logging.FieldErrorHint, "check transcript quality and reference subtitle availability"))
		return false, nil
	}

	if refinement.Displaced > 0 {
		m.logger.Info("content id block refinement applied",
			logging.String(logging.FieldEventType, "decision_summary"),
			logging.String(logging.FieldDecisionType, "contentid_block_refinement"),
			logging.String("decision_result", "refined"),
			logging.String("decision_reason", "contiguous_block_constraint"),
			logging.String("decision_options", "refine, skip"),
			logging.Int("block_start", refinement.BlockStart),
			logging.Int("block_end", refinement.BlockEnd),
			logging.Int("displaced", refinement.Displaced),
			logging.Int("gaps", refinement.Gaps),
			logging.Int("reassigned", refinement.Reassigned),
			logging.Bool("needs_review", refinement.NeedsReview),
		)
	}
	if refinement.NeedsReview {
		env.AppendReviewReason(refinement.ReviewReason)
	}

	// LLM-based second-level verification for low-confidence matches.
	if m.llm != nil {
		verified, vr := verifyMatches(ctx, m.llm, matches, ripPrints, refPrints, m.logger, m.policy.LLMVerifyThreshold)
		matches = verified
		if vr != nil && vr.Challenged > 0 && vr.NeedsReview {
			env.AppendReviewReason(vr.ReviewReason)
		}
	}

	m.applyMatches(env, seasonDetails, ctxData.ShowTitle, matches, progress)
	m.attachMatchAttributes(env, matches)
	m.attachStrategyAttributes(env, selected, outcomes)
	attachTranscriptPaths(env, ripPrints)
	markEpisodesSynchronized(env)
	m.updateMetadata(item, matches, ctxData.Season)
	if m.logger != nil {
		contextAttrs := []logging.Attr{
			logging.String("decision_options", "match, review"),
			logging.String("selected_strategy", selected.Attempt.Name),
			logging.Int("episodes_available", len(env.Episodes)),
			logging.Int("rip_transcripts", len(ripPrints)),
			logging.Int("reference_subtitles", len(refPrints)),
			logging.Int("matched_episodes", len(matches)),
		}

		infoAttrs := buildMatchSummaryAttrs("decision_summary", "contentid_matches", "selected", "matches_resolved", matches, maxLoggedContentIDMatches)
		infoAttrs = append(infoAttrs, contextAttrs...)
		m.logger.Info("content id alignment complete", logging.Args(infoAttrs...)...)

		debugAttrs := buildMatchSummaryAttrs("decision_summary_full", "contentid_matches", "selected", "matches_resolved", matches, 0)
		debugAttrs = append(debugAttrs, contextAttrs...)
		m.logger.Debug("content id alignment complete", logging.Args(debugAttrs...)...)
	}
	return true, nil
}

// applyIDFWeighting applies TF-IDF reweighting to rip and reference fingerprints.
// Common show vocabulary (e.g. character names in every episode) is downweighted
// so episode-distinctive terms drive similarity scores.
// Requires at least 2 references for IDF to provide useful discrimination.
// Vectors are rebuilt from RawVector; callers must set RawVector before calling.
func applyIDFWeighting(ripPrints []ripFingerprint, refPrints []referenceFingerprint) {
	if len(refPrints) < 2 {
		return
	}
	corpus := textutil.NewCorpus()
	for _, ref := range refPrints {
		corpus.Add(ref.RawVector)
	}
	idf := corpus.IDF()
	if len(idf) == 0 {
		return
	}
	for i := range ripPrints {
		ripPrints[i].Vector = ripPrints[i].RawVector.WithIDF(idf)
	}
	for i := range refPrints {
		refPrints[i].Vector = refPrints[i].RawVector.WithIDF(idf)
	}
}

func (m *Matcher) ensureReady() error {
	if m.cfg == nil {
		return errors.New("configuration unavailable")
	}
	if m.subs == nil {
		return errors.New("subtitle generator unavailable")
	}
	if m.openSubs == nil {
		return errors.New("opensubtitles client unavailable")
	}
	if m.tmdb == nil {
		return errors.New("tmdb client unavailable")
	}
	return nil
}

type episodeContext struct {
	ShowTitle   string
	Season      int
	DiscNumber  int
	Metadata    queue.Metadata
	SubtitleCtx subtitles.SubtitleContext
	ItemID      int64
}

func (m *Matcher) buildContext(item *queue.Item, env *ripspec.Envelope) (episodeContext, error) {
	var ctx episodeContext
	if item == nil {
		return ctx, errors.New("queue item unavailable")
	}
	ctx.Metadata = queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)
	ctx.ItemID = item.ID
	ctx.ShowTitle = strings.TrimSpace(ctx.Metadata.ShowTitle)
	if ctx.ShowTitle == "" {
		ctx.ShowTitle = strings.TrimSpace(ctx.Metadata.Title())
	}
	if ctx.ShowTitle == "" {
		ctx.ShowTitle = strings.TrimSpace(item.DiscTitle)
	}
	ctx.Season = ctx.Metadata.SeasonNumber
	if ctx.Season <= 0 && env != nil {
		for _, episode := range env.Episodes {
			if episode.Season > 0 {
				ctx.Season = episode.Season
				break
			}
		}
	}
	if ctx.Season <= 0 {
		ctx.Season = 1
	}
	ctx.SubtitleCtx = subtitles.BuildSubtitleContext(item)
	if ctx.SubtitleCtx.TMDBID == 0 {
		return ctx, errors.New("tmdb id missing from metadata")
	}
	ctx.SubtitleCtx.MediaType = "episode"
	if ctx.SubtitleCtx.Title == "" {
		ctx.SubtitleCtx.Title = ctx.ShowTitle
	}
	if env != nil && env.Attributes.DiscNumber > 0 {
		ctx.DiscNumber = env.Attributes.DiscNumber
	}
	return ctx, nil
}

func (m *Matcher) generateEpisodeFingerprints(ctx context.Context, info episodeContext, env *ripspec.Envelope, stagingRoot string, progress func(phase string, current, total int, episodeKey string)) ([]ripFingerprint, error) {
	episodeDir := filepath.Join(stagingRoot, "contentid")
	if err := os.MkdirAll(episodeDir, 0o755); err != nil {
		return nil, fmt.Errorf("create contentid dir: %w", err)
	}
	type episodeWork struct {
		episode   ripspec.Episode
		assetPath string
	}
	work := make([]episodeWork, 0, len(env.Episodes))
	for _, episode := range env.Episodes {
		asset, ok := env.Assets.FindAsset(ripspec.AssetKindRipped, episode.Key)
		if !ok || strings.TrimSpace(asset.Path) == "" {
			continue
		}
		work = append(work, episodeWork{episode: episode, assetPath: asset.Path})
	}
	fingerprints := make([]ripFingerprint, 0, len(work))
	for idx, ew := range work {
		episode := ew.episode
		workDir := filepath.Join(episodeDir, episode.Key)
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return nil, fmt.Errorf("create workdir %s: %w", workDir, err)
		}
		language := info.SubtitleCtx.Language
		if language == "" && len(m.languages) > 0 {
			language = m.languages[0]
		}
		req := subtitles.GenerateRequest{
			SourcePath: ew.assetPath,
			WorkDir:    workDir,
			OutputDir:  workDir,
			BaseName:   fmt.Sprintf("%s-contentid", episode.Key),
			Language:   language,
			Context:    info.SubtitleCtx,
		}
		req.Context.Title = fmt.Sprintf("%s %s", info.ShowTitle, strings.ToUpper(episode.Key))
		result, err := m.subs.Generate(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("whisperx generate for %s: %w", episode.Key, err)
		}
		text, err := loadPlainText(result.SubtitlePath)
		if err != nil {
			return nil, fmt.Errorf("read whisperx subtitle %s: %w", result.SubtitlePath, err)
		}
		fp := newFingerprint(text)
		if fp == nil {
			return nil, fmt.Errorf("empty whisperx transcript for %s", episode.Key)
		}
		fingerprints = append(fingerprints, ripFingerprint{
			EpisodeKey: episode.Key,
			TitleID:    episode.TitleID,
			Path:       result.SubtitlePath,
			Vector:     fp,
		})
		if progress != nil {
			progress(PhaseTranscribe, idx+1, len(work), episode.Key)
		}
		m.logger.Debug("content id whisperx transcript ready",
			logging.String("episode_key", episode.Key),
			logging.String("subtitle_file", result.SubtitlePath),
			logging.Int("token_count", fp.TokenCount()),
		)
	}
	return fingerprints, nil
}

func formatMatchSummary(match matchResult) string {
	episodeLabel := strings.ToUpper(strings.TrimSpace(match.EpisodeKey))
	if episodeLabel == "" {
		episodeLabel = "UNKNOWN"
	}
	return fmt.Sprintf("%s -> E%02d (score=%.2f, title_id=%d, subtitle_file_id=%d, lang=%s)",
		episodeLabel,
		match.TargetEpisode,
		match.Score,
		match.TitleID,
		match.SubtitleFileID,
		strings.TrimSpace(match.SubtitleLanguage),
	)
}

const maxLoggedContentIDMatches = 6

func buildMatchSummaryAttrs(eventType, decisionType, result, reason string, matches []matchResult, limit int) []logging.Attr {
	attrs := []logging.Attr{
		logging.String(logging.FieldEventType, eventType),
		logging.String(logging.FieldDecisionType, decisionType),
		logging.String("decision_result", result),
		logging.String("decision_reason", reason),
		logging.Int("selected_count", len(matches)),
	}
	if limit <= 0 || limit > len(matches) {
		limit = len(matches)
	}
	if limit < len(matches) {
		attrs = append(attrs, logging.Int("selected_hidden_count", len(matches)-limit))
	}
	for idx := 0; idx < limit; idx++ {
		match := matches[idx]
		key := fmt.Sprintf("selected_%02d", match.TargetEpisode)
		if match.TargetEpisode <= 0 {
			key = fmt.Sprintf("selected_%d", idx+1)
		}
		attrs = append(attrs, logging.String(key, formatMatchSummary(match)))
	}
	return attrs
}

func findEpisodeByNumber(season *tmdb.SeasonDetails, number int) (tmdb.Episode, bool) {
	if season == nil {
		return tmdb.Episode{}, false
	}
	for _, episode := range season.Episodes {
		if episode.EpisodeNumber == number {
			return episode, true
		}
	}
	return tmdb.Episode{}, false
}
