package identify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/discidcache"
	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/keydb"
	"github.com/five82/spindle/internal/logs"

	"github.com/five82/spindle/internal/makemkv"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/services"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/staging"
	"github.com/five82/spindle/internal/tmdb"
)

// discMetadataPattern strips season, disc, volume, and part indicators from disc labels.
// Examples: "- Season 2", ": Disc 6", "Volume 3", "Part 1", "TV Series".
var discMetadataPattern = regexp.MustCompile(
	`(?i)(\s*[-:]\s*)?(season\s+\d+|disc\s+\d+|volume\s+\d+|part\s+\d+|tv\s+series)`,
)

// formatBrandingPattern strips physical media format descriptors from disc titles.
// BDInfo often includes format branding that pollutes TMDB search queries.
// Examples: "Ultra HD Blu-ray™", "Blu-ray", "4K Ultra HD", "UHD", "DVD".
var formatBrandingPattern = regexp.MustCompile(
	`(?i)(\s*[-\x{2013}:]\s*)?(?:(?:4K\s+)?Ultra\s+HD(?:\s+Blu[- ]?ray)?|Blu[- ]?ray|\bUHD\b|\bDVD\b|\bBD\b)[\x{2122}\x{00AE}]*`,
)

// trailingPunctPattern cleans up trailing punctuation/whitespace left after stripping.
var trailingPunctPattern = regexp.MustCompile(`[\s:_-]+$`)

// trailingYearPattern matches a trailing 4-digit year in parentheses or standalone.
// Matches "(2005)" or bare "2005" at word boundary, but not unmatched parens like "2005)".
var trailingYearPattern = regexp.MustCompile(`(?i)(?:\s*\((\d{4})\)|\s+(\d{4}))\s*$`)

// seasonPattern extracts a season number from disc titles (e.g., "S01", "Season 1", "SEASON_1").
var seasonPattern = regexp.MustCompile(`(?i)(?:s|season[\s_]*)(\d+)`)

// discNumberPattern extracts a disc/volume/part number from disc titles (e.g., "Disc 1", "Volume 3", "Part 1").
var discNumberPattern = regexp.MustCompile(`(?i)(?:disc|volume|part)[\s_]*(\d+)`)

// tvHintPattern detects TV content indicators in disc titles.
// Matches "TV Series", "Season N", or "S01"/"S1" preceded by a non-letter (or start of string).
var tvHintPattern = regexp.MustCompile(`(?i)(tv\s+series|season[\s_]*\d+|(?:^|[^a-zA-Z])s\d{1,2}(?:[^a-zA-Z0-9]|$))`)

// Handler implements stage.Handler for disc identification.
type Handler struct {
	cfg         *config.Config
	store       *queue.Store
	tmdbClient  *tmdb.Client
	notifier    *notify.Notifier
	discIDCache *discidcache.Store
	keydbCat    *keydb.Catalog
}

// New creates an identification handler.
func New(
	cfg *config.Config,
	store *queue.Store,
	tmdbClient *tmdb.Client,
	notifier *notify.Notifier,
	discIDCache *discidcache.Store,
	keydbCat *keydb.Catalog,
) *Handler {
	return &Handler{
		cfg:         cfg,
		store:       store,
		tmdbClient:  tmdbClient,
		notifier:    notifier,
		discIDCache: discIDCache,
		keydbCat:    keydbCat,
	}
}

// IdentifyResult holds the results of disc identification without persistence.
// Used by both the daemon (via Run) and the CLI identify command.
type IdentifyResult struct {
	RawTitle    string
	QueryTitle  string
	TitleSource string
	SearchYear  int
	YearSource  string
	DiscSource  string
	MediaType   string
	Best        *tmdb.SearchResult
	AllResults  []tmdb.SearchResult
	DiscInfo    *makemkv.DiscInfo
	BDInfo      *BDInfoResult
	Envelope    ripspec.Envelope
	Degraded    bool
	DegradedMsg string
}

// Identify runs the full identification pipeline and returns results
// without persisting to the queue or sending notifications.
// Used by both the daemon (via Run) and the CLI identify command.
func (h *Handler) Identify(ctx context.Context, item *queue.Item, logger *slog.Logger) (*IdentifyResult, error) {
	result := &IdentifyResult{}

	// Step 1: Probe disc source type (lightweight lsblk, always needed).
	result.DiscSource = "unknown"
	if ev, err := discmonitor.ProbeDisc(ctx, h.cfg.MakeMKV.OpticalDrive); err != nil {
		logger.Warn("disc probe failed, defaulting to unknown",
			"event_type", "disc_probe_error",
			"error_hint", err.Error(),
			"impact", "disc_source will be unknown",
		)
	} else {
		result.DiscSource = mapDiscSource(ev.DiscType)
		logger.Info("disc source determined",
			"decision_type", logs.DecisionBDInfoAvailability,
			"decision_result", result.DiscSource,
			"decision_reason", fmt.Sprintf("disc_type=%s", ev.DiscType),
		)
	}

	// Step 2: BDInfo (Blu-ray discs only, non-fatal).
	if result.DiscSource == "bluray" {
		var bdErr error
		result.BDInfo, bdErr = RunBDInfo(ctx, h.cfg.MakeMKV.OpticalDrive, logger)
		if bdErr != nil {
			logger.Warn("bd_info failed",
				"event_type", "bdinfo_error",
				"error_hint", bdErr.Error(),
				"impact", "bd_info metadata unavailable",
			)
		} else if result.BDInfo != nil {
			logger.Info("bd_info results",
				"decision_type", logs.DecisionBDInfoScan,
				"decision_result", "completed",
				"decision_reason", fmt.Sprintf("disc_id=%s studio=%s year=%s", result.BDInfo.DiscID, result.BDInfo.Studio, result.BDInfo.Year),
				"disc_name", result.BDInfo.DiscName,
				"volume_id", result.BDInfo.VolumeIdentifier,
			)
		}

		// Apply disc_settle_delay between bd_info and MakeMKV scan.
		if h.cfg.MakeMKV.DiscSettleDelay > 0 {
			time.Sleep(time.Duration(h.cfg.MakeMKV.DiscSettleDelay) * time.Second)
		}
	}

	// Step 3: MakeMKV scan (always runs -- titles are needed for ripping).
	var err error
	result.DiscInfo, err = makemkv.Scan(ctx, h.cfg.MakeMKV.OpticalDrive,
		time.Duration(h.cfg.MakeMKV.InfoTimeout)*time.Second,
		h.cfg.MakeMKV.MinTitleLength, logger)
	if err != nil {
		return nil, fmt.Errorf("makemkv scan: %w", err)
	}

	// Step 4: Resolve title (needed before cache check for validation).
	result.RawTitle, result.TitleSource = h.resolveTitle(item, result.DiscInfo, result.BDInfo)
	result.QueryTitle = CleanQueryTitle(result.RawTitle)
	logger.Info("title resolved for TMDB search",
		"decision_type", logs.DecisionTitleResolution,
		"decision_result", result.TitleSource,
		"decision_reason", result.QueryTitle,
		"raw_title", result.RawTitle,
	)

	// Detect media type hint once; used for both cache validation and TMDB search routing.
	mediaHint := detectMediaTypeHint(result.RawTitle)

	// Step 5: Check disc ID cache (skips TMDB search and KeyDB lookup, not the scan).
	// Validate cached media type against fresh disc metadata to prevent stale entries
	// from overriding unambiguous disc signals (e.g., TV hint vs cached movie).
	if h.discIDCache != nil && item.DiscFingerprint != "" {
		if entry := h.discIDCache.Lookup(item.DiscFingerprint); entry != nil {
			if mediaHint == "tv" && entry.MediaType == "movie" {
				logger.Warn("disc ID cache invalidated: TV hint contradicts cached movie type",
					"decision_type", logs.DecisionDiscIDCache,
					"decision_result", "invalidated",
					"decision_reason", fmt.Sprintf("raw_title=%q has TV hint but cache says movie", result.RawTitle),
				)
				_ = h.discIDCache.Remove(item.DiscFingerprint)
				// Fall through to full identification.
			} else {
				canonTitle := entry.Title
				if entry.Year != "" {
					canonTitle = fmt.Sprintf("%s (%s)", canonTitle, entry.Year)
				}
				item.DiscTitle = canonTitle
				logger.Info("disc title updated from cache",
					"decision_type", logs.DecisionTitleSource,
					"decision_result", "updated",
					"decision_reason", "disc_id_cache_entry",
				)
				result.Envelope = h.buildEnvelopeFromCache(logger, item, entry, result.DiscInfo, result.DiscSource)
				setForcedSubtitleAttribute(logger, result.DiscInfo, &result.Envelope)
				return result, nil
			}
		}
	}

	// Step 6: Extract year and clean title for TMDB search.
	// Year priority: BDInfo > resolved title > item disc title.
	if result.BDInfo != nil && result.BDInfo.Year != "" {
		if y, err := strconv.Atoi(result.BDInfo.Year); err == nil {
			result.SearchYear = y
			result.YearSource = "bdinfo"
		}
	}
	if result.SearchYear == 0 {
		if cleaned, y := splitTitleYear(result.QueryTitle); y > 0 {
			result.SearchYear = y
			result.QueryTitle = cleaned
			result.YearSource = "resolved_title"
		}
	}
	if result.SearchYear == 0 {
		if cleaned, y := splitTitleYear(item.DiscTitle); y > 0 {
			result.SearchYear = y
			result.YearSource = "disc_title"
			// Only use the cleaned title if queryTitle still contains the year.
			if result.QueryTitle == item.DiscTitle || result.QueryTitle == CleanQueryTitle(item.DiscTitle) {
				result.QueryTitle = cleaned
			}
		}
	}
	if result.YearSource != "" {
		logger.Info("year source decision",
			"decision_type", logs.DecisionYearSource,
			"decision_result", result.YearSource,
			"decision_reason", fmt.Sprintf("year=%d", result.SearchYear),
		)
	}

	switch mediaHint {
	case "tv":
		logger.Info("media type hint detected",
			"decision_type", logs.DecisionTMDBSearch,
			"decision_result", "tv",
			"decision_reason", fmt.Sprintf("raw_title=%q", result.RawTitle),
		)
		yearStr := ""
		if result.SearchYear > 0 {
			yearStr = strconv.Itoa(result.SearchYear)
		}
		result.AllResults, err = h.tmdbClient.SearchTV(ctx, result.QueryTitle, yearStr)
		if err != nil {
			return nil, fmt.Errorf("tmdb search (tv): %w", err)
		}
		result.Best = tmdb.SelectBestResult(result.AllResults, result.QueryTitle, result.SearchYear, 5, logger)
		if result.Best == nil {
			logger.Info("TV-hinted search found no match, falling back to multi",
				"decision_type", logs.DecisionTMDBSearch,
				"decision_result", "fallback_multi",
				"decision_reason", "no tv match above threshold",
			)
			result.AllResults, err = h.tmdbClient.SearchMulti(ctx, result.QueryTitle)
			if err != nil {
				return nil, fmt.Errorf("tmdb search (multi fallback): %w", err)
			}
			result.Best = tmdb.SelectBestResult(result.AllResults, result.QueryTitle, result.SearchYear, 5, logger)
		}
	default:
		result.AllResults, err = h.tmdbClient.SearchMulti(ctx, result.QueryTitle)
		if err != nil {
			return nil, fmt.Errorf("tmdb search: %w", err)
		}
		result.Best = tmdb.SelectBestResult(result.AllResults, result.QueryTitle, result.SearchYear, 5, logger)
	}
	if result.Best == nil {
		logger.Warn("no TMDB match",
			"event_type", "tmdb_no_match",
			"error_hint", "no result met confidence threshold",
			"impact", "item flagged for review",
		)
		item.AppendReviewReason("TMDB: no confident match found")
		result.Envelope = h.buildFallbackEnvelope(logger, item, result.DiscInfo)
		setForcedSubtitleAttribute(logger, result.DiscInfo, &result.Envelope)
		result.Degraded = true
		result.DegradedMsg = "no TMDB match found for: " + result.QueryTitle
		return result, nil
	}

	logger.Info("TMDB match found",
		"decision_type", logs.DecisionTMDBMatch,
		"decision_result", result.Best.DisplayTitle(),
		"decision_reason", fmt.Sprintf("tmdb_id=%d year=%s votes=%d", result.Best.ID, result.Best.Year(), result.Best.VoteCount),
	)

	// Update disc_title to canonical name per spec.
	result.MediaType = result.Best.MediaType
	if result.MediaType == "" {
		result.MediaType = "movie" // default for single-type searches
		logger.Info("media type defaulted to movie",
			"decision_type", logs.DecisionTMDBMatch,
			"decision_result", "movie",
			"decision_reason", "empty media type from search result",
		)
	}
	item.DiscTitle = canonicalTitle(*result.Best, result.MediaType, item.DiscTitle, result.DiscInfo)

	// Step 6: Build RipSpec envelope.
	result.Envelope = h.buildEnvelope(logger, item, result.DiscInfo, result.Best, result.MediaType, result.DiscSource)
	setForcedSubtitleAttribute(logger, result.DiscInfo, &result.Envelope)

	return result, nil
}

// Run executes the identification stage.
func (h *Handler) Run(ctx context.Context, item *queue.Item) error {
	logger := stage.LoggerFromContext(ctx)
	logger.Info("identification stage started",
		"event_type", "stage_start",
		"stage", "identification",
		"disc_title", item.DiscTitle,
	)

	h.updateProgress(item, 5, "Phase 1/3 - Cleaning stale staging")

	// Clean stale staging directories (older than 48 hours).
	cleanResult := staging.CleanStale(ctx, h.cfg.Paths.StagingDir, 48*time.Hour, nil, logger)
	if cleanResult.Removed > 0 {
		logger.Info("cleaned stale staging directories", "removed", cleanResult.Removed)
	}

	h.updateProgress(item, 20, "Phase 2/3 - Scanning disc and resolving metadata")

	result, err := h.Identify(ctx, item, logger)
	if err != nil {
		return err
	}

	// Persist envelope.
	h.updateProgress(item, 85, "Phase 3/3 - Finalizing identification")
	if err := h.persistEnvelope(ctx, item, &result.Envelope); err != nil {
		return err
	}

	if result.Degraded {
		return &services.ErrDegraded{Msg: result.DegradedMsg}
	}

	// Cache disc ID.
	if result.Best != nil && h.discIDCache != nil && item.DiscFingerprint != "" {
		entry := discidcache.Entry{
			TMDBID:                 result.Best.ID,
			MediaType:              result.MediaType,
			Title:                  result.Best.DisplayTitle(),
			Year:                   result.Best.Year(),
			HasForcedSubtitleTrack: result.Envelope.Attributes.HasForcedSubtitleTrack,
		}
		if err := h.discIDCache.Set(item.DiscFingerprint, entry); err != nil {
			logger.Warn("disc ID cache write failed",
				"event_type", "cache_write_error",
				"error_hint", err.Error(),
				"impact", "cache miss on next insert",
			)
		}
	}

	// Send notification.
	if h.notifier != nil {
		msg := item.DiscTitle + queue.FormatAlsoProcessing(h.store, item.ID)
		_ = h.notifier.Send(ctx, notify.EventIdentificationComplete,
			"Identification Complete",
			msg,
		)
	}

	logger.Info("identification stage completed",
		"event_type", "stage_complete",
		"stage", "identification",
	)
	return nil
}

func (h *Handler) updateProgress(item *queue.Item, percent float64, message string) {
	item.ProgressPercent = percent
	item.ProgressMessage = message
	if h.store != nil {
		_ = h.store.UpdateProgress(item)
	}
}

// resolveTitle implements the title priority chain and returns both the
// resolved title and the source that was used for observability.
func (h *Handler) resolveTitle(item *queue.Item, discInfo *makemkv.DiscInfo, bdInfo *BDInfoResult) (string, string) {
	if h.keydbCat != nil && item.DiscFingerprint != "" {
		if title := h.keydbCat.Lookup(item.DiscFingerprint); title != "" {
			return title, "keydb"
		}
	}
	if bdInfo != nil && bdInfo.DiscName != "" {
		return bdInfo.DiscName, "bdinfo"
	}
	if discInfo != nil && discInfo.Name != "" {
		return discInfo.Name, "makemkv"
	}
	if item.DiscTitle != "" {
		return item.DiscTitle, "disc_label"
	}
	return "Unknown Disc", "fallback"
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

// splitTitleYear extracts a trailing year (1880-2100) from a title string.
// Returns the cleaned title and year, or the original title and 0 if no year found.
// Examples: "Munich (2005)" → ("Munich", 2005), "Munich" → ("Munich", 0).
func splitTitleYear(value string) (string, int) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", 0
	}
	matches := trailingYearPattern.FindStringSubmatch(trimmed)
	if matches == nil {
		return trimmed, 0
	}
	// Two capture groups: matches[1] is "(YEAR)", matches[2] is bare "YEAR".
	yearStr := matches[1]
	if yearStr == "" {
		yearStr = matches[2]
	}
	if yearStr == "" {
		return trimmed, 0
	}
	year, err := strconv.Atoi(yearStr)
	if err != nil || year < 1880 || year > 2100 {
		return trimmed, 0
	}
	cleaned := strings.TrimSpace(trailingYearPattern.ReplaceAllString(trimmed, ""))
	if cleaned == "" {
		return trimmed, 0
	}
	return cleaned, year
}

// CleanQueryTitle strips disc metadata (season, disc, volume, "TV Series") from a
// resolved title to produce a cleaner TMDB search query.
// Example: "Batman TV Series - Season 2: Disc 6" → "Batman"
func CleanQueryTitle(title string) string {
	cleaned := discMetadataPattern.ReplaceAllString(title, "")
	cleaned = formatBrandingPattern.ReplaceAllString(cleaned, "")
	cleaned = trailingPunctPattern.ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return title // don't return empty; fall back to original
	}
	return cleaned
}

// detectMediaTypeHint examines the raw disc title for TV indicators.
// Returns "tv" or "" (no hint). Per spec section 1.6, this hint controls
// which TMDB search endpoint is tried first.
func detectMediaTypeHint(rawTitle string) string {
	if tvHintPattern.MatchString(rawTitle) {
		return "tv"
	}
	return ""
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

// setForcedSubtitleAttribute detects forced English subtitle tracks from the
// MakeMKV scan and sets the HasForcedSubtitleTrack attribute on the envelope.
func setForcedSubtitleAttribute(logger *slog.Logger, discInfo *makemkv.DiscInfo, env *ripspec.Envelope) {
	hasForcedTrack := discInfo.HasForcedEnglishSubtitles()
	env.Attributes.HasForcedSubtitleTrack = hasForcedTrack
	result := "none"
	reason := "no_forced_track_found"
	if hasForcedTrack {
		result = "detected"
		reason = "disc_has_forced_track"
	}
	logger.Info("forced subtitle detection",
		"decision_type", logs.DecisionForcedSubtitleDetection,
		"decision_result", result,
		"decision_reason", reason,
		"has_forced_subtitle_track", hasForcedTrack,
	)
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
	logger *slog.Logger,
	item *queue.Item,
	discInfo *makemkv.DiscInfo,
	best *tmdb.SearchResult,
	mediaType string,
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
		h.createEpisodePlaceholders(logger, &env)
	}

	return env
}

// createEpisodePlaceholders adds episode entries for each unique title that
// meets the minimum title length threshold. Duplicate titles (same segment map
// or title hash) are skipped -- TV Blu-rays commonly contain multiple playlists
// for the same episode. Each episode gets a placeholder key (e.g., "s01_001")
// and is linked to the title's ID for downstream ripping.
func (h *Handler) createEpisodePlaceholders(logger *slog.Logger, env *ripspec.Envelope) {
	season := env.Metadata.SeasonNumber
	if season <= 0 {
		season = 1
	}

	// Compute median duration of titles passing MinTitleLength for outlier detection.
	medianDur := medianTitleDuration(env.Titles, h.cfg.MakeMKV.MinTitleLength)

	seen := make(map[string]int) // dedup key -> first title ID
	var duplicates int

	idx := 0
	for _, title := range env.Titles {
		if title.Duration < h.cfg.MakeMKV.MinTitleLength {
			logger.Debug("title below minimum duration for placeholder",
				"title_id", title.ID,
				"duration", title.Duration,
				"min_title_length", h.cfg.MakeMKV.MinTitleLength,
			)
			continue
		}

		// Episode runtime filter: skip titles whose duration is less than
		// half the median of all candidate titles. This excludes bonus
		// features and menus without hardcoding an episode-length window.
		if medianDur > 0 && title.Duration < medianDur/2 {
			logger.Info("title duration outlier filtered",
				"decision_type", logs.DecisionEpisodeRuntimeFilter,
				"decision_result", "skipped",
				"decision_reason", fmt.Sprintf("title %d duration %ds < half median %ds",
					title.ID, title.Duration, medianDur),
			)
			continue
		}

		// Dedup on SegmentMap (m2ts stream identity) when available.
		// Titles sharing a segment map reference identical content even
		// if playlist metadata differs. Fall back to TitleHash for DVDs
		// where SegmentMap is absent.
		dedupKey := strings.TrimSpace(title.SegmentMap)
		if dedupKey == "" {
			dedupKey = strings.TrimSpace(title.TitleHash)
		}
		if dedupKey != "" {
			if firstID, dup := seen[dedupKey]; dup {
				duplicates++
				logger.Info("duplicate TV title skipped",
					"decision_type", logs.DecisionDuplicateDetection,
					"decision_result", "skipped",
					"decision_reason", fmt.Sprintf("title %d matches title %d (key=%s)",
						title.ID, firstID, dedupKey),
				)
				continue
			}
			seen[dedupKey] = title.ID
		}

		idx++
		env.Episodes = append(env.Episodes, ripspec.Episode{
			Key:            ripspec.PlaceholderKey(season, idx),
			TitleID:        title.ID,
			Season:         season,
			RuntimeSeconds: title.Duration,
		})
	}

	logger.Info("episode placeholders created",
		"decision_type", logs.DecisionEpisodePlaceholders,
		"decision_result", fmt.Sprintf("%d episodes", idx),
		"decision_reason", fmt.Sprintf("season=%d titles=%d duplicates=%d", season, len(env.Titles), duplicates),
	)
}

// medianTitleDuration returns the median duration of titles whose duration
// is at least minDur. Returns 0 if no titles qualify.
func medianTitleDuration(titles []ripspec.Title, minDur int) int {
	var durations []int
	for _, t := range titles {
		if t.Duration >= minDur {
			durations = append(durations, t.Duration)
		}
	}
	if len(durations) == 0 {
		return 0
	}
	slices.Sort(durations)
	return durations[len(durations)/2]
}

// buildEnvelopeFromCache constructs an envelope from a disc ID cache entry
// and MakeMKV scan results. The cache provides TMDB metadata (skipping the
// TMDB search), while the scan provides title data for ripping.
func (h *Handler) buildEnvelopeFromCache(logger *slog.Logger, item *queue.Item, entry *discidcache.Entry, discInfo *makemkv.DiscInfo, discSource string) ripspec.Envelope {
	discName := discInfoName(discInfo)
	seasonNum := extractSeasonNumber(item.DiscTitle, discName)
	discNum := extractDiscNumber(item.DiscTitle, discName)

	env := ripspec.Envelope{
		Version:     ripspec.CurrentVersion,
		Fingerprint: item.DiscFingerprint,
		Metadata: ripspec.Metadata{
			ID:           entry.TMDBID,
			Title:        entry.Title,
			MediaType:    entry.MediaType,
			Year:         entry.Year,
			Movie:        entry.MediaType == "movie",
			Cached:       true,
			DiscSource:   discSource,
			SeasonNumber: seasonNum,
			DiscNumber:   discNum,
		},
	}

	if entry.MediaType == "tv" {
		env.Metadata.ShowTitle = entry.Title
	}

	env.Attributes.HasForcedSubtitleTrack = entry.HasForcedSubtitleTrack

	// Populate titles from MakeMKV scan results.
	env.Titles = convertTitles(discInfo)

	// For TV content, create episode placeholders from eligible titles.
	if entry.MediaType == "tv" {
		h.createEpisodePlaceholders(logger, &env)
	}

	return env
}

// buildFallbackEnvelope constructs an envelope with unknown media type for review.
func (h *Handler) buildFallbackEnvelope(logger *slog.Logger, item *queue.Item, discInfo *makemkv.DiscInfo) ripspec.Envelope {
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
		h.createEpisodePlaceholders(logger, &env)
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
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	item.MetadataJSON = string(metaJSON)

	// Persist RipSpec via queue helper.
	return queue.PersistRipSpec(ctx, h.store, item, env)
}
