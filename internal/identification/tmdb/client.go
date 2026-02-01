package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Result represents a single TMDB search match.
type Result struct {
	ID           int64   `json:"id"`
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	Overview     string  `json:"overview"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	MediaType    string  `json:"media_type"`
	Popularity   float64 `json:"popularity"`
	VoteAverage  float64 `json:"vote_average"`
	VoteCount    int64   `json:"vote_count"`
}

// Response models the TMDB paginated search response.
type Response struct {
	Page         int      `json:"page"`
	Results      []Result `json:"results"`
	TotalPages   int      `json:"total_pages"`
	TotalResults int      `json:"total_results"`
}

// Episode describes a single TMDB episode entry.
type Episode struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Overview      string `json:"overview"`
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
	Runtime       int    `json:"runtime"`
	AirDate       string `json:"air_date"`
}

// SeasonDetails captures the full TMDB season payload (episodes included).
type SeasonDetails struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	SeasonNumber int       `json:"season_number"`
	Episodes     []Episode `json:"episodes"`
}

// Searcher defines the TMDB search operations used by identification.
type Searcher interface {
	SearchMovieWithOptions(ctx context.Context, query string, opts SearchOptions) (*Response, error)
	SearchTVWithOptions(ctx context.Context, query string, opts SearchOptions) (*Response, error)
	SearchMultiWithOptions(ctx context.Context, query string, opts SearchOptions) (*Response, error)
	GetSeasonDetails(ctx context.Context, showID int64, seasonNumber int) (*SeasonDetails, error)
	GetMovieDetails(ctx context.Context, movieID int64) (*Result, error)
	GetTVDetails(ctx context.Context, showID int64) (*Result, error)
}

// Client provides access to the TMDB API for searches.
type Client struct {
	apiKey     string
	baseURL    string
	language   string
	httpClient *http.Client
}

var _ Searcher = (*Client)(nil)

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// New creates a TMDB client.
func New(apiKey, baseURL, language string, opts ...Option) (*Client, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("tmdb api key required")
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, errors.New("tmdb base url required")
	}
	language = strings.TrimSpace(language)
	client := &Client{
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		language:   language,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(client)
	}
	return client, nil
}

// SearchMovie searches TMDB for the supplied title.
func (c *Client) SearchMovie(ctx context.Context, query string) (*Response, error) {
	return c.SearchMovieWithOptions(ctx, query, SearchOptions{})
}

// SearchOptions contains optional parameters for TMDB movie search.
type SearchOptions struct {
	Year    int    `json:"year,omitempty"`
	Studio  string `json:"studio,omitempty"`
	Runtime int    `json:"runtime,omitempty"` // in minutes
}

// CacheKey returns a stable string representation for caching.
func (c SearchOptions) CacheKey() string {
	var builder strings.Builder
	builder.WriteString("y=")
	builder.WriteString(strconv.Itoa(c.Year))
	builder.WriteString("|r=")
	builder.WriteString(strconv.Itoa(c.Runtime))
	builder.WriteString("|s=")
	builder.WriteString(strings.ToLower(strings.TrimSpace(c.Studio)))
	return builder.String()
}

// SearchMovieWithOptions performs a TMDB movie search with optional filters.
func (c *Client) SearchMovieWithOptions(ctx context.Context, query string, opts SearchOptions) (*Response, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query must not be empty")
	}
	endpoint, err := url.Parse(c.baseURL + "/search/movie")
	if err != nil {
		return nil, fmt.Errorf("parse tmdb url: %w", err)
	}
	params := url.Values{}
	params.Set("query", query)
	params.Set("api_key", c.apiKey)
	if c.language != "" {
		params.Set("language", c.language)
	}

	// Add optional search parameters
	if opts.Year > 0 {
		params.Set("primary_release_year", strconv.Itoa(opts.Year))
	}
	// Note: Studio filtering is not yet implemented.
	// Future enhancement: TMDB uses company IDs, not names, so we would need
	// an additional API call to convert studio names to TMDB company IDs.
	if opts.Runtime > 0 {
		// Add runtime range filter (±10 minutes)
		params.Set("runtime.gte", strconv.Itoa(opts.Runtime-10))
		params.Set("runtime.lte", strconv.Itoa(opts.Runtime+10))
	}
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	requestStart := time.Now()
	resp, err := c.httpClient.Do(req)
	latency := time.Since(requestStart)
	if err != nil {
		return nil, fmt.Errorf("execute request (latency=%v): %w", latency, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb search returned %d (latency=%v)", resp.StatusCode, latency)
	}

	var payload Response
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode tmdb response: %w", err)
	}
	return &payload, nil
}

// SearchTVWithOptions performs a TMDB TV search with optional filters.
func (c *Client) SearchTVWithOptions(ctx context.Context, query string, opts SearchOptions) (*Response, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query must not be empty")
	}
	endpoint, err := url.Parse(c.baseURL + "/search/tv")
	if err != nil {
		return nil, fmt.Errorf("parse tmdb url: %w", err)
	}
	params := url.Values{}
	params.Set("query", query)
	params.Set("api_key", c.apiKey)
	if c.language != "" {
		params.Set("language", c.language)
	}
	if opts.Year > 0 {
		params.Set("first_air_date_year", strconv.Itoa(opts.Year))
	}
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	requestStart := time.Now()
	resp, err := c.httpClient.Do(req)
	latency := time.Since(requestStart)
	if err != nil {
		return nil, fmt.Errorf("execute request (latency=%v): %w", latency, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb tv search returned %d (latency=%v)", resp.StatusCode, latency)
	}

	var payload Response
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode tmdb response: %w", err)
	}
	return &payload, nil
}

// SearchMultiWithOptions performs a TMDB multi search, falling back to any media type.
func (c *Client) SearchMultiWithOptions(ctx context.Context, query string, opts SearchOptions) (*Response, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query must not be empty")
	}
	endpoint, err := url.Parse(c.baseURL + "/search/multi")
	if err != nil {
		return nil, fmt.Errorf("parse tmdb url: %w", err)
	}
	params := url.Values{}
	params.Set("query", query)
	params.Set("api_key", c.apiKey)
	if c.language != "" {
		params.Set("language", c.language)
	}
	if opts.Year > 0 {
		params.Set("year", strconv.Itoa(opts.Year))
	}
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	requestStart := time.Now()
	resp, err := c.httpClient.Do(req)
	latency := time.Since(requestStart)
	if err != nil {
		return nil, fmt.Errorf("execute request (latency=%v): %w", latency, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb multi search returned %d (latency=%v)", resp.StatusCode, latency)
	}

	var payload Response
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode tmdb response: %w", err)
	}
	return &payload, nil
}

// GetSeasonDetails fetches the full season metadata for a TV show, including episodes.
func (c *Client) GetSeasonDetails(ctx context.Context, showID int64, seasonNumber int) (*SeasonDetails, error) {
	if showID <= 0 {
		return nil, errors.New("show id must be positive")
	}
	if seasonNumber <= 0 {
		return nil, errors.New("season number must be positive")
	}
	endpoint, err := url.Parse(fmt.Sprintf("%s/tv/%d/season/%d", c.baseURL, showID, seasonNumber))
	if err != nil {
		return nil, fmt.Errorf("parse tmdb url: %w", err)
	}
	params := url.Values{}
	params.Set("api_key", c.apiKey)
	if c.language != "" {
		params.Set("language", c.language)
	}
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	requestStart := time.Now()
	resp, err := c.httpClient.Do(req)
	latency := time.Since(requestStart)
	if err != nil {
		return nil, fmt.Errorf("execute request (latency=%v): %w", latency, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb season fetch returned %d (latency=%v)", resp.StatusCode, latency)
	}

	var payload SeasonDetails
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode season response: %w", err)
	}
	return &payload, nil
}

// GetMovieDetails fetches movie details by TMDB ID.
func (c *Client) GetMovieDetails(ctx context.Context, movieID int64) (*Result, error) {
	if movieID <= 0 {
		return nil, errors.New("movie id must be positive")
	}
	endpoint, err := url.Parse(fmt.Sprintf("%s/movie/%d", c.baseURL, movieID))
	if err != nil {
		return nil, fmt.Errorf("parse tmdb url: %w", err)
	}
	params := url.Values{}
	params.Set("api_key", c.apiKey)
	if c.language != "" {
		params.Set("language", c.language)
	}
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	requestStart := time.Now()
	resp, err := c.httpClient.Do(req)
	latency := time.Since(requestStart)
	if err != nil {
		return nil, fmt.Errorf("execute request (latency=%v): %w", latency, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb movie details returned %d (latency=%v)", resp.StatusCode, latency)
	}

	var payload Result
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode movie details: %w", err)
	}
	payload.MediaType = "movie"
	return &payload, nil
}

// GetTVDetails fetches TV show details by TMDB ID.
func (c *Client) GetTVDetails(ctx context.Context, showID int64) (*Result, error) {
	if showID <= 0 {
		return nil, errors.New("show id must be positive")
	}
	endpoint, err := url.Parse(fmt.Sprintf("%s/tv/%d", c.baseURL, showID))
	if err != nil {
		return nil, fmt.Errorf("parse tmdb url: %w", err)
	}
	params := url.Values{}
	params.Set("api_key", c.apiKey)
	if c.language != "" {
		params.Set("language", c.language)
	}
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	requestStart := time.Now()
	resp, err := c.httpClient.Do(req)
	latency := time.Since(requestStart)
	if err != nil {
		return nil, fmt.Errorf("execute request (latency=%v): %w", latency, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb tv details returned %d (latency=%v)", resp.StatusCode, latency)
	}

	var payload Result
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode tv details: %w", err)
	}
	payload.MediaType = "tv"
	return &payload, nil
}
