package identification

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/disc"
	"spindle/internal/discidcache"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services"
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

	// Fetch fresh metadata from TMDB using the cached ID
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

	// Build metadata from TMDB response or cached data
	var identifiedTitle, releaseDate, year string
	var voteAverage float64
	var voteCount int64

	if tmdbResult != nil {
		identifiedTitle = pickTitle(*tmdbResult)
		releaseDate = tmdbResult.ReleaseDate
		if mediaType == "tv" && tmdbResult.FirstAirDate != "" {
			releaseDate = tmdbResult.FirstAirDate
		}
		voteAverage = tmdbResult.VoteAverage
		voteCount = tmdbResult.VoteCount
	} else {
		identifiedTitle = cacheEntry.Title
	}

	if releaseDate != "" && len(releaseDate) >= 4 {
		year = releaseDate[:4]
	} else if cacheEntry.Year != "" {
		year = cacheEntry.Year
	}

	titleWithYear := identifiedTitle
	if year != "" {
		titleWithYear = fmt.Sprintf("%s (%s)", identifiedTitle, year)
	}

	seasonNumber := cacheEntry.SeasonNumber
	var episodeMatches map[int]episodeAnnotation
	var matchedEpisodes []int

	// For TV shows, fetch episode details
	if mediaType == "tv" {
		if seasonNumber == 0 {
			seasonNumber = 1
		}
		discNumber := 0
		if n, ok := extractDiscNumber(item.DiscTitle); ok {
			discNumber = n
		}
		matches, episodes := i.annotateEpisodes(ctx, logger, tmdbID, seasonNumber, discNumber, scanResult)
		episodeMatches = matches
		matchedEpisodes = episodes
	}

	// Build metadata map
	metadata := map[string]any{
		"id":            tmdbID,
		"title":         identifiedTitle,
		"media_type":    mediaType,
		"release_date":  releaseDate,
		"vote_average":  voteAverage,
		"vote_count":    voteCount,
		"movie":         mediaType != "tv",
		"season_number": seasonNumber,
		"cached":        true,
	}

	if tmdbResult != nil {
		metadata["overview"] = tmdbResult.Overview
		metadata["first_air_date"] = tmdbResult.FirstAirDate
	}

	if len(matchedEpisodes) > 0 {
		metadata["episode_numbers"] = matchedEpisodes
	}
	if len(episodeMatches) > 0 {
		airDates := make([]string, 0, len(episodeMatches))
		for _, ann := range episodeMatches {
			if strings.TrimSpace(ann.Air) != "" {
				airDates = append(airDates, ann.Air)
			}
		}
		if len(airDates) > 0 {
			metadata["episode_air_dates"] = airDates
		}
	}
	if mediaType == "tv" {
		metadata["show_title"] = identifiedTitle
	}
	if cacheEntry.Edition != "" {
		metadata["edition"] = cacheEntry.Edition
		logger.Info("edition from cache",
			logging.String(logging.FieldDecisionType, "edition_detection"),
			logging.String("decision_result", "cached"),
			logging.String("decision_reason", "disc_id_cache"),
			logging.String("edition_label", cacheEntry.Edition))
	}

	// Build filename
	var metaRecord queue.Metadata
	if mediaType == "tv" {
		metaRecord = queue.NewTVMetadata(identifiedTitle, seasonNumber, matchedEpisodes, fmt.Sprintf("%s Season %02d", identifiedTitle, seasonNumber))
	} else {
		metaRecord = queue.NewBasicMetadata(titleWithYear, true)
		if cacheEntry.Edition != "" {
			metaRecord.Edition = cacheEntry.Edition
		}
	}
	metadata["filename"] = metaRecord.GetFilename()

	// Encode and store metadata
	encodedMetadata, encodeErr := json.Marshal(metadata)
	if encodeErr != nil {
		return services.Wrap(services.ErrTransient, "identification", "encode metadata", "Failed to encode TMDB metadata", encodeErr)
	}
	item.MetadataJSON = string(encodedMetadata)

	// Update display title
	displayTitle := titleWithYear
	if mediaType == "tv" {
		displayTitle = fmt.Sprintf("%s Season %02d", identifiedTitle, seasonNumber)
		if year != "" {
			displayTitle = fmt.Sprintf("%s Season %02d (%s)", identifiedTitle, seasonNumber, year)
		}
	}
	item.DiscTitle = displayTitle
	item.ProgressStage = "Identified"
	item.ProgressPercent = 100
	item.ProgressMessage = fmt.Sprintf("Identified as: %s (cached)", item.DiscTitle)

	contentKey := fmt.Sprintf("tmdb:%s:%d", mediaType, tmdbID)

	// Build attributes
	attributes := make(map[string]any)
	discNumber := 0
	discSources := []string{item.DiscTitle}
	if scanResult != nil && scanResult.BDInfo != nil {
		discSources = append(discSources, scanResult.BDInfo.VolumeIdentifier, scanResult.BDInfo.DiscName)
	}
	if n, ok := extractDiscNumber(discSources...); ok {
		discNumber = n
		attributes["disc_number"] = discNumber
	}
	if scanResult.HasForcedEnglishSubtitles() {
		attributes["has_forced_subtitle_track"] = true
	}

	// Build rip specs
	titleSpecs, episodeSpecs := buildRipSpecs(logger, scanResult, episodeMatches, identifiedTitle, item.DiscTitle, metadata)

	ripFingerprint := strings.TrimSpace(item.DiscFingerprint)
	spec := ripspec.Envelope{
		Fingerprint: ripFingerprint,
		ContentKey:  contentKey,
		Metadata:    metadata,
		Attributes:  attributes,
		Titles:      titleSpecs,
		Episodes:    episodeSpecs,
	}

	encodedSpec, err := spec.Encode()
	if err != nil {
		return services.Wrap(services.ErrTransient, "identification", "encode rip spec", "Failed to serialize rip specification", err)
	}
	item.RipSpecData = encodedSpec

	// Log identification
	logger.Info("disc identified from cache",
		logging.String(logging.FieldDecisionType, "tmdb_identification"),
		logging.String("decision_result", "cache_hit"),
		logging.String("decision_reason", "disc_id_cache"),
		logging.String(logging.FieldEventType, "status"),
		logging.Int64("tmdb_id", tmdbID),
		logging.String("identified_title", identifiedTitle),
		logging.String("media_type", mediaType),
		logging.Duration("identification_duration", time.Since(stageStart)))

	// Send notification
	if i.notifier != nil && year != "" {
		payload := notifications.Payload{
			"title":        identifiedTitle,
			"year":         year,
			"mediaType":    mediaType,
			"displayTitle": titleWithYear,
			"cached":       true,
		}
		if err := i.notifier.Publish(ctx, notifications.EventIdentificationCompleted, payload); err != nil {
			logger.Debug("identification notification failed", logging.Error(err))
		}
	}

	// Validate and finalize
	if err := i.validateIdentification(ctx, item); err != nil {
		return err
	}

	i.logStageSummary(ctx, item, stageStart, true, titleCount, tmdbID, mediaType)

	return nil
}

// populateDiscIDCache stores the identification result in the disc ID cache.
func (i *Identifier) populateDiscIDCache(
	logger *slog.Logger,
	discID string,
	tmdbID int64,
	mediaType, title, edition string,
	seasonNumber int,
	year string,
) {
	if i.discIDCache == nil || discID == "" {
		return
	}

	entry := discidcache.Entry{
		DiscID:       discID,
		TMDBID:       tmdbID,
		MediaType:    mediaType,
		Title:        title,
		Edition:      edition,
		SeasonNumber: seasonNumber,
		Year:         year,
		CachedAt:     time.Now(),
	}

	if err := i.discIDCache.Store(entry); err != nil {
		logger.Warn("failed to cache disc id mapping",
			logging.String(logging.FieldEventType, "discidcache_store_failed"),
			logging.Error(err),
			logging.String("disc_id", discID))
	} else {
		logger.Debug("cached disc id mapping",
			logging.String("disc_id", discID),
			logging.Int64("tmdb_id", tmdbID),
			logging.String("title", title))
	}
}

// fetchTMDBDetails retrieves movie or TV details from TMDB by ID.
func (i *Identifier) fetchTMDBDetails(ctx context.Context, mediaType string, tmdbID int64) (*tmdb.Result, error) {
	if mediaType == "tv" {
		return i.tmdbInfo.GetTVDetails(ctx, tmdbID)
	}
	return i.tmdbInfo.GetMovieDetails(ctx, tmdbID)
}
