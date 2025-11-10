package contentid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/subtitles"
	"spindle/internal/subtitles/opensubtitles"
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
}

const (
	openSubtitlesMinInterval    = time.Second
	openSubtitlesMaxRateRetries = 4
	openSubtitlesInitialBackoff = 2 * time.Second
	openSubtitlesMaxBackoff     = 12 * time.Second
)

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
	contentLogger := logger
	if contentLogger != nil {
		contentLogger = contentLogger.With(logging.String("component", "contentid"))
	} else {
		contentLogger = logging.NewNop().With(logging.String("component", "contentid"))
	}
	m := &Matcher{
		cfg:    cfg,
		logger: contentLogger,
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.languages == nil {
		if cfg != nil && len(cfg.OpenSubtitlesLanguages) > 0 {
			m.languages = append([]string(nil), cfg.OpenSubtitlesLanguages...)
		} else {
			m.languages = []string{"en"}
		}
	}
	if m.subs == nil && cfg != nil {
		m.subs = subtitles.NewService(cfg, contentLogger)
	}
	if m.openSubs == nil && cfg != nil && cfg.OpenSubtitlesEnabled {
		client, err := opensubtitles.New(opensubtitles.Config{
			APIKey:    cfg.OpenSubtitlesAPIKey,
			UserAgent: cfg.OpenSubtitlesUserAgent,
			UserToken: cfg.OpenSubtitlesUserToken,
		})
		if err != nil {
			contentLogger.Warn("opensubtitles client unavailable", logging.Error(err))
		} else {
			m.openSubs = client
		}
	}
	if m.cache == nil && cfg != nil {
		dir := strings.TrimSpace(cfg.OpenSubtitlesCacheDir)
		if dir != "" {
			cache, err := opensubtitles.NewCache(dir, contentLogger)
			if err != nil {
				contentLogger.Warn("opensubtitles cache unavailable", logging.Error(err))
			} else {
				m.cache = cache
			}
		}
	}
	if m.tmdb == nil && cfg != nil {
		client, err := tmdb.New(cfg.TMDBAPIKey, cfg.TMDBBaseURL, cfg.TMDBLanguage)
		if err != nil {
			contentLogger.Warn("tmdb client unavailable", logging.Error(err))
		} else {
			m.tmdb = client
		}
	}
	return m
}

// Match analyzes ripped episode assets with WhisperX, compares them to OpenSubtitles,
// and updates the rip specification with definitive episode mappings when possible.
// The queue item metadata is updated in-place when matches are found.
func (m *Matcher) Match(ctx context.Context, item *queue.Item, env *ripspec.Envelope) (bool, error) {
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
	stagingRoot := item.StagingRoot(m.cfg.StagingDir)
	if stagingRoot == "" {
		return false, errors.New("staging root unavailable for content id")
	}
	ripPrints, err := m.generateEpisodeFingerprints(ctx, ctxData, env, stagingRoot)
	if err != nil {
		return false, err
	}
	if len(ripPrints) == 0 {
		return false, errors.New("whisperx produced no transcripts for content id")
	}
	candidateEpisodes := deriveCandidateEpisodes(env, seasonDetails, ctxData.DiscNumber)
	refPrints, err := m.fetchReferenceFingerprints(ctx, ctxData, seasonDetails, candidateEpisodes)
	if err != nil {
		return false, err
	}
	if len(refPrints) == 0 {
		return false, errors.New("no opensubtitles references downloaded for comparison")
	}
	matches := resolveEpisodeMatches(ripPrints, refPrints)
	if len(matches) == 0 {
		return false, errors.New("failed to correlate ripped episodes with opensubtitles references")
	}
	m.applyMatches(env, seasonDetails, ctxData.ShowTitle, matches)
	m.attachMatchAttributes(env, matches)
	m.updateMetadata(item, matches, ctxData.Season)
	return true, nil
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
}

func (m *Matcher) buildContext(item *queue.Item, env *ripspec.Envelope) (episodeContext, error) {
	var ctx episodeContext
	if item == nil {
		return ctx, errors.New("queue item unavailable")
	}
	ctx.Metadata = queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)
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
	if ctx.SubtitleCtx.MediaType == "" {
		ctx.SubtitleCtx.MediaType = "tv"
	}
	if ctx.SubtitleCtx.TMDBID == 0 {
		return ctx, errors.New("tmdb id missing from metadata")
	}
	ctx.SubtitleCtx.MediaType = "episode"
	if ctx.SubtitleCtx.Title == "" {
		ctx.SubtitleCtx.Title = ctx.ShowTitle
	}
	if env != nil && len(env.Attributes) > 0 {
		if disc, ok := asInt(env.Attributes["disc_number"]); ok {
			ctx.DiscNumber = disc
		}
	}
	return ctx, nil
}

func (m *Matcher) generateEpisodeFingerprints(ctx context.Context, info episodeContext, env *ripspec.Envelope, stagingRoot string) ([]ripFingerprint, error) {
	episodeDir := filepath.Join(stagingRoot, "contentid")
	if err := os.MkdirAll(episodeDir, 0o755); err != nil {
		return nil, fmt.Errorf("create contentid dir: %w", err)
	}
	fingerprints := make([]ripFingerprint, 0, len(env.Episodes))
	for _, episode := range env.Episodes {
		asset, ok := env.Assets.FindAsset("ripped", episode.Key)
		if !ok || strings.TrimSpace(asset.Path) == "" {
			continue
		}
		workDir := filepath.Join(episodeDir, episode.Key)
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return nil, fmt.Errorf("create workdir %s: %w", workDir, err)
		}
		language := info.SubtitleCtx.Language
		if language == "" && len(m.languages) > 0 {
			language = m.languages[0]
		}
		req := subtitles.GenerateRequest{
			SourcePath: asset.Path,
			WorkDir:    workDir,
			OutputDir:  workDir,
			BaseName:   fmt.Sprintf("%s-contentid", episode.Key),
			Language:   language,
			Context:    info.SubtitleCtx,
			ForceAI:    true,
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
		m.logger.Info("content id whisperx transcript ready",
			logging.String("episode_key", episode.Key),
			logging.String("subtitle_path", result.SubtitlePath),
			logging.Int("token_count", len(fp.tokens)),
		)
	}
	return fingerprints, nil
}

func (m *Matcher) fetchReferenceFingerprints(ctx context.Context, info episodeContext, season *tmdb.SeasonDetails, candidates []int) ([]referenceFingerprint, error) {
	references := make([]referenceFingerprint, 0, len(candidates))
	seen := make(map[int]struct{}, len(candidates))
	var lastAPICall time.Time
	for _, num := range candidates {
		if _, ok := seen[num]; ok {
			continue
		}
		seen[num] = struct{}{}
		episodeData, ok := findEpisodeByNumber(season, num)
		if !ok {
			continue
		}
		episodeYear := strings.TrimSpace(episodeData.AirDate)
		if len(episodeYear) >= 4 {
			episodeYear = episodeYear[:4]
		} else {
			episodeYear = ""
		}
		searchReq := opensubtitles.SearchRequest{
			ParentTMDBID: info.SubtitleCtx.TMDBID,
			Query:        info.ShowTitle,
			Languages:    append([]string(nil), m.languages...),
			Season:       season.SeasonNumber,
			Episode:      episodeData.EpisodeNumber,
			MediaType:    "episode",
			Year:         episodeYear,
		}
		searchVariants := episodeSearchVariants(searchReq, info.ShowTitle, season.SeasonNumber, episodeData.EpisodeNumber, episodeData.ID)
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
					)
				}
				continue
			}
			selected = variant
			foundMatch = true
			if attempt > 0 && m.logger != nil {
				m.logger.Info("opensubtitles fallback search succeeded",
					logging.Int("season", season.SeasonNumber),
					logging.Int("episode", num),
					logging.Int("attempt", attempt+1),
				)
			}
			break
		}
		if !foundMatch {
			continue
		}
		candidate := resp.Subtitles[0]
		var (
			payload   opensubtitles.DownloadResult
			cachePath string
			cacheHit  bool
		)
		if m.cache != nil && candidate.FileID > 0 {
			if cached, ok, err := m.cache.Load(candidate.FileID); err != nil {
				m.logger.Warn("opensubtitles cache load failed", logging.Error(err))
			} else if ok {
				payload = cached.DownloadResult()
				cachePath = cached.Path
				cacheHit = true
				m.logger.Info("opensubtitles cache hit",
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
				if path, err := m.cache.Store(entry, payload.Data); err != nil {
					m.logger.Warn("opensubtitles cache store failed", logging.Error(err))
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
		m.logger.Info("opensubtitles reference downloaded",
			logging.Int("season", episodeData.SeasonNumber),
			logging.Int("episode", episodeData.EpisodeNumber),
			logging.String("title", episodeData.Name),
			logging.Int("token_count", len(fp.tokens)),
		)
	}
	return references, nil
}

func episodeSearchVariants(base opensubtitles.SearchRequest, showTitle string, seasonNumber, episodeNumber int, episodeTMDBID int64) []opensubtitles.SearchRequest {
	variants := make([]opensubtitles.SearchRequest, 0, 3)

	primary := base
	primary.TMDBID = 0
	variants = append(variants, primary)

	if episodeTMDBID > 0 {
		episodeVariant := base
		episodeVariant.ParentTMDBID = 0
		episodeVariant.TMDBID = episodeTMDBID
		variants = append(variants, episodeVariant)
	}

	queryVariant := base
	queryVariant.ParentTMDBID = 0
	queryVariant.TMDBID = 0
	title := strings.TrimSpace(showTitle)
	if title != "" {
		queryVariant.Query = fmt.Sprintf("%s S%02dE%02d", title, seasonNumber, episodeNumber)
	} else {
		queryVariant.Query = fmt.Sprintf("S%02dE%02d", seasonNumber, episodeNumber)
	}
	variants = append(variants, queryVariant)

	unique := make([]opensubtitles.SearchRequest, 0, len(variants))
	seen := make(map[string]struct{}, len(variants))
	for _, variant := range variants {
		key := variantSignature(variant)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, variant)
	}
	return unique
}

func variantSignature(req opensubtitles.SearchRequest) string {
	var builder strings.Builder
	builder.Grow(128)
	builder.WriteString("tmdb=")
	builder.WriteString(strconv.FormatInt(req.TMDBID, 10))
	builder.WriteString("|parent=")
	builder.WriteString(strconv.FormatInt(req.ParentTMDBID, 10))
	builder.WriteString("|season=")
	builder.WriteString(strconv.Itoa(req.Season))
	builder.WriteString("|episode=")
	builder.WriteString(strconv.Itoa(req.Episode))
	builder.WriteString("|query=")
	builder.WriteString(strings.TrimSpace(req.Query))
	builder.WriteString("|languages=")
	builder.WriteString(strings.Join(req.Languages, ","))
	return builder.String()
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
		if !isOpenSubtitlesRateLimit(err) || attempt >= openSubtitlesMaxRateRetries {
			return err
		}
		attempt++
		backoff := openSubtitlesInitialBackoff * time.Duration(1<<uint(attempt-1))
		if backoff > openSubtitlesMaxBackoff {
			backoff = openSubtitlesMaxBackoff
		}
		if m.logger != nil {
			m.logger.Warn("opensubtitles rate limited",
				logging.Duration("backoff", backoff),
				logging.Int("attempt", attempt),
			)
		}
		if err := sleepWithContext(ctx, backoff); err != nil {
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
	if elapsed >= openSubtitlesMinInterval {
		return nil
	}
	return sleepWithContext(ctx, openSubtitlesMinInterval-elapsed)
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isOpenSubtitlesRateLimit(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "429") ||
		strings.Contains(strings.ToLower(err.Error()), "rate limit")
}

func (m *Matcher) applyMatches(env *ripspec.Envelope, season *tmdb.SeasonDetails, showTitle string, matches []matchResult) {
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
	for _, match := range matches {
		target, ok := episodeByNumber[match.TargetEpisode]
		if !ok {
			continue
		}
		if episode := env.EpisodeByKey(match.EpisodeKey); episode != nil {
			episode.Season = target.SeasonNumber
			episode.Episode = target.EpisodeNumber
			episode.EpisodeTitle = strings.TrimSpace(target.Name)
			episode.EpisodeAirDate = strings.TrimSpace(target.AirDate)
			episode.OutputBasename = buildEpisodeBasename(showTitle, target.SeasonNumber, target.EpisodeNumber)
		}
		if title := titleByID[match.TitleID]; title != nil {
			title.Season = target.SeasonNumber
			title.Episode = target.EpisodeNumber
			title.EpisodeTitle = strings.TrimSpace(target.Name)
			title.EpisodeAirDate = strings.TrimSpace(target.AirDate)
		}
		m.logger.Info("content id episode matched",
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
	if env.Attributes == nil {
		env.Attributes = make(map[string]any)
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
	env.Attributes["content_id_matches"] = payload
	env.Attributes["content_id_method"] = "whisperx_opensubtitles"
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
	episodes = uniqueInts(episodes)
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
		m.logger.Warn("failed to encode metadata after content id", logging.Error(err))
		return
	}
	item.MetadataJSON = string(data)
}

func deriveCandidateEpisodes(env *ripspec.Envelope, season *tmdb.SeasonDetails, discNumber int) []int {
	set := make(map[int]struct{}, len(env.Episodes)*2)
	for _, episode := range env.Episodes {
		if episode.Episode > 0 {
			set[episode.Episode] = struct{}{}
		}
	}
	totalEpisodes := len(season.Episodes)
	if discNumber > 0 && totalEpisodes > 0 {
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
			set[season.Episodes[idx].EpisodeNumber] = struct{}{}
		}
	}
	if len(set) == 0 {
		for _, episode := range season.Episodes {
			set[episode.EpisodeNumber] = struct{}{}
		}
	}
	list := make([]int, 0, len(set))
	for number := range set {
		list = append(list, number)
	}
	sort.Ints(list)
	return list
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

func buildEpisodeBasename(show string, season, episode int) string {
	meta := queue.NewTVMetadata(show, season, []int{episode}, fmt.Sprintf("%s Season %02d", show, season))
	name := meta.GetFilename()
	if strings.TrimSpace(name) == "" {
		return fmt.Sprintf("%s - S%02dE%02d", strings.TrimSpace(show), season, episode)
	}
	return name
}

func uniqueInts(numbers []int) []int {
	if len(numbers) == 0 {
		return nil
	}
	result := []int{numbers[0]}
	for _, value := range numbers[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
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
