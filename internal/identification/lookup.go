package identification

import (
	"context"
	"errors"
	"strings"

	"log/slog"

	"spindle/internal/identification/tmdb"
	"spindle/internal/logging"
)

// LookupMatch captures high-signal metadata from a TMDB lookup.
type LookupMatch struct {
	TMDBID      int64
	Title       string
	MediaType   string
	ReleaseDate string
	Year        string
}

// LookupTMDBByTitle searches TMDB for the provided title and returns the best match, if any.
func LookupTMDBByTitle(ctx context.Context, client *tmdb.Client, logger *slog.Logger, title string, opts tmdb.SearchOptions) (*LookupMatch, error) {
	if client == nil {
		return nil, errors.New("tmdb client is nil")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, nil
	}

	response, err := client.SearchMovieWithOptions(ctx, title, opts)
	if err != nil {
		return nil, err
	}

	scoreLogger := logger
	if scoreLogger == nil {
		scoreLogger = logging.NewNop()
	}

	best := selectBestResult(scoreLogger, title, response)
	if best == nil {
		return nil, nil
	}

	match := &LookupMatch{
		TMDBID:      best.ID,
		Title:       strings.TrimSpace(pickTitle(*best)),
		MediaType:   strings.ToLower(strings.TrimSpace(best.MediaType)),
		ReleaseDate: strings.TrimSpace(best.ReleaseDate),
	}
	if len(match.ReleaseDate) >= 4 {
		match.Year = match.ReleaseDate[:4]
	}

	if match.MediaType == "" {
		match.MediaType = "movie"
	}

	return match, nil
}
