package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Client communicates with the TMDB API.
type Client struct {
	apiKey   string
	baseURL  string
	language string
	client   *http.Client
}

// New creates a TMDB client.
func New(apiKey, baseURL, language string) *Client {
	if baseURL == "" {
		baseURL = "https://api.themoviedb.org/3"
	}
	if language == "" {
		language = "en-US"
	}
	return &Client{
		apiKey:   apiKey,
		baseURL:  baseURL,
		language: language,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

// SearchResult represents a single TMDB search result.
type SearchResult struct {
	ID            int     `json:"id"`
	Title         string  `json:"title"`           // movie
	Name          string  `json:"name"`            // TV
	Overview      string  `json:"overview"`
	ReleaseDate   string  `json:"release_date"`    // movie
	FirstAirDate  string  `json:"first_air_date"`  // TV
	MediaType     string  `json:"media_type"`      // from multi search
	VoteAverage   float64 `json:"vote_average"`
	VoteCount     int     `json:"vote_count"`
	OriginalTitle string  `json:"original_title"`
	OriginalName  string  `json:"original_name"`
}

// DisplayTitle returns the best title for display.
func (r SearchResult) DisplayTitle() string {
	if r.Title != "" {
		return r.Title
	}
	return r.Name
}

// Year extracts the year from ReleaseDate or FirstAirDate.
func (r SearchResult) Year() string {
	date := r.ReleaseDate
	if date == "" {
		date = r.FirstAirDate
	}
	if len(date) >= 4 {
		return date[:4]
	}
	return ""
}

// MovieDetail contains extended movie information.
type MovieDetail struct {
	ID          int     `json:"id"`
	Title       string  `json:"title"`
	Overview    string  `json:"overview"`
	ReleaseDate string  `json:"release_date"`
	IMDBID      string  `json:"imdb_id"`
	Runtime     int     `json:"runtime"`
	VoteAverage float64 `json:"vote_average"`
	VoteCount   int     `json:"vote_count"`
}

// TVDetail contains extended TV show information.
type TVDetail struct {
	ID              int          `json:"id"`
	Name            string       `json:"name"`
	Overview        string       `json:"overview"`
	FirstAirDate    string       `json:"first_air_date"`
	VoteAverage     float64      `json:"vote_average"`
	VoteCount       int          `json:"vote_count"`
	NumberOfSeasons int          `json:"number_of_seasons"`
	ExternalIDs     *ExternalIDs `json:"external_ids,omitempty"`
}

// ExternalIDs contains external service IDs.
type ExternalIDs struct {
	IMDBID string `json:"imdb_id"`
}

// Season contains TV season information.
type Season struct {
	SeasonNumber int       `json:"season_number"`
	Episodes     []Episode `json:"episodes"`
}

// Episode contains TV episode information.
type Episode struct {
	EpisodeNumber int     `json:"episode_number"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	AirDate       string  `json:"air_date"`
	Runtime       int     `json:"runtime"`
	VoteAverage   float64 `json:"vote_average"`
}

// searchResponse is the paginated TMDB search response.
type searchResponse struct {
	Results    []SearchResult `json:"results"`
	TotalPages int            `json:"total_pages"`
}

// get builds a URL, sets the Authorization Bearer header, makes the GET request,
// reads the body, and unmarshals into result.
func (c *Client) get(ctx context.Context, path string, params url.Values, result any) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("language", c.language)

	reqURL := c.baseURL + path + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("tmdb: creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("tmdb: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("tmdb: reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tmdb: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("tmdb: decoding response: %w", err)
	}
	return nil
}

// SearchMovie searches for movies by title with an optional year filter.
func (c *Client) SearchMovie(ctx context.Context, query, year string) ([]SearchResult, error) {
	params := url.Values{}
	params.Set("query", query)
	if year != "" {
		params.Set("year", year)
	}

	var resp searchResponse
	if err := c.get(ctx, "/search/movie", params, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// SearchTV searches for TV shows by name with an optional year filter.
func (c *Client) SearchTV(ctx context.Context, query, year string) ([]SearchResult, error) {
	params := url.Values{}
	params.Set("query", query)
	if year != "" {
		params.Set("first_air_date_year", year)
	}

	var resp searchResponse
	if err := c.get(ctx, "/search/tv", params, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// SearchMulti searches across movies, TV shows, and people.
func (c *Client) SearchMulti(ctx context.Context, query string) ([]SearchResult, error) {
	params := url.Values{}
	params.Set("query", query)

	var resp searchResponse
	if err := c.get(ctx, "/search/multi", params, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// GetMovie retrieves extended movie information by ID.
func (c *Client) GetMovie(ctx context.Context, id int) (*MovieDetail, error) {
	var detail MovieDetail
	if err := c.get(ctx, fmt.Sprintf("/movie/%d", id), nil, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

// GetTV retrieves extended TV show information by ID, including external IDs.
func (c *Client) GetTV(ctx context.Context, id int) (*TVDetail, error) {
	params := url.Values{}
	params.Set("append_to_response", "external_ids")

	var detail TVDetail
	if err := c.get(ctx, fmt.Sprintf("/tv/%d", id), params, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

// GetSeason retrieves TV season information including episodes.
func (c *Client) GetSeason(ctx context.Context, tvID, season int) (*Season, error) {
	var s Season
	path := fmt.Sprintf("/tv/%d/season/%d", tvID, season)
	if err := c.get(ctx, path, nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// scoreResult computes the raw score for a single result against the query.
// Formula per spec: match(0/1) + voteAverage/10 + voteCount/1000.
func scoreResult(query string, r *SearchResult) float64 {
	titleLower := strings.ToLower(r.DisplayTitle())
	queryLower := strings.ToLower(query)
	match := 0.0
	if strings.Contains(titleLower, queryLower) {
		match = 1.0
	}
	return match + (r.VoteAverage / 10.0) + float64(r.VoteCount)/1000.0
}

// normalizeForComparison normalizes a string for title comparison: lowercase,
// replace &/+ with "and", strip non-alphanumeric.
func normalizeForComparison(input string) string {
	normalized := strings.ToLower(input)
	normalized = strings.ReplaceAll(normalized, "&", "and")
	normalized = strings.ReplaceAll(normalized, "+", "and")
	var b strings.Builder
	for _, r := range normalized {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// resultReleaseYear extracts the release year from a search result.
func resultReleaseYear(r *SearchResult) int {
	date := strings.TrimSpace(r.ReleaseDate)
	if date == "" {
		date = strings.TrimSpace(r.FirstAirDate)
	}
	if len(date) < 4 {
		return 0
	}
	year, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return year
}

// SelectBestResult scores each TMDB result and returns the best match, or nil
// if no result meets acceptance thresholds.
//
// Scoring formula: match(0/1) + voteAverage/10 + voteCount/1000.
//
// Acceptance paths:
//   - Exact match (normalized title equals query): voteAverage >= 2.0 AND voteCount >= minVoteCountExact.
//   - Non-exact: voteAverage >= 3.0 AND score >= 1.3 + voteCount/1000.
//
// Year-aware matching: when year > 0, an exact title match also requires the
// result's release year to equal the provided year. This disambiguates
// same-title films from different years.
//
// Preference: an exact match meeting its thresholds is preferred over a
// higher-scoring non-exact result.
func SelectBestResult(results []SearchResult, query string, year, minVoteCountExact int) *SearchResult {
	if len(results) == 0 {
		return nil
	}

	queryNorm := normalizeForComparison(query)

	var best *SearchResult
	var bestScore float64
	var bestExact *SearchResult
	var bestExactScore float64

	for i := range results {
		r := &results[i]
		score := scoreResult(query, r)
		titleNorm := normalizeForComparison(r.DisplayTitle())

		exactMatch := titleNorm == queryNorm
		if exactMatch && year > 0 {
			exactMatch = resultReleaseYear(r) == year
		}

		if exactMatch && score > bestExactScore {
			bestExact = r
			bestExactScore = score
		}
		if score > bestScore {
			best = r
			bestScore = score
		}
	}

	if best == nil {
		return nil
	}

	// Prefer exact match over highest-scoring result if it meets thresholds.
	selected := best
	if bestExact != nil && bestExact.VoteAverage >= 2.0 &&
		bestExact.VoteCount >= minVoteCountExact {
		selected = bestExact
	}

	// Apply acceptance thresholds.
	selectedNorm := normalizeForComparison(selected.DisplayTitle())
	isExact := selectedNorm == queryNorm
	if isExact && year > 0 {
		isExact = resultReleaseYear(selected) == year
	}

	if isExact {
		if selected.VoteAverage < 2.0 || selected.VoteCount < minVoteCountExact {
			return nil
		}
	} else {
		if selected.VoteAverage < 3.0 {
			return nil
		}
		selectedScore := scoreResult(query, selected)
		if selectedScore < 1.3+float64(selected.VoteCount)/1000.0 {
			return nil
		}
	}

	return selected
}
