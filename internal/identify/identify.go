package identify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/discidcache"
	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/keydb"
	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/makemkv"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/services"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/staging"
	"github.com/five82/spindle/internal/tmdb"
)

// editionPatterns matches common edition keywords in disc titles.
// Uses [\s_]+ to handle both space and underscore separators common in disc labels.
var editionPatterns = regexp.MustCompile(
	`(?i)(extended[\s_]+(edition|cut)|director'?s[\s_]+(cut|edition)|unrated|theatrical|special[\s_]+edition|criterion|imax)`,
)

// discMetadataPattern strips season, disc, volume, and part indicators from disc labels.
// Examples: "- Season 2", ": Disc 6", "Volume 3", "Part 1", "TV Series".
var discMetadataPattern = regexp.MustCompile(
	`(?i)(\s*[-:]\s*)?(season\s+\d+|disc\s+\d+|volume\s+\d+|part\s+\d+|tv\s+series)`,
)

// trailingPunctPattern cleans up trailing punctuation/whitespace left after stripping.
var trailingPunctPattern = regexp.MustCompile(`[\s:_-]+$`)

// seasonPattern extracts a season number from disc titles (e.g., "S01", "Season 1", "SEASON_1").
var seasonPattern = regexp.MustCompile(`(?i)(?:s|season[\s_]*)(\d+)`)

// discNumberPattern extracts a disc/volume/part number from disc titles (e.g., "Disc 1", "Volume 3", "Part 1").
var discNumberPattern = regexp.MustCompile(`(?i)(?:disc|volume|part)[\s_]*(\d+)`)

// editionLLMConfidenceThreshold is the minimum confidence for LLM edition detection.
const editionLLMConfidenceThreshold = 0.8

// editionLLMSystemPrompt is the system prompt for LLM edition classification.
const editionLLMSystemPrompt = `You determine if a disc is an alternate movie edition (not the standard theatrical release).

Alternate editions include:
- Director's Cut / Director's Edition
- Extended Edition / Extended Cut
- Unrated / Uncut versions
- Special Editions
- Remastered versions
- Anniversary Editions
- Theatrical vs different cuts
- Color versions of originally B&W films
- Black and white versions (like "Noir" editions)
- IMAX editions

NOT alternate editions:
- Standard theatrical releases
- Different regional releases of the same version
- 4K/UHD remasters (unless labeled as a different cut)
- Bonus disc content
- Just year differences in release date

Respond ONLY with JSON: {"is_edition": true/false, "confidence": 0.0-1.0, "reason": "brief explanation"}`

// editionLLMResponse is the JSON response from LLM edition classification.
type editionLLMResponse struct {
	IsEdition  bool    `json:"is_edition"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// Handler implements stage.Handler for disc identification.
type Handler struct {
	cfg         *config.Config
	store       *queue.Store
	tmdbClient  *tmdb.Client
	llmClient   *llm.Client
	notifier    *notify.Notifier
	discIDCache *discidcache.Store
	keydbCat    *keydb.Catalog
}

// New creates an identification handler.
func New(
	cfg *config.Config,
	store *queue.Store,
	tmdbClient *tmdb.Client,
	llmClient *llm.Client,
	notifier *notify.Notifier,
	discIDCache *discidcache.Store,
	keydbCat *keydb.Catalog,
) *Handler {
	return &Handler{
		cfg:         cfg,
		store:       store,
		tmdbClient:  tmdbClient,
		llmClient:   llmClient,
		notifier:    notifier,
		discIDCache: discIDCache,
		keydbCat:    keydbCat,
	}
}

// Run executes the identification stage.
func (h *Handler) Run(ctx context.Context, item *queue.Item) error {
	logger := stage.LoggerFromContext(ctx)
	logger.Info("identification stage started",
		"event_type", "stage_start",
		"disc_title", item.DiscTitle,
	)

	// Clean stale staging directories (older than 48 hours).
	cleanResult := staging.CleanStale(ctx, h.cfg.Paths.StagingDir, 48*time.Hour, nil, logger)
	if cleanResult.Removed > 0 {
		logger.Info("cleaned stale staging directories", "removed", cleanResult.Removed)
	}

	// Step 1: Check disc ID cache for fast path.
	if h.discIDCache != nil && item.DiscFingerprint != "" {
		if entry := h.discIDCache.Lookup(item.DiscFingerprint); entry != nil {
			logger.Info("disc ID cache hit",
				"decision_type", "disc_id_cache",
				"decision_result", "hit",
				"decision_reason", fmt.Sprintf("cached TMDB ID %d", entry.TMDBID),
			)
			// Build envelope from cached data and return.
			env := h.buildEnvelopeFromCache(item, entry)
			if err := h.persistEnvelope(ctx, item, &env); err != nil {
				return err
			}
			return nil
		}
	}

	// Step 2: Probe disc source type.
	discSource := "unknown"
	if ev, err := discmonitor.ProbeDisc(ctx, h.cfg.MakeMKV.OpticalDrive); err != nil {
		logger.Warn("disc probe failed, defaulting to unknown",
			"event_type", "disc_probe_error",
			"error_hint", err.Error(),
			"impact", "disc_source will be unknown",
		)
	} else {
		discSource = mapDiscSource(ev.DiscType)
	}

	// Step 2b: BDInfo (Blu-ray discs only, non-fatal).
	var bdInfo *BDInfoResult
	if discSource == "bluray" {
		var bdErr error
		bdInfo, bdErr = RunBDInfo(ctx, h.cfg.MakeMKV.OpticalDrive)
		if bdErr != nil {
			logger.Warn("bd_info failed",
				"event_type", "bdinfo_error",
				"error_hint", bdErr.Error(),
				"impact", "bd_info metadata unavailable",
			)
		} else if bdInfo != nil {
			logger.Info("bd_info results",
				"decision_type", "bdinfo_scan",
				"decision_result", "completed",
				"decision_reason", fmt.Sprintf("disc_id=%s studio=%s year=%s", bdInfo.DiscID, bdInfo.Studio, bdInfo.Year),
				"disc_name", bdInfo.DiscName,
				"volume_id", bdInfo.VolumeIdentifier,
			)
		}

		// Apply disc_settle_delay between bd_info and MakeMKV scan.
		if h.cfg.MakeMKV.DiscSettleDelay > 0 {
			time.Sleep(time.Duration(h.cfg.MakeMKV.DiscSettleDelay) * time.Second)
		}
	}

	// Step 3: MakeMKV scan.
	discInfo, err := makemkv.Scan(ctx, h.cfg.MakeMKV.OpticalDrive,
		time.Duration(h.cfg.MakeMKV.InfoTimeout)*time.Second,
		h.cfg.MakeMKV.MinTitleLength)
	if err != nil {
		return fmt.Errorf("makemkv scan: %w", err)
	}

	// Step 4: Build title priority chain for TMDB query.
	rawTitle := h.resolveTitle(item, discInfo, bdInfo)
	queryTitle := CleanQueryTitle(rawTitle)
	logger.Info("title resolved for TMDB search",
		"decision_type", "title_source",
		"decision_result", "resolved",
		"decision_reason", queryTitle,
		"raw_title", rawTitle,
	)

	// Step 5: TMDB search.
	// Use BDInfo year as a hint for better TMDB matching.
	searchYear := ""
	if bdInfo != nil && bdInfo.Year != "" {
		searchYear = bdInfo.Year
	}
	results, err := h.tmdbClient.SearchMulti(ctx, queryTitle)
	if err != nil {
		return fmt.Errorf("tmdb search: %w", err)
	}

	best, confidence := tmdb.SelectBestResult(results, queryTitle, searchYear, 5)
	if best == nil {
		logger.Warn("no TMDB results",
			"event_type", "tmdb_no_results",
			"error_hint", "disc title may not match any TMDB entry",
			"impact", "item flagged for review",
		)
		item.AppendReviewReason("TMDB: no results found")
		// Build minimal envelope and continue.
		env := h.buildFallbackEnvelope(item, discInfo)
		if err := h.persistEnvelope(ctx, item, &env); err != nil {
			return err
		}
		return &services.ErrDegraded{
			Msg: "no TMDB results found for: " + queryTitle,
		}
	}

	logger.Info("TMDB match found",
		"decision_type", "tmdb_match",
		"decision_result", best.DisplayTitle(),
		"decision_reason", fmt.Sprintf("confidence=%.2f", confidence),
	)

	// Update disc_title to canonical name per spec.
	mediaType := best.MediaType
	if mediaType == "" {
		mediaType = "movie" // default for single-type searches
	}
	item.DiscTitle = canonicalTitle(*best, mediaType, item.DiscTitle, discInfo)

	// Step 6: Detect edition (movies only, via regex + optional LLM).
	var edition string
	if mediaType == "movie" {
		edition = h.detectEdition(ctx, logger, item.DiscTitle, discInfo.Name)
	}

	// Step 7: Build and persist RipSpec envelope.
	env := h.buildEnvelope(item, discInfo, best, mediaType, edition, confidence, discSource)
	if err := h.persistEnvelope(ctx, item, &env); err != nil {
		return err
	}

	// Step 8: Cache disc ID.
	if h.discIDCache != nil && item.DiscFingerprint != "" {
		entry := discidcache.Entry{
			TMDBID:    best.ID,
			MediaType: mediaType,
			Title:     best.DisplayTitle(),
			Year:      best.Year(),
		}
		if err := h.discIDCache.Set(item.DiscFingerprint, entry); err != nil {
			logger.Warn("disc ID cache write failed",
				"event_type", "cache_write_error",
				"error_hint", err.Error(),
				"impact", "cache miss on next insert",
			)
		}
	}

	// Step 9: Send notification.
	if h.notifier != nil {
		_ = h.notifier.Send(ctx, notify.EventIdentificationComplete,
			"Identification Complete",
			item.DiscTitle,
		)
	}

	logger.Info("identification stage completed",
		"event_type", "stage_complete",
	)
	return nil
}

// resolveTitle implements the title priority chain: KeyDB -> BDInfo disc name
// -> queue item label -> MakeMKV disc name -> lsblk label (item.DiscTitle) ->
// "Unknown Disc". Returns the first non-empty value.
func (h *Handler) resolveTitle(item *queue.Item, discInfo *makemkv.DiscInfo, bdInfo *BDInfoResult) string {
	// Priority 1: KeyDB lookup by fingerprint.
	if h.keydbCat != nil && item.DiscFingerprint != "" {
		if title := h.keydbCat.Lookup(item.DiscFingerprint); title != "" {
			return title
		}
	}

	// Priority 2: BDInfo disc name.
	if bdInfo != nil && bdInfo.DiscName != "" {
		return bdInfo.DiscName
	}

	// Priority 3: MakeMKV disc name.
	if discInfo != nil && discInfo.Name != "" {
		return discInfo.Name
	}

	// Priority 4: disc label from queue item (lsblk label).
	if item.DiscTitle != "" {
		return item.DiscTitle
	}

	return "Unknown Disc"
}

// canonicalTitle builds the canonical disc title from a TMDB match.
// Movie: "Title (Year)", TV: "Show Season XX (Year)".
// Falls back to just the display title if year is unavailable.
func canonicalTitle(best tmdb.SearchResult, mediaType string, discTitle string, discInfo *makemkv.DiscInfo) string {
	title := best.DisplayTitle()
	year := best.Year()

	if mediaType == "tv" {
		var discName string
		if discInfo != nil {
			discName = discInfo.Name
		}
		if season := extractSeasonNumber(discTitle, discName); season > 0 {
			title = fmt.Sprintf("%s Season %02d", title, season)
		}
	}

	if year != "" {
		return fmt.Sprintf("%s (%s)", title, year)
	}
	return title
}

// CleanQueryTitle strips disc metadata (season, disc, volume, "TV Series") from a
// resolved title to produce a cleaner TMDB search query.
// Example: "Batman TV Series - Season 2: Disc 6" → "Batman"
func CleanQueryTitle(title string) string {
	cleaned := discMetadataPattern.ReplaceAllString(title, "")
	cleaned = trailingPunctPattern.ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return title // don't return empty; fall back to original
	}
	return cleaned
}

// extractFirstIntMatch returns the first integer captured by pattern across
// the provided sources, or 0 if none match.
func extractFirstIntMatch(pattern *regexp.Regexp, sources ...string) int {
	for _, s := range sources {
		if m := pattern.FindStringSubmatch(s); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
		}
	}
	return 0
}

// extractSeasonNumber returns the first season number found in any of the
// provided sources, or 0 if none match.
func extractSeasonNumber(sources ...string) int {
	return extractFirstIntMatch(seasonPattern, sources...)
}

// extractDiscNumber returns the first disc/volume/part number found in any of
// the provided sources, or 0 if none match.
func extractDiscNumber(sources ...string) int {
	return extractFirstIntMatch(discNumberPattern, sources...)
}

// mapDiscSource converts a discmonitor disc type string to a ripspec disc_source value.
func mapDiscSource(discType string) string {
	switch discType {
	case "Blu-ray":
		return "bluray"
	case "DVD":
		return "dvd"
	default:
		return "unknown"
	}
}

// detectEdition checks for edition markers in disc title and disc name.
// Tries regex first; if no match and LLM is available, tries LLM classification.
// Returns the detected edition label, or empty string if none detected.
func (h *Handler) detectEdition(ctx context.Context, logger *slog.Logger, discTitle, discName string) string {
	// Try regex on both disc title and disc name.
	combined := discTitle + " " + discName
	if match := editionPatterns.FindString(combined); match != "" {
		logger.Info("edition detected via regex",
			"decision_type", "edition_detection",
			"decision_result", match,
			"decision_reason", "regex match",
		)
		return match
	}

	// If LLM is available and there is extra content to analyze, try LLM.
	if h.llmClient == nil || discTitle == "" {
		return ""
	}

	userPrompt := fmt.Sprintf("Disc: %s\nTMDB: %s", strings.TrimSpace(discTitle), strings.TrimSpace(discName))
	var resp editionLLMResponse
	if err := h.llmClient.CompleteJSON(ctx, editionLLMSystemPrompt, userPrompt, &resp); err != nil {
		logger.Warn("edition LLM classification failed",
			"event_type", "edition_llm_error",
			"error_hint", err.Error(),
			"impact", "falling back to regex-only",
		)
		return ""
	}

	if resp.IsEdition && resp.Confidence >= editionLLMConfidenceThreshold {
		logger.Info("edition detected via LLM",
			"decision_type", "edition_detection",
			"decision_result", resp.Reason,
			"decision_reason", fmt.Sprintf("confidence=%.2f", resp.Confidence),
		)
		return resp.Reason
	}

	return ""
}

// discInfoName returns the disc name from a DiscInfo, or "" if nil.
func discInfoName(discInfo *makemkv.DiscInfo) string {
	if discInfo != nil {
		return discInfo.Name
	}
	return ""
}

// convertTitles converts MakeMKV title info to ripspec titles.
func convertTitles(discInfo *makemkv.DiscInfo) []ripspec.Title {
	if discInfo == nil {
		return nil
	}
	titles := make([]ripspec.Title, 0, len(discInfo.Titles))
	for _, t := range discInfo.Titles {
		titles = append(titles, ripspec.Title{
			ID:           t.ID,
			Name:         t.Name,
			Duration:     int(t.Duration.Seconds()),
			Chapters:     t.Chapters,
			Playlist:     t.Playlist,
			SegmentCount: t.SegmentCount,
			SegmentMap:   t.SegmentMap,
		})
	}
	return titles
}

// buildEnvelope constructs a full RipSpec envelope from scan and TMDB data.
func (h *Handler) buildEnvelope(
	item *queue.Item,
	discInfo *makemkv.DiscInfo,
	best *tmdb.SearchResult,
	mediaType, edition string,
	confidence float64,
	discSource string,
) ripspec.Envelope {
	// Extract season and disc numbers from disc title / MakeMKV disc name.
	discName := discInfoName(discInfo)
	seasonNum := extractSeasonNumber(item.DiscTitle, discName)
	discNum := extractDiscNumber(item.DiscTitle, discName)

	env := ripspec.Envelope{
		Version:     ripspec.CurrentVersion,
		Fingerprint: item.DiscFingerprint,
		Metadata: ripspec.Metadata{
			ID:           best.ID,
			Title:        best.DisplayTitle(),
			Overview:     best.Overview,
			MediaType:    mediaType,
			Year:         best.Year(),
			ReleaseDate:  best.ReleaseDate,
			VoteAverage:  best.VoteAverage,
			VoteCount:    best.VoteCount,
			Movie:        mediaType == "movie",
			Edition:      edition,
			SeasonNumber: seasonNum,
			DiscNumber:   discNum,
			DiscSource:   discSource,
		},
	}

	if best.FirstAirDate != "" {
		env.Metadata.FirstAirDate = best.FirstAirDate
	}
	if mediaType == "tv" {
		env.Metadata.ShowTitle = best.DisplayTitle()
	}

	// Add titles from MakeMKV scan.
	env.Titles = convertTitles(discInfo)

	// For TV content, create episode placeholders from eligible titles.
	if mediaType == "tv" {
		h.createEpisodePlaceholders(&env)
	}

	return env
}

// createEpisodePlaceholders adds episode entries for each title that meets the
// minimum title length threshold. Each episode gets a placeholder key (e.g.,
// "s01_001") and is linked to the title's ID for downstream ripping.
func (h *Handler) createEpisodePlaceholders(env *ripspec.Envelope) {
	season := env.Metadata.SeasonNumber
	if season <= 0 {
		season = 1
	}

	idx := 0
	for _, title := range env.Titles {
		if title.Duration < h.cfg.MakeMKV.MinTitleLength {
			continue
		}
		idx++
		env.Episodes = append(env.Episodes, ripspec.Episode{
			Key:            ripspec.PlaceholderKey(season, idx),
			TitleID:        title.ID,
			Season:         season,
			RuntimeSeconds: title.Duration,
		})
	}
}

// buildEnvelopeFromCache constructs a minimal envelope from a disc ID cache entry.
func (h *Handler) buildEnvelopeFromCache(item *queue.Item, entry *discidcache.Entry) ripspec.Envelope {
	return ripspec.Envelope{
		Version:     ripspec.CurrentVersion,
		Fingerprint: item.DiscFingerprint,
		Metadata: ripspec.Metadata{
			ID:        entry.TMDBID,
			Title:     entry.Title,
			MediaType: entry.MediaType,
			Year:      entry.Year,
			Movie:     entry.MediaType == "movie",
			Cached:    true,
		},
	}
}

// buildFallbackEnvelope constructs an envelope with unknown media type for review.
func (h *Handler) buildFallbackEnvelope(item *queue.Item, discInfo *makemkv.DiscInfo) ripspec.Envelope {
	title := item.DiscTitle
	if title == "" && discInfo != nil {
		title = discInfo.Name
	}
	if title == "" {
		title = "Unknown Disc"
	}

	// Extract season/disc numbers even for fallback — they indicate TV content.
	discName := discInfoName(discInfo)
	seasonNum := extractSeasonNumber(item.DiscTitle, discName)
	discNum := extractDiscNumber(item.DiscTitle, discName)

	env := ripspec.Envelope{
		Version:     ripspec.CurrentVersion,
		Fingerprint: item.DiscFingerprint,
		Metadata: ripspec.Metadata{
			Title:        title,
			MediaType:    "unknown",
			SeasonNumber: seasonNum,
			DiscNumber:   discNum,
		},
	}

	env.Titles = convertTitles(discInfo)

	// If season number was extracted, this is likely TV — create episode placeholders.
	if seasonNum > 0 {
		h.createEpisodePlaceholders(&env)
	}

	return env
}

// persistEnvelope updates the item's metadata_json and persists the RipSpec.
func (h *Handler) persistEnvelope(ctx context.Context, item *queue.Item, env *ripspec.Envelope) error {
	// Update metadata_json on the item.
	meta := queue.Metadata{
		ID:           env.Metadata.ID,
		Title:        env.Metadata.Title,
		MediaType:    env.Metadata.MediaType,
		ShowTitle:    env.Metadata.ShowTitle,
		Year:         env.Metadata.Year,
		SeasonNumber: env.Metadata.SeasonNumber,
		Movie:        env.Metadata.Movie,
		Edition:      env.Metadata.Edition,
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	item.MetadataJSON = string(metaJSON)

	// Persist RipSpec via queue helper.
	return queue.PersistRipSpec(ctx, h.store, item, env)
}
