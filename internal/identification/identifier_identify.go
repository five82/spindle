package identification

import (
	"context"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/disc"
	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/services"
)

type identifyContext struct {
	Title      string
	DiscLabel  string
	DiscNumber int
	SearchOpts tmdb.SearchOptions
	MediaHint  mediaKind
	ScanResult *disc.ScanResult
}

type identifyOutcome struct {
	Identified      bool
	MediaType       string
	ContentKey      string
	IdentifiedTitle string
	Year            string
	ReleaseDate     string
	FirstAirDate    string
	Overview        string
	TMDBID          int64
	SeasonNumber    int
	VoteAverage     float64
	VoteCount       int64
	Edition         string
	EpisodeMatches  map[int]episodeAnnotation
	Metadata        map[string]any // only populated for unidentified fallback
}

func (i *Identifier) identifyWithTMDB(ctx context.Context, logger *slog.Logger, item *queue.Item, input identifyContext) (identifyOutcome, error) {
	// Default metadata assumes unidentified content until TMDB lookup succeeds.
	metadata := map[string]any{
		"title": strings.TrimSpace(input.Title),
	}
	if input.DiscNumber > 0 {
		metadata["disc_number"] = input.DiscNumber
	}
	mediaType := input.MediaHint.String()
	if mediaType != "unknown" {
		metadata["media_type"] = mediaType
	}
	contentKey := unknownContentKey(item.DiscFingerprint)
	var (
		identifiedTitle string
		year            string
		tmdbID          int64
		seasonNumber    int
		episodeMatches  map[int]episodeAnnotation
	)
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
	showHint, hintedSeason := deriveShowHint(showHintSources...)
	if seasonNumber == 0 && hintedSeason > 0 {
		seasonNumber = hintedSeason
	}

	if season, ok := extractSeasonNumber(input.Title, input.DiscLabel); ok {
		seasonNumber = season
	}
	logger.Info("identification heuristics",
		logging.String(logging.FieldDecisionType, "identification_heuristics"),
		logging.String("decision_result", "computed"),
		logging.String("decision_reason", "title_and_scan_analysis"),
		logging.String("media_hint", input.MediaHint.String()),
		logging.Int("season_guess", seasonNumber))

	// Extract canonical title from keydb format "DISC_LABEL (CANONICAL_TITLE)"
	canonicalTitle, discLabelPart := extractCanonicalTitle(input.Title)

	titleForQuery, titleYear := splitTitleYear(input.Title)
	discLabelForQuery, labelYear := splitTitleYear(input.DiscLabel)
	if input.SearchOpts.Year == 0 {
		var source, reason string
		var year int
		switch {
		case titleYear > 0:
			year, source, reason = titleYear, "title", "extracted_from_title"
		case labelYear > 0:
			year, source, reason = labelYear, "disc_label", "extracted_from_disc_label"
		}
		if year > 0 {
			input.SearchOpts.Year = year
			logger.Info("year source decision",
				logging.String(logging.FieldDecisionType, "year_source"),
				logging.String("decision_result", source),
				logging.String("decision_reason", reason),
				logging.Int("year", year))
		}
	}

	// Build query inputs prioritizing canonical title from keydb
	var queryInputs []string
	if canonicalTitle != "" {
		// Strip edition suffixes for TMDB search (e.g., "Director's Edition")
		// since TMDB typically only has the base movie title
		strippedCanonical := StripEditionSuffix(canonicalTitle)
		if strippedCanonical != "" && strippedCanonical != canonicalTitle {
			queryInputs = append(queryInputs, strippedCanonical)
			logger.Debug("stripped edition suffix from canonical title",
				logging.String("original", canonicalTitle),
				logging.String("stripped", strippedCanonical))
		}
		// Also try the full canonical title in case TMDB has edition variants
		queryInputs = append(queryInputs, canonicalTitle)
		logger.Debug("using canonical title from keydb format",
			logging.String("canonical_title", canonicalTitle),
			logging.String("disc_label_part", discLabelPart))
	}
	// Add other candidates as fallbacks
	if discLabelPart != "" {
		queryInputs = append(queryInputs, discLabelPart)
	} else if canonicalTitle == "" {
		// No keydb format detected, use original title
		queryInputs = append(queryInputs, titleForQuery)
	}
	if showHint != "" {
		queryInputs = append(queryInputs, showHint)
	}
	if discLabelForQuery != "" && discLabelForQuery != discLabelPart {
		queryInputs = append(queryInputs, discLabelForQuery)
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
			logging.String(logging.FieldDecisionType, "tmdb_search"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "placeholder_title"),
			logging.String("decision_options", "search, review"),
			logging.String("title", input.Title),
			logging.String("disc_label", input.DiscLabel),
			logging.String("reason", "title is generic/placeholder; cannot perform meaningful search"))
		i.flagReview(ctx, item, "Disc title placeholder; manual identification required", false)
		return identifyOutcome{
			Identified:      false,
			MediaType:       mediaType,
			ContentKey:      contentKey,
			IdentifiedTitle: identifiedTitle,
			Year:            year,
			TMDBID:          tmdbID,
			SeasonNumber:    seasonNumber,
			EpisodeMatches:  episodeMatches,
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
			logger.Debug("tmdb search attempt failed",
				logging.String("query", candidate),
				logging.Error(err))
			continue
		}
		response = resp
		modeUsed = mode
		if response != nil {
			attrs := []logging.Attr{
				logging.Int("result_count", len(response.Results)),
				logging.Int("search_year", input.SearchOpts.Year),
				logging.Int("search_runtime", input.SearchOpts.Runtime),
				logging.String("search_mode", string(modeUsed)),
				logging.String("query", candidate),
			}
			if len(response.Results) > 0 {
				best := response.Results[0]
				attrs = append(attrs,
					logging.Int64("best_tmdb_id", best.ID),
					logging.String("best_title", best.Title),
					logging.String("best_release_date", best.ReleaseDate),
					logging.Float64("best_vote_average", best.VoteAverage),
				)
				if len(response.Results) > 1 {
					attrs = append(attrs, logging.Int("results_hidden_count", len(response.Results)-1))
				}
			}
			logger.Debug("tmdb search results", logging.Args(attrs...)...)
		}
		minVoteCount := 0
		if i.cfg != nil {
			minVoteCount = i.cfg.Validation.MinVoteCountExactMatch
		}
		best = selectBestResult(logger, candidate, response, minVoteCount)
		if best != nil {
			break
		}
	}
	if best == nil {
		lastQuery := queries[len(queries)-1]
		tmdbDuration := time.Since(tmdbStart)
		if searchErr != nil {
			logger.Warn("tmdb search failed",
				logging.String(logging.FieldDecisionType, "tmdb_search"),
				logging.String("decision_result", "failed"),
				logging.String("decision_reason", "search_error"),
				logging.String("decision_options", "retry, review"),
				logging.String(logging.FieldEventType, "tmdb_search_failed"),
				logging.String("query", lastQuery),
				logging.Error(searchErr),
				logging.String("error_message", "TMDB search failed"),
				logging.String(logging.FieldErrorHint, "Check TMDB API key, network connectivity, and search query"),
				logging.String(logging.FieldImpact, "item moved to review for manual identification"),
				logging.Int("queries_attempted", queriesCount),
				logging.Duration("total_tmdb_duration", tmdbDuration))
			i.flagReview(ctx, item, "TMDB lookup failed", false)
		} else {
			logger.Warn("tmdb confidence scoring failed",
				logging.String(logging.FieldDecisionType, "tmdb_confidence"),
				logging.String("decision_result", "rejected"),
				logging.String("decision_reason", "no_result_met_threshold"),
				logging.String("decision_options", "retry, review"),
				logging.String(logging.FieldEventType, "tmdb_confidence_failed"),
				logging.String("query", lastQuery),
				logging.String("reason", "No result met confidence threshold"),
				logging.String(logging.FieldImpact, "item moved to review for manual identification"),
				logging.String(logging.FieldErrorHint, "Adjust tmdb_confidence_threshold or retry with a revised title"),
				logging.Int("queries_attempted", queriesCount),
				logging.Duration("total_tmdb_duration", tmdbDuration))
			i.flagReview(ctx, item, "No confident TMDB match", false)
		}
		return identifyOutcome{
			Identified:      false,
			MediaType:       mediaType,
			ContentKey:      contentKey,
			IdentifiedTitle: identifiedTitle,
			Year:            year,
			TMDBID:          tmdbID,
			SeasonNumber:    seasonNumber,
			EpisodeMatches:  episodeMatches,
			Metadata:        metadata,
		}, nil
	}

	mediaType = determineMediaType(*best, modeUsed)
	identifiedTitle = pickTitle(*best)
	releaseDate := best.ReleaseDate
	if mediaType == "tv" && strings.TrimSpace(best.FirstAirDate) != "" {
		releaseDate = best.FirstAirDate
	}
	if releaseDate != "" && len(releaseDate) >= 4 {
		year = releaseDate[:4]
	}
	tmdbID = best.ID
	if mediaType == "tv" {
		if seasonNumber == 0 {
			seasonNumber = 1
		}
		if input.ScanResult != nil {
			episodeMatches = buildPlaceholderAnnotations(input.ScanResult.Titles, seasonNumber)
		}
	}

	// Detect movie edition (Director's Cut, Extended, etc.)
	var editionLabel string
	if mediaType == "movie" {
		titleWithYear := identifiedTitle
		if year != "" {
			titleWithYear = fmt.Sprintf("%s (%s)", identifiedTitle, year)
		}
		editionLabel = i.detectMovieEdition(ctx, logger, input.Title, identifiedTitle, titleWithYear)
	}

	logger.Info("disc identified",
		logging.String(logging.FieldDecisionType, "tmdb_identification"),
		logging.String("decision_result", "identified"),
		logging.String("decision_reason", "tmdb_match"),
		logging.String("decision_options", "identify, review"),
		logging.String("decision_selected", fmt.Sprintf("%d:%s", best.ID, identifiedTitle)),
		logging.String(logging.FieldEventType, "status"),
		logging.Int64("tmdb_id", best.ID),
		logging.String("identified_title", identifiedTitle),
		logging.String("media_type", strings.TrimSpace(best.MediaType)),
		logging.Int("queries_attempted", queriesCount),
		logging.Duration("tmdb_search_duration", time.Since(tmdbStart)),
		logging.Float64("vote_average", best.VoteAverage),
		logging.Int64("vote_count", best.VoteCount),
	)

	return identifyOutcome{
		Identified:      true,
		MediaType:       mediaType,
		IdentifiedTitle: identifiedTitle,
		Year:            year,
		ReleaseDate:     releaseDate,
		FirstAirDate:    best.FirstAirDate,
		Overview:        best.Overview,
		TMDBID:          tmdbID,
		SeasonNumber:    seasonNumber,
		VoteAverage:     best.VoteAverage,
		VoteCount:       best.VoteCount,
		Edition:         editionLabel,
		EpisodeMatches:  episodeMatches,
	}, nil
}

// determineMediaType resolves the media type from a TMDB result and search mode.
func determineMediaType(result tmdb.Result, mode searchMode) string {
	mediaType := strings.ToLower(strings.TrimSpace(result.MediaType))
	if mediaType != "" {
		return mediaType
	}
	switch mode {
	case searchModeTV:
		return "tv"
	default:
		return "movie"
	}
}

// validateMetadataForPersist ensures required metadata fields are valid before
// persisting to the database. Returns an error if any required field is missing
// or invalid.
func validateMetadataForPersist(title, mediaType string, tmdbID int64) error {
	if strings.TrimSpace(title) == "" {
		return services.Wrap(
			services.ErrValidation,
			"identification",
			"validate metadata",
			"Identified title is empty; cannot persist invalid metadata",
			nil,
		)
	}

	if mediaType != "movie" && mediaType != "tv" {
		return services.Wrap(
			services.ErrValidation,
			"identification",
			"validate metadata",
			fmt.Sprintf("Invalid media type %q; must be 'movie' or 'tv'", mediaType),
			nil,
		)
	}

	if tmdbID <= 0 {
		return services.Wrap(
			services.ErrValidation,
			"identification",
			"validate metadata",
			fmt.Sprintf("Invalid TMDB ID %d; must be positive", tmdbID),
			nil,
		)
	}

	return nil
}
