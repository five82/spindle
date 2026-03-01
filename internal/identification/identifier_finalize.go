package identification

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"log/slog"

	"spindle/internal/disc"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/textutil"
)

// identificationResult captures resolved identification data from either the
// TMDB search path or the disc ID cache path. Both paths fill this struct,
// then call finalizeIdentifiedItem for all common post-identification work.
type identificationResult struct {
	IdentifiedTitle string
	MediaType       string
	TMDBID          int64
	Year            string
	ReleaseDate     string
	FirstAirDate    string
	Overview        string
	SeasonNumber    int
	VoteAverage     float64
	VoteCount       int64
	Edition         string
	Cached          bool
	EpisodeMatches  map[int]episodeAnnotation

	// Context for rip spec and attribute building.
	ScanResult    *disc.ScanResult
	DiscSources   []string // candidates for extractDiscNumber
	DiscNumber    int      // pre-computed disc number (0 = not detected)
	FallbackTitle string   // fallback show label for rip spec
}

// buildAttributes constructs the rip spec attributes map from scan results.
// Returns the attributes and the detected disc number.
func buildAttributes(logger *slog.Logger, scanResult *disc.ScanResult, discSources []string, discNumber int) map[string]any {
	attributes := make(map[string]any)
	if discNumber == 0 {
		if n, ok := extractDiscNumber(discSources...); ok {
			discNumber = n
		}
	}
	if discNumber > 0 {
		attributes["disc_number"] = discNumber
	}
	hasForcedTrack := false
	if scanResult != nil {
		hasForcedTrack = scanResult.HasForcedEnglishSubtitles()
	}
	if hasForcedTrack {
		attributes["has_forced_subtitle_track"] = true
	}
	logger.Info("forced subtitle detection",
		logging.String(logging.FieldDecisionType, "forced_subtitle_detection"),
		logging.String("decision_result", textutil.Ternary(hasForcedTrack, "detected", "none")),
		logging.String("decision_reason", textutil.Ternary(hasForcedTrack, "disc_has_forced_track", "no_forced_track_found")),
		logging.Bool("has_forced_subtitle_track", hasForcedTrack))
	return attributes
}

// storeAndValidateEnvelope encodes a rip spec envelope, stores it on the item,
// and runs post-identification validation.
func (i *Identifier) storeAndValidateEnvelope(ctx context.Context, item *queue.Item, spec ripspec.Envelope) error {
	encodedSpec, err := spec.Encode()
	if err != nil {
		return services.Wrap(services.ErrTransient, "identification", "encode rip spec",
			"Failed to serialize rip specification", err)
	}
	item.RipSpecData = encodedSpec
	return i.validateIdentification(ctx, item)
}

// finalizeIdentifiedItem builds metadata, rip specs, and envelope from resolved
// identification data, then validates and sends a notification. This is the
// single path through which all successful identifications flow.
func (i *Identifier) finalizeIdentifiedItem(
	ctx context.Context,
	logger *slog.Logger,
	item *queue.Item,
	r identificationResult,
) error {
	titleWithYear := r.IdentifiedTitle
	if r.Year != "" {
		titleWithYear = fmt.Sprintf("%s (%s)", r.IdentifiedTitle, r.Year)
	}

	// Build metadata map.
	metadata := map[string]any{
		"id":             r.TMDBID,
		"title":          r.IdentifiedTitle,
		"overview":       r.Overview,
		"media_type":     r.MediaType,
		"release_date":   r.ReleaseDate,
		"first_air_date": r.FirstAirDate,
		"vote_average":   r.VoteAverage,
		"vote_count":     r.VoteCount,
		"movie":          r.MediaType != MediaTypeTV,
		"season_number":  r.SeasonNumber,
	}
	if r.Cached {
		metadata["cached"] = true
	}
	if r.MediaType == MediaTypeTV {
		metadata["show_title"] = r.IdentifiedTitle
	}
	if r.Edition != "" {
		metadata["edition"] = r.Edition
	}

	// Build filename.
	var metaRecord queue.Metadata
	if r.MediaType == MediaTypeTV {
		metaRecord = queue.NewTVMetadata(r.IdentifiedTitle, r.SeasonNumber, nil,
			fmt.Sprintf("%s Season %02d", r.IdentifiedTitle, r.SeasonNumber))
	} else {
		metaRecord = queue.NewBasicMetadata(titleWithYear, true)
		if r.Edition != "" {
			metaRecord.Edition = r.Edition
		}
	}
	metadata["filename"] = metaRecord.GetFilename()

	// Validate metadata.
	if err := validateMetadataForPersist(r.IdentifiedTitle, r.MediaType, r.TMDBID); err != nil {
		logger.Error("metadata validation failed before persist",
			logging.String(logging.FieldEventType, "metadata_validation_failed"),
			logging.String("title", r.IdentifiedTitle),
			logging.String("media_type", r.MediaType),
			logging.Int64("tmdb_id", r.TMDBID),
			logging.Error(err))
		return err
	}

	// Encode and store metadata.
	encodedMetadata, encodeErr := json.Marshal(metadata)
	if encodeErr != nil {
		return services.Wrap(services.ErrTransient, "identification", "encode metadata",
			"Failed to encode TMDB metadata", encodeErr)
	}
	item.MetadataJSON = string(encodedMetadata)

	// Format display title.
	displayTitle := titleWithYear
	if r.MediaType == MediaTypeTV {
		displayTitle = fmt.Sprintf("%s Season %02d", r.IdentifiedTitle, r.SeasonNumber)
		if r.Year != "" {
			displayTitle = fmt.Sprintf("%s Season %02d (%s)", r.IdentifiedTitle, r.SeasonNumber, r.Year)
		}
	}
	item.DiscTitle = displayTitle
	item.ProgressStage = "Identified"
	item.ProgressPercent = 100
	suffix := ""
	if r.Cached {
		suffix = " (cached)"
	}
	item.ProgressMessage = fmt.Sprintf("Identified as: %s%s", item.DiscTitle, suffix)

	contentKey := fmt.Sprintf("tmdb:%s:%d", r.MediaType, r.TMDBID)

	// Build attributes and rip specs.
	attributes := buildAttributes(logger, r.ScanResult, r.DiscSources, r.DiscNumber)
	discNumber := r.DiscNumber
	if discNumber == 0 {
		if n, ok := extractDiscNumber(r.DiscSources...); ok {
			discNumber = n
		}
	}
	titleSpecs, episodeSpecs := buildRipSpecs(logger, r.ScanResult, r.EpisodeMatches,
		r.IdentifiedTitle, r.FallbackTitle, discNumber, metadata)

	// Encode envelope and validate.
	if err := i.storeAndValidateEnvelope(ctx, item, ripspec.Envelope{
		Fingerprint: strings.TrimSpace(item.DiscFingerprint),
		ContentKey:  contentKey,
		Metadata:    metadata,
		Attributes:  attributes,
		Titles:      titleSpecs,
		Episodes:    episodeSpecs,
	}); err != nil {
		return err
	}

	// Log primary title decision.
	if selection, ok, candidates, rejects := rippingPrimaryTitleSummary(titleSpecs); ok {
		attrs := []logging.Attr{
			logging.String(logging.FieldDecisionType, "primary_title"),
			logging.String("decision_result", "selected"),
			logging.String("decision_selected", fmt.Sprintf("%d:%ds", selection.ID, selection.Duration)),
			logging.Int("candidate_count", len(candidates)),
			logging.Int("rejected_count", len(rejects)),
			logging.Int("title_id", selection.ID),
			logging.Int("duration_seconds", selection.Duration),
			logging.Int("chapters", selection.Chapters),
			logging.String("playlist", strings.TrimSpace(selection.Playlist)),
			logging.Int("segment_count", selection.SegmentCount),
		}
		for idx, candidate := range candidates {
			key := fmt.Sprintf("candidate_%d", idx+1)
			if id, ok := logging.ParseDecisionID(candidate); ok {
				key = fmt.Sprintf("candidate_%d", id)
			}
			attrs = append(attrs, logging.String(key, candidate))
		}
		for idx, reject := range rejects {
			key := fmt.Sprintf("rejected_%d", idx+1)
			if id, ok := logging.ParseDecisionID(reject); ok {
				key = fmt.Sprintf("rejected_%d", id)
			}
			attrs = append(attrs, logging.String(key, reject))
		}
		logger.Info("primary title decision", logging.Args(attrs...)...)
	}

	// Send notification.
	if i.notifier != nil && r.Year != "" {
		payload := notifications.Payload{
			"title":        r.IdentifiedTitle,
			"year":         r.Year,
			"mediaType":    r.MediaType,
			"displayTitle": titleWithYear,
		}
		if r.Cached {
			payload["cached"] = true
		}
		if err := i.notifier.Publish(ctx, notifications.EventIdentificationCompleted, payload); err != nil {
			logger.Debug("identification notification failed", logging.Error(err))
		}
	}

	return nil
}
