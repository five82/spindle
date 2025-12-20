package identification

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/disc"
	"spindle/internal/identification/overrides"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/services"
)

type identifyContext struct {
	Title      string
	DiscLabel  string
	DiscNumber int
	SearchOpts tmdb.SearchOptions
	MediaHint  mediaKind
	Override   *overrides.Override
	ScanResult *disc.ScanResult
}

type identifyOutcome struct {
	Identified      bool
	MediaType       string
	ContentKey      string
	IdentifiedTitle string
	Year            string
	TMDBID          int64
	SeasonNumber    int
	EpisodeMatches  map[int]episodeAnnotation
	MatchedEpisodes []int
	Metadata        map[string]any
}

func (i *Identifier) identifyWithTMDB(ctx context.Context, logger *slog.Logger, item *queue.Item, input identifyContext) (identifyOutcome, error) {
	// Default metadata assumes unidentified content until TMDB lookup succeeds.
	metadata := map[string]any{
		"title": strings.TrimSpace(input.Title),
	}
	if input.DiscNumber > 0 {
		metadata["disc_number"] = input.DiscNumber
	}
	if hint := input.MediaHint.String(); hint != "unknown" {
		metadata["media_type"] = hint
	}
	mediaType := fmt.Sprintf("%v", metadata["media_type"])
	if mediaType == "<nil>" || strings.TrimSpace(mediaType) == "" {
		mediaType = "unknown"
	}
	contentKey := unknownContentKey(item.DiscFingerprint)
	identified := false
	var (
		identifiedTitle string
		year            string
		tmdbID          int64
		seasonNumber    int
		episodeMatches  map[int]episodeAnnotation
		matchedEpisodes []int
	)
	if input.Override != nil && input.Override.Season > 0 {
		seasonNumber = input.Override.Season
	}

	showHintSources := []string{input.Title}
	if input.DiscLabel != "" {
		showHintSources = append(showHintSources, input.DiscLabel)
	}
	if input.ScanResult != nil && input.ScanResult.BDInfo != nil {
		if input.ScanResult.BDInfo.DiscName != "" {
			showHintSources = append(showHintSources, input.ScanResult.BDInfo.DiscName)
		}
		if input.ScanResult.BDInfo.VolumeIdentifier != "" {
			showHintSources = append(showHintSources, input.ScanResult.BDInfo.VolumeIdentifier)
		}
	}
	if input.Override != nil && strings.TrimSpace(input.Override.Title) != "" {
		showHintSources = append(showHintSources, input.Override.Title)
	}
	showHint, hintedSeason := deriveShowHint(showHintSources...)
	if seasonNumber == 0 && hintedSeason > 0 {
		seasonNumber = hintedSeason
	}

	if season, ok := extractSeasonNumber(input.Title, input.DiscLabel); ok {
		seasonNumber = season
	}
	logger.Debug("identification heuristics",
		logging.String("media_hint", input.MediaHint.String()),
		logging.Int("season_guess", seasonNumber))

	queryInputs := []string{input.Title, showHint}
	if input.Override != nil {
		queryInputs = append(queryInputs, input.Override.Title)
	}
	if input.DiscLabel != "" {
		queryInputs = append(queryInputs, input.DiscLabel)
	}
	seasonQuerySource := strings.TrimSpace(showHint)
	if seasonQuerySource == "" {
		seasonQuerySource = strings.TrimSpace(input.Title)
	}
	if seasonNumber > 0 && seasonQuerySource != "" {
		queryInputs = append(queryInputs, fmt.Sprintf("%s Season %d", seasonQuerySource, seasonNumber))
	}
	queries := buildQueryList(queryInputs...)
	if len(queries) == 0 {
		queries = []string{strings.TrimSpace(input.Title)}
	}

	if isPlaceholderTitle(input.Title, input.DiscLabel) {
		logger.Info("tmdb lookup skipped for placeholder title",
			logging.String("title", input.Title),
			logging.String("disc_label", input.DiscLabel),
			logging.String("reason", "title is generic/placeholder; cannot perform meaningful search"))
		i.scheduleReview(ctx, item, "Disc title placeholder; manual identification required")
		return identifyOutcome{
			Identified:      identified,
			MediaType:       mediaType,
			ContentKey:      contentKey,
			IdentifiedTitle: identifiedTitle,
			Year:            year,
			TMDBID:          tmdbID,
			SeasonNumber:    seasonNumber,
			EpisodeMatches:  episodeMatches,
			MatchedEpisodes: matchedEpisodes,
			Metadata:        metadata,
		}, nil
	}

	var (
		best         *tmdb.Result
		response     *tmdb.Response
		modeUsed     searchMode
		searchErr    error
		tmdbStart    = time.Now()
		queriesCount int
	)
	for _, candidate := range queries {
		queriesCount++
		resp, mode, err := i.performTMDBSearch(ctx, logger, candidate, input.SearchOpts, input.MediaHint)
		if err != nil {
			searchErr = err
			logger.Warn("tmdb search attempt failed", logging.String("query", candidate), logging.Error(err))
			continue
		}
		response = resp
		modeUsed = mode
		if response != nil {
			logger.Debug("tmdb response received",
				logging.Int("result_count", len(response.Results)),
				logging.Int("search_year", input.SearchOpts.Year),
				logging.Int("search_runtime", input.SearchOpts.Runtime),
				logging.String("search_mode", string(modeUsed)),
				logging.String("query", candidate))
			for idx, result := range response.Results {
				logger.Debug("tmdb search result",
					logging.Int("index", idx),
					logging.Int64("tmdb_id", result.ID),
					logging.String("title", result.Title),
					logging.String("release_date", result.ReleaseDate),
					logging.Float64("vote_average", result.VoteAverage),
					logging.Int64("vote_count", result.VoteCount),
					logging.Float64("popularity", result.Popularity),
					logging.String("media_type", result.MediaType))
			}
		}
		best = selectBestResult(logger, candidate, response)
		if best != nil {
			break
		}
	}
	if best == nil {
		lastQuery := queries[len(queries)-1]
		tmdbDuration := time.Since(tmdbStart)
		if searchErr != nil {
			logger.Warn("tmdb search failed",
				logging.String("query", lastQuery),
				logging.Error(searchErr),
				logging.Int("queries_attempted", queriesCount),
				logging.Duration("total_tmdb_duration", tmdbDuration))
			i.scheduleReview(ctx, item, "TMDB lookup failed")
		} else {
			logger.Warn("tmdb confidence scoring failed",
				logging.String("query", lastQuery),
				logging.String("reason", "No result met confidence threshold"),
				logging.Int("queries_attempted", queriesCount),
				logging.Duration("total_tmdb_duration", tmdbDuration))
			i.scheduleReview(ctx, item, "No confident TMDB match")
		}
		return identifyOutcome{
			Identified:      identified,
			MediaType:       mediaType,
			ContentKey:      contentKey,
			IdentifiedTitle: identifiedTitle,
			Year:            year,
			TMDBID:          tmdbID,
			SeasonNumber:    seasonNumber,
			EpisodeMatches:  episodeMatches,
			MatchedEpisodes: matchedEpisodes,
			Metadata:        metadata,
		}, nil
	}

	identified = true
	mediaType = strings.ToLower(strings.TrimSpace(best.MediaType))
	if mediaType == "" {
		switch modeUsed {
		case searchModeTV:
			mediaType = "tv"
		case searchModeMulti:
			mediaType = strings.TrimSpace(best.MediaType)
			if mediaType == "" {
				mediaType = "movie"
			}
		default:
			mediaType = "movie"
		}
	}
	isMovie := mediaType != "tv"
	identifiedTitle = pickTitle(*best)
	year = ""
	titleWithYear := identifiedTitle
	releaseDate := best.ReleaseDate
	if mediaType == "tv" && strings.TrimSpace(best.FirstAirDate) != "" {
		releaseDate = best.FirstAirDate
	}
	if releaseDate != "" && len(releaseDate) >= 4 {
		year = releaseDate[:4]
		titleWithYear = fmt.Sprintf("%s (%s)", identifiedTitle, year)
	}
	tmdbID = best.ID
	if mediaType == "tv" {
		if seasonNumber == 0 {
			if season, ok := extractSeasonNumber(item.DiscTitle, input.Title, input.DiscLabel); ok {
				seasonNumber = season
			}
		}
		if seasonNumber == 0 {
			seasonNumber = 1
		}
		matches, episodes := i.annotateEpisodes(ctx, logger, tmdbID, seasonNumber, input.DiscNumber, input.ScanResult)
		episodeMatches = matches
		matchedEpisodes = episodes
	}
	metadata = map[string]any{
		"id":             best.ID,
		"title":          identifiedTitle,
		"overview":       best.Overview,
		"media_type":     mediaType,
		"release_date":   releaseDate,
		"first_air_date": best.FirstAirDate,
		"vote_average":   best.VoteAverage,
		"vote_count":     best.VoteCount,
		"movie":          isMovie,
		"season_number":  seasonNumber,
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
	var metaRecord queue.Metadata
	if mediaType == "tv" {
		metaRecord = queue.NewTVMetadata(identifiedTitle, seasonNumber, matchedEpisodes, fmt.Sprintf("%s Season %02d", identifiedTitle, seasonNumber))
	} else {
		metaRecord = queue.NewBasicMetadata(titleWithYear, true)
	}
	metadata["filename"] = metaRecord.GetFilename()
	if mediaType == "tv" {
		metadata["show_title"] = identifiedTitle
	}

	encodedMetadata, encodeErr := json.Marshal(metadata)
	if encodeErr != nil {
		return identifyOutcome{}, services.Wrap(services.ErrTransient, "identification", "encode metadata", "Failed to encode TMDB metadata", encodeErr)
	}
	item.MetadataJSON = string(encodedMetadata)
	// Update DiscTitle to the proper TMDB title with season/year for subsequent stages
	displayTitle := titleWithYear
	if mediaType == "tv" {
		displayTitle = fmt.Sprintf("%s Season %02d", identifiedTitle, seasonNumber)
		if strings.TrimSpace(year) != "" {
			displayTitle = fmt.Sprintf("%s Season %02d (%s)", identifiedTitle, seasonNumber, year)
		}
	}
	item.DiscTitle = displayTitle
	item.ProgressStage = "Identified"
	item.ProgressPercent = 100
	item.ProgressMessage = fmt.Sprintf("Identified as: %s", item.DiscTitle)
	contentKey = fmt.Sprintf("tmdb:%s:%d", mediaType, tmdbID)

	logger.Info(
		"disc identified",
		logging.Int64("tmdb_id", best.ID),
		logging.String("identified_title", identifiedTitle),
		logging.String("media_type", strings.TrimSpace(best.MediaType)),
		logging.Int("queries_attempted", queriesCount),
		logging.Duration("tmdb_search_duration", time.Since(tmdbStart)),
		logging.Float64("vote_average", best.VoteAverage),
		logging.Int64("vote_count", best.VoteCount),
	)
	if i.notifier != nil {
		notifyType := mediaType
		if notifyType == "" {
			notifyType = "unknown"
		}
		if strings.TrimSpace(year) != "" {
			payload := notifications.Payload{
				"title":        identifiedTitle,
				"year":         strings.TrimSpace(year),
				"mediaType":    notifyType,
				"displayTitle": titleWithYear,
			}
			if err := i.notifier.Publish(ctx, notifications.EventIdentificationCompleted, payload); err != nil {
				logger.Debug("identification notification failed", logging.Error(err))
			}
		}
	}

	return identifyOutcome{
		Identified:      identified,
		MediaType:       mediaType,
		ContentKey:      contentKey,
		IdentifiedTitle: identifiedTitle,
		Year:            year,
		TMDBID:          tmdbID,
		SeasonNumber:    seasonNumber,
		EpisodeMatches:  episodeMatches,
		MatchedEpisodes: matchedEpisodes,
		Metadata:        metadata,
	}, nil
}
