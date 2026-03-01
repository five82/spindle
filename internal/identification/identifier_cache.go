package identification

import (
	"context"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/disc"
	"spindle/internal/discidcache"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/queue"
)

// completeIdentificationFromCache performs identification using a cached disc ID mapping.
// This skips KeyDB lookup, title parsing, TMDB search, and confidence scoring.
// We still fetch fresh metadata from TMDB using the cached ID.
func (i *Identifier) completeIdentificationFromCache(
	ctx context.Context,
	logger *slog.Logger,
	item *queue.Item,
	scanResult *disc.ScanResult,
	cacheEntry discidcache.Entry,
	stageStart time.Time,
	titleCount int,
) error {
	if err := i.updateProgress(ctx, item, "Using cached identification", 50); err != nil {
		logger.Debug("failed to update cache progress", logging.Error(err))
	}

	mediaType := cacheEntry.MediaType
	tmdbID := cacheEntry.TMDBID

	// Fetch fresh metadata from TMDB using the cached ID.
	tmdbResult, err := i.fetchTMDBDetails(ctx, mediaType, tmdbID)
	if err != nil {
		logger.Warn("failed to fetch details from TMDB",
			logging.String(logging.FieldEventType, "tmdb_fetch_failed"),
			logging.Error(err),
			logging.Int64("tmdb_id", tmdbID),
			logging.String("media_type", mediaType),
			logging.String(logging.FieldErrorHint, "cache entry may be stale"),
			logging.String(logging.FieldImpact, "falling back to cached title"))
	}

	// Resolve identification data from TMDB response or cache fallback.
	var identifiedTitle, releaseDate, firstAirDate, overview string
	var voteAverage float64
	var voteCount int64

	if tmdbResult != nil {
		identifiedTitle = pickTitle(*tmdbResult)
		releaseDate = tmdbResult.ReleaseDate
		firstAirDate = tmdbResult.FirstAirDate
		if mediaType == MediaTypeTV && firstAirDate != "" {
			releaseDate = firstAirDate
		}
		overview = tmdbResult.Overview
		voteAverage = tmdbResult.VoteAverage
		voteCount = tmdbResult.VoteCount
	} else {
		identifiedTitle = cacheEntry.Title
	}

	year := yearFromDate(releaseDate)
	if year == "" && cacheEntry.Year != "" {
		year = cacheEntry.Year
	}

	seasonNumber := cacheEntry.SeasonNumber
	var episodeMatches map[int]episodeAnnotation
	if mediaType == MediaTypeTV {
		if seasonNumber == 0 {
			seasonNumber = 1
		}
		if scanResult != nil {
			episodeMatches = buildPlaceholderAnnotations(scanResult.Titles, seasonNumber)
		}
	}

	if cacheEntry.Edition != "" {
		logger.Info("edition from cache",
			logging.String(logging.FieldDecisionType, "edition_detection"),
			logging.String("decision_result", "cached"),
			logging.String("decision_reason", "disc_id_cache"),
			logging.String("edition_label", cacheEntry.Edition))
	}

	// Build disc sources for disc number extraction.
	discSources := collectDiscSources(scanResult, strings.TrimSpace(item.DiscTitle))

	// Finalize through shared path.
	r := identificationResult{
		IdentifiedTitle: identifiedTitle,
		MediaType:       mediaType,
		TMDBID:          tmdbID,
		Year:            year,
		ReleaseDate:     releaseDate,
		FirstAirDate:    firstAirDate,
		Overview:        overview,
		SeasonNumber:    seasonNumber,
		VoteAverage:     voteAverage,
		VoteCount:       voteCount,
		Edition:         cacheEntry.Edition,
		Cached:          true,
		EpisodeMatches:  episodeMatches,
		ScanResult:      scanResult,
		DiscSources:     discSources,
		FallbackTitle:   strings.TrimSpace(item.DiscTitle),
	}
	if err := i.finalizeIdentifiedItem(ctx, logger, item, r); err != nil {
		return err
	}

	logger.Info("disc identified from cache",
		logging.String(logging.FieldDecisionType, "tmdb_identification"),
		logging.String("decision_result", "cache_hit"),
		logging.String("decision_reason", "disc_id_cache"),
		logging.String(logging.FieldEventType, "status"),
		logging.Int64("tmdb_id", tmdbID),
		logging.String("identified_title", identifiedTitle),
		logging.String("media_type", mediaType),
		logging.Duration("identification_duration", time.Since(stageStart)))

	i.logStageSummary(ctx, item, stageStart, true, titleCount, tmdbID, mediaType)
	return nil
}

// populateDiscIDCache stores the identification result in the disc ID cache.
func (i *Identifier) populateDiscIDCache(logger *slog.Logger, entry discidcache.Entry) {
	if i.discIDCache == nil || entry.DiscID == "" {
		return
	}

	if entry.CachedAt.IsZero() {
		entry.CachedAt = time.Now()
	}

	if err := i.discIDCache.Store(entry); err != nil {
		logger.Warn("failed to cache disc id mapping",
			logging.String(logging.FieldEventType, "discidcache_store_failed"),
			logging.Error(err),
			logging.String("disc_id", entry.DiscID))
	} else {
		logger.Debug("cached disc id mapping",
			logging.String("disc_id", entry.DiscID),
			logging.Int64("tmdb_id", entry.TMDBID),
			logging.String("title", entry.Title))
	}
}

// fetchTMDBDetails retrieves movie or TV details from TMDB by ID.
func (i *Identifier) fetchTMDBDetails(ctx context.Context, mediaType string, tmdbID int64) (*tmdb.Result, error) {
	if mediaType == MediaTypeTV {
		return i.tmdbInfo.GetTVDetails(ctx, tmdbID)
	}
	return i.tmdbInfo.GetMovieDetails(ctx, tmdbID)
}
