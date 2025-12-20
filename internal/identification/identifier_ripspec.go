package identification

import (
	"strings"

	"log/slog"

	"spindle/internal/disc"
	discfingerprint "spindle/internal/disc/fingerprint"
	"spindle/internal/logging"
	"spindle/internal/ripspec"
)

func buildRipSpecs(logger *slog.Logger, scanResult *disc.ScanResult, episodeMatches map[int]episodeAnnotation, identifiedTitle, fallbackTitle string, metadata map[string]any) ([]ripspec.Title, []ripspec.Episode) {
	if scanResult == nil {
		return nil, nil
	}
	episodeSpecs := make([]ripspec.Episode, 0, len(episodeMatches))
	titleSpecs := make([]ripspec.Title, 0, len(scanResult.Titles))
	for _, t := range scanResult.Titles {
		fp := discfingerprint.TitleFingerprint(t)
		spec := ripspec.Title{
			ID:                 t.ID,
			Name:               t.Name,
			Duration:           t.Duration,
			Chapters:           t.Chapters,
			Playlist:           t.Playlist,
			SegmentCount:       t.SegmentCount,
			SegmentMap:         t.SegmentMap,
			ContentFingerprint: fp,
		}
		if annotation, ok := episodeMatches[t.ID]; ok {
			spec.Season = annotation.Season
			spec.Episode = annotation.Episode
			spec.EpisodeTitle = annotation.Title
			spec.EpisodeAirDate = annotation.Air
			if annotation.Season > 0 && annotation.Episode > 0 {
				showLabel := identifiedTitle
				if strings.TrimSpace(showLabel) == "" {
					if value, ok := metadata["title"].(string); ok && strings.TrimSpace(value) != "" {
						showLabel = value
					} else {
						showLabel = fallbackTitle
					}
				}
				episodeSpecs = append(episodeSpecs, ripspec.Episode{
					Key:                ripspec.EpisodeKey(annotation.Season, annotation.Episode),
					TitleID:            t.ID,
					Season:             annotation.Season,
					Episode:            annotation.Episode,
					EpisodeTitle:       annotation.Title,
					EpisodeAirDate:     annotation.Air,
					RuntimeSeconds:     t.Duration,
					ContentFingerprint: fp,
					OutputBasename:     episodeOutputBasename(showLabel, annotation.Season, annotation.Episode),
				})
			}
		}
		titleSpecs = append(titleSpecs, spec)
		logFields := []any{
			logging.Int("title_id", t.ID),
			logging.Int("duration_seconds", t.Duration),
			logging.String("title_name", strings.TrimSpace(t.Name)),
			logging.String("content_fingerprint", truncateFingerprint(fp)),
		}
		if spec.Season > 0 && spec.Episode > 0 {
			logFields = append(logFields,
				logging.Int("season", spec.Season),
				logging.Int("episode", spec.Episode),
				logging.String("episode_title", strings.TrimSpace(spec.EpisodeTitle)))
		}
		logger.Info("prepared title fingerprint", logFields...)
	}
	return titleSpecs, episodeSpecs
}
