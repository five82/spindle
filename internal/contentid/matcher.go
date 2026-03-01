package contentid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/identification"
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

// NewMatcher constructs a content identification matcher bound to the supplied configuration.
func NewMatcher(cfg *config.Config, logger *slog.Logger, opts ...Option) *Matcher {
	m := &Matcher{cfg: cfg}
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
	candidatePlan := deriveCandidateEpisodes(env, seasonDetails, ctxData.DiscNumber)
	if m.logger != nil {
		options := candidatePlan.Options()
		attrs := []logging.Attr{
			logging.String(logging.FieldEventType, "decision_summary"),
			logging.String(logging.FieldDecisionType, "contentid_candidates"),
			logging.String("decision_result", "selected"),
			logging.String("decision_reason", "derived_from_ripspec"),
			logging.String("decision_options", "match, skip"),
			logging.Int("selected_count", len(candidatePlan.Episodes)),
			logging.Int("source_count", len(candidatePlan.Sources)),
			logging.Int("disc_number", ctxData.DiscNumber),
			logging.Int("season_episodes", len(seasonDetails.Episodes)),
		}
		for _, episode := range candidatePlan.Episodes {
			attrs = append(attrs, logging.String(fmt.Sprintf("selected_%02d", episode), fmt.Sprintf("E%02d", episode)))
		}
		for idx, source := range candidatePlan.Sources {
			attrs = append(attrs, logging.String(fmt.Sprintf("source_%d", idx+1), source))
		}
		attrs = appendCandidatePlanOptions(attrs, options)
		m.logger.Debug("content id candidate episodes", logging.Args(attrs...)...)
	}
	refPrints, err := m.fetchReferenceFingerprints(ctx, ctxData, seasonDetails, candidatePlan.Episodes, progress)
	if err != nil {
		return false, err
	}
	if len(refPrints) == 0 {
		m.logger.Warn("no opensubtitles references available",
			logging.String(logging.FieldEventType, "contentid_no_references"),
			logging.String(logging.FieldImpact, "episode numbers remain unresolved"),
			logging.String(logging.FieldErrorHint, "verify OpenSubtitles languages and TMDB metadata"))
		return false, nil
	}
	// Preserve raw (pre-IDF) vectors for potential expansion retries.
	for i := range ripPrints {
		ripPrints[i].RawVector = ripPrints[i].Vector
	}
	for i := range refPrints {
		refPrints[i].RawVector = refPrints[i].Vector
	}
	applyIDFWeighting(ripPrints, refPrints)
	matches := resolveEpisodeMatches(ripPrints, refPrints)
	if len(matches) == 0 {
		m.logger.Warn("no episode matches resolved",
			logging.String(logging.FieldEventType, "contentid_no_matches"),
			logging.String(logging.FieldImpact, "episode numbers remain unresolved"),
			logging.String(logging.FieldErrorHint, "check transcript quality and reference subtitle availability"))
		return false, nil
	}
	// Enforce contiguous block constraint: disc episodes should map to a
	// consecutive range. Reassign outliers to gaps within the block.
	matches, refinement := refineMatchBlock(matches, refPrints, ripPrints, len(seasonDetails.Episodes), ctxData.DiscNumber)
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
	// Candidate range expansion: if the refined block extends beyond the
	// original candidate range, fetch missing references and re-match.
	if refinement.BlockEnd > 0 {
		refSet := make(map[int]struct{}, len(refPrints))
		for _, ref := range refPrints {
			refSet[ref.EpisodeNumber] = struct{}{}
		}
		var missingEps []int
		for ep := refinement.BlockStart; ep <= refinement.BlockEnd; ep++ {
			if _, inRef := refSet[ep]; !inRef {
				missingEps = append(missingEps, ep)
			}
		}
		if len(missingEps) > 0 {
			newRefs, fetchErr := m.fetchReferenceFingerprints(ctx, ctxData, seasonDetails, missingEps, progress)
			if fetchErr != nil {
				m.logger.Warn("candidate range expansion fetch failed",
					logging.Error(fetchErr),
					logging.String(logging.FieldEventType, "contentid_range_expansion_failed"),
					logging.String(logging.FieldImpact, "continuing with partial matches"),
					logging.String(logging.FieldErrorHint, "check OpenSubtitles connectivity"),
				)
			} else if len(newRefs) > 0 {
				for i := range newRefs {
					newRefs[i].RawVector = newRefs[i].Vector
				}
				refPrints = append(refPrints, newRefs...)
				applyIDFWeighting(ripPrints, refPrints)
				matches = resolveEpisodeMatches(ripPrints, refPrints)
				if len(matches) > 0 {
					matches, refinement = refineMatchBlock(matches, refPrints, ripPrints, len(seasonDetails.Episodes), ctxData.DiscNumber)
				}
				m.logger.Info("content id candidate range expanded",
					logging.String(logging.FieldEventType, "decision_summary"),
					logging.String(logging.FieldDecisionType, "contentid_range_expansion"),
					logging.String("decision_result", "expanded"),
					logging.String("decision_reason", "block_extends_beyond_candidates"),
					logging.String("decision_options", "expand, skip"),
					logging.Int("new_references", len(newRefs)),
					logging.Int("total_references", len(refPrints)),
					logging.Int("matches_after", len(matches)),
				)
			}
		}
	}

	if refinement.NeedsReview {
		env.AppendReviewReason(refinement.ReviewReason)
	}

	// LLM-based second-level verification for low-confidence matches.
	if m.llm != nil {
		verified, vr := verifyMatches(ctx, m.llm, matches, ripPrints, refPrints, m.logger)
		matches = verified
		if vr != nil && vr.Challenged > 0 && vr.NeedsReview {
			env.AppendReviewReason(vr.ReviewReason)
		}
	}

	m.applyMatches(env, seasonDetails, ctxData.ShowTitle, matches, progress)
	m.attachMatchAttributes(env, matches)
	attachTranscriptPaths(env, ripPrints)
	markEpisodesSynchronized(env)
	m.updateMetadata(item, matches, ctxData.Season)
	if m.logger != nil {
		contextAttrs := []logging.Attr{
			logging.String("decision_options", "match, review"),
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
	if env != nil && len(env.Attributes) > 0 {
		if disc, ok := asInt(env.Attributes[ripspec.AttrDiscNumber]); ok {
			ctx.DiscNumber = disc
		}
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

func (m *Matcher) fetchReferenceFingerprints(ctx context.Context, info episodeContext, season *tmdb.SeasonDetails, candidates []int, progress func(phase string, current, total int, episodeKey string)) ([]referenceFingerprint, error) {
	references := make([]referenceFingerprint, 0, len(candidates))
	unique := make([]int, 0, len(candidates))
	seen := make(map[int]struct{}, len(candidates))
	for _, num := range candidates {
		if _, ok := seen[num]; ok {
			continue
		}
		seen[num] = struct{}{}
		unique = append(unique, num)
	}
	var lastAPICall time.Time
	for idx, num := range unique {
		episodeKey := fmt.Sprintf("s%02de%02d", season.SeasonNumber, num)
		episodeData, ok := findEpisodeByNumber(season, num)
		if !ok {
			if progress != nil {
				progress(PhaseReference, idx+1, len(unique), episodeKey)
			}
			continue
		}
		episodeYear := strings.TrimSpace(episodeData.AirDate)
		if len(episodeYear) >= 4 {
			episodeYear = episodeYear[:4]
		} else {
			episodeYear = ""
		}
		parentID := info.SubtitleCtx.ParentID()
		searchReq := opensubtitles.SearchRequest{
			ParentTMDBID: parentID,
			Query:        info.ShowTitle,
			Languages:    append([]string(nil), m.languages...),
			Season:       season.SeasonNumber,
			Episode:      episodeData.EpisodeNumber,
			MediaType:    "episode",
			Year:         episodeYear,
		}
		searchVariants := opensubtitles.EpisodeSearchVariants(searchReq, info.ShowTitle, season.SeasonNumber, episodeData.EpisodeNumber, episodeData.ID)
		var (
			resp       opensubtitles.SearchResponse
			selected   opensubtitles.SearchRequest
			searchErr  error
			foundMatch bool
		)
		for attempt, variant := range searchVariants {
			searchErr = m.invokeOpenSubtitles(ctx, &lastAPICall, func() error {
				var err error
				resp, err = m.openSubs.Search(ctx, variant)
				return err
			})
			if searchErr != nil {
				return nil, fmt.Errorf("opensubtitles search s%02de%02d attempt %d: %w", season.SeasonNumber, num, attempt+1, searchErr)
			}
			if len(resp.Subtitles) == 0 {
				if m.logger != nil {
					m.logger.Warn("opensubtitles returned no candidates",
						logging.Int("season", season.SeasonNumber),
						logging.Int("episode", num),
						logging.Int("attempt", attempt+1),
						logging.String(logging.FieldEventType, "opensubtitles_no_candidates"),
						logging.String(logging.FieldImpact, "episode matching may fall back to WhisperX-only heuristics"),
						logging.String(logging.FieldErrorHint, "Verify OpenSubtitles languages and TMDB metadata"),
					)
				}
				continue
			}
			selected = variant
			foundMatch = true
			if m.logger != nil {
				m.logger.Debug("opensubtitles reference search selected",
					logging.String(logging.FieldEventType, "decision_summary"),
					logging.String(logging.FieldDecisionType, "opensubtitles_reference_search"),
					logging.String("decision_result", "selected"),
					logging.String("decision_reason", "candidates_available"),
					logging.String("decision_options", "search, skip"),
					logging.Int("season", season.SeasonNumber),
					logging.Int("episode", num),
					logging.Int("attempt", attempt+1),
					logging.Int("attempts_total", len(searchVariants)),
					logging.Int("candidates", len(resp.Subtitles)),
				)
			}
			break
		}
		if !foundMatch {
			if m.logger != nil {
				m.logger.Debug("opensubtitles reference search skipped",
					logging.String(logging.FieldEventType, "decision_summary"),
					logging.String(logging.FieldDecisionType, "opensubtitles_reference_search"),
					logging.String("decision_result", "skipped"),
					logging.String("decision_reason", "no_candidates"),
					logging.String("decision_options", "search, skip"),
					logging.Int("season", season.SeasonNumber),
					logging.Int("episode", num),
					logging.Int("attempts_total", len(searchVariants)),
				)
			}
			if progress != nil {
				progress(PhaseReference, idx+1, len(unique), episodeKey)
			}
			continue
		}
		candidate, selectedIdx, selectionReason := selectReferenceCandidate(resp.Subtitles, episodeData.Name, season)
		if m.logger != nil {
			attrs := []logging.Attr{
				logging.String(logging.FieldEventType, "decision_summary"),
				logging.String(logging.FieldDecisionType, "opensubtitles_reference_pick"),
				logging.String("decision_result", "selected"),
				logging.String("decision_reason", selectionReason),
				logging.String("decision_options", "select, skip"),
				logging.Int("season", season.SeasonNumber),
				logging.Int("episode", episodeData.EpisodeNumber),
				logging.Int("candidate_count", len(resp.Subtitles)),
				logging.Int64("file_id", candidate.FileID),
				logging.String("language", strings.TrimSpace(candidate.Language)),
				logging.Int("downloads", candidate.Downloads),
				logging.String("release", strings.TrimSpace(candidate.Release)),
				logging.Bool("hearing_impaired", candidate.HearingImpaired),
			}
			if selectedIdx > 0 {
				attrs = append(attrs, logging.Int("skipped_candidates", selectedIdx))
			}
			if len(resp.Subtitles) > 1 {
				attrs = append(attrs, logging.Int("candidate_hidden_count", len(resp.Subtitles)-1))
			}
			m.logger.Debug("opensubtitles reference selection",
				logging.Args(attrs...)...,
			)
		}
		var (
			payload   opensubtitles.DownloadResult
			cachePath string
			cacheHit  bool
		)
		if m.cache != nil && candidate.FileID > 0 {
			if cached, ok, err := m.cache.Load(candidate.FileID); err != nil {
				m.logger.Warn("opensubtitles cache load failed",
					logging.Error(err),
					logging.String(logging.FieldEventType, "opensubtitles_cache_load_failed"),
					logging.String(logging.FieldImpact, "cache miss forces network download"),
					logging.String(logging.FieldErrorHint, "Check opensubtitles_cache_dir permissions"))
			} else if ok {
				payload = cached.DownloadResult()
				cachePath = cached.Path
				cacheHit = true
				m.logger.Debug("opensubtitles cache hit",
					logging.Int("season", season.SeasonNumber),
					logging.Int("episode", episodeData.EpisodeNumber),
					logging.Int64("file_id", candidate.FileID),
				)
			}
		}
		if !cacheHit {
			if err := m.invokeOpenSubtitles(ctx, &lastAPICall, func() error {
				var err error
				payload, err = m.openSubs.Download(ctx, candidate.FileID, opensubtitles.DownloadOptions{Format: "srt"})
				return err
			}); err != nil {
				return nil, fmt.Errorf("download opensubtitles file %d: %w", candidate.FileID, err)
			}
			if m.cache != nil && len(payload.Data) > 0 {
				entry := opensubtitles.CacheEntry{
					FileID:       candidate.FileID,
					Language:     payload.Language,
					FileName:     payload.FileName,
					DownloadURL:  payload.DownloadURL,
					TMDBID:       selected.TMDBID,
					ParentTMDBID: selected.ParentTMDBID,
					Season:       season.SeasonNumber,
					Episode:      episodeData.EpisodeNumber,
					FeatureTitle: candidate.FeatureTitle,
					FeatureYear:  candidate.FeatureYear,
				}
				if entry.ParentTMDBID == 0 {
					entry.ParentTMDBID = parentID
				}
				if entry.TMDBID == 0 {
					entry.TMDBID = info.SubtitleCtx.EpisodeID()
				}
				if path, err := m.cache.Store(entry, payload.Data); err != nil {
					m.logger.Warn("opensubtitles cache store failed",
						logging.Error(err),
						logging.String(logging.FieldEventType, "opensubtitles_cache_store_failed"),
						logging.String(logging.FieldImpact, "future runs will re-download reference subtitles"),
						logging.String(logging.FieldErrorHint, "Check opensubtitles_cache_dir permissions and free space"))
				} else {
					cachePath = path
				}
			}
		}
		text, err := normalizeSubtitlePayload(payload.Data)
		if err != nil {
			return nil, fmt.Errorf("normalize opensubtitles payload: %w", err)
		}
		fp := newFingerprint(text)
		if fp == nil {
			if progress != nil {
				progress(PhaseReference, idx+1, len(unique), episodeKey)
			}
			return nil, fmt.Errorf("empty opensubtitles transcript for S%02dE%02d", season.SeasonNumber, num)
		}
		references = append(references, referenceFingerprint{
			EpisodeNumber: episodeData.EpisodeNumber,
			Title:         strings.TrimSpace(episodeData.Name),
			Vector:        fp,
			FileID:        candidate.FileID,
			Language:      payload.Language,
			CachePath:     cachePath,
		})
		if progress != nil {
			progress(PhaseReference, idx+1, len(unique), episodeKey)
		}
		m.logger.Debug("opensubtitles reference downloaded",
			logging.Int("season", episodeData.SeasonNumber),
			logging.Int("episode", episodeData.EpisodeNumber),
			logging.String("title", episodeData.Name),
			logging.Int("token_count", fp.TokenCount()),
		)
	}
	return references, nil
}

// selectReferenceCandidate picks the best candidate from OpenSubtitles results.
// It skips candidates whose release name contains a different episode's TMDB
// title but not the expected episode's title, which indicates mislabeled metadata
// on OpenSubtitles. Among title-consistent candidates it prefers non-HI subtitles,
// since HI annotations dilute similarity scores against WhisperX transcripts.
// Falls back to the top result (highest download count) if all candidates appear
// suspect.
//
// Returned reason values:
//
//	"top_result"               – first candidate selected with no reranking
//	"title_consistency_rerank" – skipped higher-ranked candidates for title mismatch
//	"non_hi_preferred"         – selected a non-HI candidate over a higher-ranked HI one
//	"hi_fallback"              – all acceptable candidates are HI; picked the first
func selectReferenceCandidate(candidates []opensubtitles.Subtitle, episodeTitle string, season *tmdb.SeasonDetails) (opensubtitles.Subtitle, int, string) {
	if len(candidates) <= 1 {
		return candidates[0], 0, "top_result"
	}

	currentTitle := strings.ToLower(strings.TrimSpace(episodeTitle))
	const minTitleLen = 5
	if len(currentTitle) < minTitleLen {
		return preferNonHI(candidates)
	}

	// Collect TMDB titles from other episodes in the season.
	var otherTitles []string
	for _, ep := range season.Episodes {
		t := strings.ToLower(strings.TrimSpace(ep.Name))
		if t == currentTitle || len(t) < minTitleLen {
			continue
		}
		otherTitles = append(otherTitles, t)
	}
	if len(otherTitles) == 0 {
		return preferNonHI(candidates)
	}

	// Collect candidates that pass the title-consistency check.
	type indexedCandidate struct {
		sub opensubtitles.Subtitle
		idx int
	}
	var acceptable []indexedCandidate
	for i, c := range candidates {
		release := strings.ToLower(strings.TrimSpace(c.Release))
		if release == "" {
			acceptable = append(acceptable, indexedCandidate{c, i})
			continue
		}
		referencesOther := containsAnySubstring(release, otherTitles)
		if !referencesOther || strings.Contains(release, currentTitle) {
			acceptable = append(acceptable, indexedCandidate{c, i})
			continue
		}
		// Skip: release clearly references a different episode.
	}

	if len(acceptable) > 0 {
		// Among acceptable candidates, prefer non-HI.
		for j, ac := range acceptable {
			if !ac.sub.HearingImpaired {
				reason := "top_result"
				if j > 0 {
					// Skipped earlier acceptable candidates because they were HI.
					reason = "non_hi_preferred"
				} else if ac.idx > 0 {
					// First acceptable candidate, but title-consistency skipped earlier originals.
					reason = "title_consistency_rerank"
				}
				return ac.sub, ac.idx, reason
			}
		}
		// All acceptable candidates are HI -- return the first acceptable.
		first := acceptable[0]
		reason := "hi_fallback"
		if first.idx > 0 {
			reason = "title_consistency_rerank"
		}
		return first.sub, first.idx, reason
	}

	// All candidates look suspect -- prefer non-HI among them.
	return preferNonHI(candidates)
}

// preferNonHI returns the first non-HI candidate from the slice, falling back
// to the first element if all are HI.
func preferNonHI(candidates []opensubtitles.Subtitle) (opensubtitles.Subtitle, int, string) {
	for i, c := range candidates {
		if !c.HearingImpaired {
			if i > 0 {
				return c, i, "non_hi_preferred"
			}
			return c, 0, "top_result"
		}
	}
	return candidates[0], 0, "hi_fallback"
}

// containsAnySubstring reports whether s contains any of the given substrings.
func containsAnySubstring(s string, substrings []string) bool {
	for _, sub := range substrings {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func (m *Matcher) invokeOpenSubtitles(ctx context.Context, lastCall *time.Time, op func() error) error {
	if op == nil {
		return errors.New("opensubtitles operation unavailable")
	}
	attempt := 0
	for {
		if err := waitForOpenSubtitlesWindow(ctx, lastCall); err != nil {
			return err
		}
		err := op()
		if lastCall != nil {
			*lastCall = time.Now()
		}
		if err == nil {
			return nil
		}
		if !opensubtitles.IsRetriable(err) || attempt >= opensubtitles.MaxRateRetries {
			return err
		}
		attempt++
		backoff := opensubtitles.InitialBackoff * time.Duration(1<<uint(attempt-1))
		if backoff > opensubtitles.MaxBackoff {
			backoff = opensubtitles.MaxBackoff
		}
		if m.logger != nil {
			m.logger.Warn("opensubtitles rate limited",
				logging.Duration("backoff", backoff),
				logging.Int("attempt", attempt),
				logging.String(logging.FieldEventType, "opensubtitles_rate_limited"),
				logging.String(logging.FieldImpact, "episode matching delayed while respecting API limits"),
				logging.String(logging.FieldErrorHint, "Wait and retry or check OpenSubtitles rate limits"),
			)
		}
		if err := opensubtitles.SleepWithContext(ctx, backoff); err != nil {
			return err
		}
	}
}

func waitForOpenSubtitlesWindow(ctx context.Context, lastCall *time.Time) error {
	if ctx == nil {
		return errors.New("context unavailable")
	}
	if lastCall == nil || lastCall.IsZero() {
		return nil
	}
	elapsed := time.Since(*lastCall)
	if elapsed >= opensubtitles.MinInterval {
		return nil
	}
	return opensubtitles.SleepWithContext(ctx, opensubtitles.MinInterval-elapsed)
}

func (m *Matcher) applyMatches(env *ripspec.Envelope, season *tmdb.SeasonDetails, showTitle string, matches []matchResult, progress func(phase string, current, total int, episodeKey string)) {
	if env == nil || season == nil || len(matches) == 0 {
		return
	}
	titleByID := make(map[int]*ripspec.Title, len(env.Titles))
	for idx := range env.Titles {
		titleByID[env.Titles[idx].ID] = &env.Titles[idx]
	}
	episodeByNumber := make(map[int]tmdb.Episode, len(season.Episodes))
	for _, e := range season.Episodes {
		episodeByNumber[e.EpisodeNumber] = e
	}
	for idx, match := range matches {
		target, ok := episodeByNumber[match.TargetEpisode]
		if !ok {
			continue
		}
		if episode := env.EpisodeByKey(match.EpisodeKey); episode != nil {
			episode.Season = target.SeasonNumber
			episode.Episode = target.EpisodeNumber
			episode.EpisodeTitle = strings.TrimSpace(target.Name)
			episode.EpisodeAirDate = strings.TrimSpace(target.AirDate)
			episode.OutputBasename = identification.EpisodeOutputBasename(showTitle, target.SeasonNumber, target.EpisodeNumber)
			episode.MatchConfidence = match.Score
		}
		if title := titleByID[match.TitleID]; title != nil {
			title.Season = target.SeasonNumber
			title.Episode = target.EpisodeNumber
			title.EpisodeTitle = strings.TrimSpace(target.Name)
			title.EpisodeAirDate = strings.TrimSpace(target.AirDate)
		}
		if progress != nil {
			progress(PhaseApply, idx+1, len(matches), match.EpisodeKey)
		}
		m.logger.Debug("content id episode matched",
			logging.String("episode_key", match.EpisodeKey),
			logging.Int("title_id", match.TitleID),
			logging.Int("matched_episode", target.EpisodeNumber),
			logging.Float64("score", match.Score),
			logging.String("episode_title", strings.TrimSpace(target.Name)),
		)
	}
}

func (m *Matcher) attachMatchAttributes(env *ripspec.Envelope, matches []matchResult) {
	if env == nil || len(matches) == 0 {
		return
	}
	payload := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		entry := map[string]any{
			"episode_key":     match.EpisodeKey,
			"title_id":        match.TitleID,
			"matched_episode": match.TargetEpisode,
			"score":           match.Score,
		}
		if match.SubtitleFileID > 0 {
			entry["subtitle_file_id"] = match.SubtitleFileID
		}
		if strings.TrimSpace(match.SubtitleLanguage) != "" {
			entry["subtitle_language"] = match.SubtitleLanguage
		}
		if strings.TrimSpace(match.SubtitleCachePath) != "" {
			entry["subtitle_cache_path"] = match.SubtitleCachePath
		}
		payload = append(payload, entry)
	}
	env.SetAttribute(ripspec.AttrContentIDMatches, payload)
	env.SetAttribute(ripspec.AttrContentIDMethod, "whisperx_opensubtitles")
}

func attachTranscriptPaths(env *ripspec.Envelope, fingerprints []ripFingerprint) {
	if env == nil || len(fingerprints) == 0 {
		return
	}
	paths := make(map[string]string, len(fingerprints))
	for _, fp := range fingerprints {
		if strings.TrimSpace(fp.EpisodeKey) != "" && strings.TrimSpace(fp.Path) != "" {
			paths[strings.ToLower(strings.TrimSpace(fp.EpisodeKey))] = fp.Path
		}
	}
	if len(paths) > 0 {
		env.SetAttribute(ripspec.AttrContentIDTranscripts, paths)
	}
}

func markEpisodesSynchronized(env *ripspec.Envelope) {
	if env == nil {
		return
	}
	env.SetAttribute(ripspec.AttrEpisodesSynchronized, true)
}

func (m *Matcher) updateMetadata(item *queue.Item, matches []matchResult, season int) {
	if item == nil || len(matches) == 0 {
		return
	}
	episodes := make([]int, 0, len(matches))
	for _, match := range matches {
		if match.TargetEpisode > 0 {
			episodes = append(episodes, match.TargetEpisode)
		}
	}
	if len(episodes) == 0 {
		return
	}
	sort.Ints(episodes)
	episodes = slices.Compact(episodes)
	var payload map[string]any
	if strings.TrimSpace(item.MetadataJSON) != "" {
		if err := json.Unmarshal([]byte(item.MetadataJSON), &payload); err != nil {
			payload = make(map[string]any)
		}
	} else {
		payload = make(map[string]any)
	}
	payload["episode_numbers"] = episodes
	if season > 0 {
		payload["season_number"] = season
	}
	payload["media_type"] = "tv"
	data, err := json.Marshal(payload)
	if err != nil {
		m.logger.Warn("failed to encode metadata after content id",
			logging.Error(err),
			logging.String(logging.FieldEventType, "metadata_encode_failed"),
			logging.String(logging.FieldImpact, "episode metadata updates were not persisted"),
			logging.String(logging.FieldErrorHint, "Retry content identification or inspect metadata serialization errors"))
		return
	}
	item.MetadataJSON = string(data)
}

type candidateEpisodePlan struct {
	Episodes          []int
	Sources           []string
	RipSpecEpisodes   []int
	DiscBlockEpisodes []int
	SeasonFallback    []int
}

func (p candidateEpisodePlan) Options() map[string]any {
	return map[string]any{
		"rip_spec":        p.RipSpecEpisodes,
		"disc_block":      p.DiscBlockEpisodes,
		"season_fallback": p.SeasonFallback,
	}
}

func deriveCandidateEpisodes(env *ripspec.Envelope, season *tmdb.SeasonDetails, discNumber int) candidateEpisodePlan {
	plan := candidateEpisodePlan{}
	// Tier 1: collect resolved episode numbers from the rip spec.
	set := make(map[int]struct{}, len(env.Episodes)*2)
	for _, episode := range env.Episodes {
		if episode.Episode > 0 {
			set[episode.Episode] = struct{}{}
			plan.RipSpecEpisodes = append(plan.RipSpecEpisodes, episode.Episode)
		}
	}
	sort.Ints(plan.RipSpecEpisodes)
	if len(plan.RipSpecEpisodes) > 0 {
		plan.Sources = append(plan.Sources, "rip_spec")
	}

	// Tier 2: supplement resolved episodes with disc-block neighbors.
	// Only runs when Tier 1 found at least one resolved episode.
	totalEpisodes := len(season.Episodes)
	if len(set) > 0 && discNumber > 0 && totalEpisodes > 0 {
		block := len(env.Episodes)
		if block == 0 {
			block = 4
		}
		start := (discNumber - 1) * block
		if start >= totalEpisodes {
			start = totalEpisodes - block
		}
		if start < 0 {
			start = 0
		}
		for idx := start; idx < totalEpisodes && idx < start+block; idx++ {
			number := season.Episodes[idx].EpisodeNumber
			set[number] = struct{}{}
			plan.DiscBlockEpisodes = append(plan.DiscBlockEpisodes, number)
		}
		sort.Ints(plan.DiscBlockEpisodes)
		if len(plan.DiscBlockEpisodes) > 0 {
			plan.Sources = append(plan.Sources, "disc_block")
		}
	}

	// Tier 2b: disc-block estimate for placeholder episodes.
	// When Tier 1 found no resolved episodes but we know the disc number,
	// estimate which episodes belong on this disc rather than searching
	// the entire season.
	if len(set) == 0 && discNumber > 0 && totalEpisodes > 0 {
		block := len(env.Episodes)
		if block == 0 {
			block = 4
		}
		padding := max(2, block/4)
		start := (discNumber-1)*block - padding
		end := discNumber*block + padding
		if start < 0 {
			start = 0
		}
		if end > totalEpisodes {
			end = totalEpisodes
		}
		for idx := start; idx < end; idx++ {
			number := season.Episodes[idx].EpisodeNumber
			set[number] = struct{}{}
			plan.DiscBlockEpisodes = append(plan.DiscBlockEpisodes, number)
		}
		sort.Ints(plan.DiscBlockEpisodes)
		if len(plan.DiscBlockEpisodes) > 0 {
			plan.Sources = append(plan.Sources, "disc_block")
		}
	}

	// Tier 3: fall back to full season when no episodes were resolved.
	if len(set) == 0 {
		for _, episode := range season.Episodes {
			plan.SeasonFallback = append(plan.SeasonFallback, episode.EpisodeNumber)
			set[episode.EpisodeNumber] = struct{}{}
		}
		sort.Ints(plan.SeasonFallback)
		plan.Sources = append(plan.Sources, "season_fallback")
	}
	list := make([]int, 0, len(set))
	for number := range set {
		list = append(list, number)
	}
	sort.Ints(list)
	plan.Episodes = list
	return plan
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

func appendCandidatePlanOptions(attrs []logging.Attr, options map[string]any) []logging.Attr {
	if len(options) == 0 {
		return attrs
	}
	keys := make([]string, 0, len(options))
	for key := range options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	attrs = append(attrs, logging.Int("candidate_count", len(keys)))
	for _, key := range keys {
		label := fmt.Sprintf("candidate_%s", key)
		attrs = append(attrs, logging.String(label, formatCandidateOptionValue(options[key])))
	}
	return attrs
}

func formatCandidateOptionValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "none"
	case []int:
		if len(typed) == 0 {
			return "none"
		}
		parts := make([]string, 0, len(typed))
		for _, v := range typed {
			parts = append(parts, strconv.Itoa(v))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprint(value)
	}
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

func asInt(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), true
	case float32:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i), true
		}
	case string:
		if value := strings.TrimSpace(v); value != "" {
			if i, err := strconv.Atoi(value); err == nil {
				return i, true
			}
		}
	}
	return 0, false
}
