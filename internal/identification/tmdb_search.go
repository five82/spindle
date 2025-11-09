package identification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"spindle/internal/identification/tmdb"
)

// TMDBSearcher defines the subset of TMDB client functionality used by the identifier.
type TMDBSearcher interface {
	SearchMovieWithOptions(ctx context.Context, query string, opts tmdb.SearchOptions) (*tmdb.Response, error)
	SearchTVWithOptions(ctx context.Context, query string, opts tmdb.SearchOptions) (*tmdb.Response, error)
	SearchMultiWithOptions(ctx context.Context, query string, opts tmdb.SearchOptions) (*tmdb.Response, error)
	GetSeasonDetails(ctx context.Context, showID int64, seasonNumber int) (*tmdb.SeasonDetails, error)
}

type searchMode string

const (
	searchModeMovie searchMode = "movie"
	searchModeTV    searchMode = "tv"
	searchModeMulti searchMode = "multi"
)

type tmdbCacheEntry struct {
	resp    *tmdb.Response
	expires time.Time
}

type tmdbSearch struct {
	client     TMDBSearcher
	cache      map[string]tmdbCacheEntry
	cacheTTL   time.Duration
	rateLimit  time.Duration
	mu         sync.Mutex
	lastLookup time.Time
}

func newTMDBSearch(client TMDBSearcher) *tmdbSearch {
	if client == nil {
		return &tmdbSearch{}
	}
	return &tmdbSearch{
		client:     client,
		cache:      make(map[string]tmdbCacheEntry),
		cacheTTL:   10 * time.Minute,
		rateLimit:  250 * time.Millisecond,
		lastLookup: time.Unix(0, 0),
	}
}

func (s *tmdbSearch) search(ctx context.Context, title string, opts tmdb.SearchOptions, mode searchMode) (*tmdb.Response, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("tmdb client unavailable")
	}

	key := fmt.Sprintf("%s|%s|%s", mode, strings.ToLower(strings.TrimSpace(title)), opts.CacheKey())
	now := time.Now()

	s.mu.Lock()
	if entry, ok := s.cache[key]; ok && now.Before(entry.expires) {
		resp := entry.resp
		s.mu.Unlock()
		return resp, nil
	}

	wait := s.rateLimit - now.Sub(s.lastLookup)
	if wait > 0 {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		s.mu.Lock()
	}
	s.lastLookup = time.Now()
	s.mu.Unlock()

	var (
		resp *tmdb.Response
		err  error
	)
	switch mode {
	case searchModeTV:
		resp, err = s.client.SearchTVWithOptions(ctx, title, opts)
	case searchModeMulti:
		resp, err = s.client.SearchMultiWithOptions(ctx, title, opts)
	default:
		resp, err = s.client.SearchMovieWithOptions(ctx, title, opts)
	}
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.cache != nil {
		s.cache[key] = tmdbCacheEntry{resp: resp, expires: time.Now().Add(s.cacheTTL)}
	}
	s.mu.Unlock()
	return resp, nil
}
