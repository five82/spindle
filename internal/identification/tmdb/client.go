package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

// Client provides access to the TMDB API for searches.
type Client struct {
	apiKey     string
	baseURL    string
	language   string
	httpClient *http.Client
}

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
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb search returned %d", resp.StatusCode)
	}

	var payload Response
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode tmdb response: %w", err)
	}
	return &payload, nil
}
