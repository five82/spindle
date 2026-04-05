package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/five82/spindle/internal/logs"
)

// Client communicates with the TMDB API.
type Client struct {
	apiKey   string
	baseURL  string
	language string
	client   *http.Client
	logger   *slog.Logger
}

// New creates a TMDB client.
func New(apiKey, baseURL, language string, logger *slog.Logger) *Client {
	if baseURL == "" {
		baseURL = "https://api.themoviedb.org/3"
	}
	if language == "" {
		language = "en-US"
	}
	logger = logs.Default(logger)
	return &Client{
		apiKey:   apiKey,
		baseURL:  baseURL,
		language: language,
		client:   &http.Client{Timeout: 15 * time.Second},
		logger:   logger,
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
	for i := range resp.Results {
		resp.Results[i].MediaType = "tv"
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

// Scoring and acceptance constants per spec (DESIGN_STAGES.md section 1.7).
const (
	voteAverageDivisor          = 10.0
	voteCountDivisor            = 1000.0
	exactMatchMinVoteAverage    = 2.0
	nonExactMatchMinVoteAverage = 3.0
	nonExactMatchBaseThreshold  = 1.3
)

// scoreResult computes the raw score for a single result against the query.
// Formula per spec: match(0/1) + voteAverage/10 + voteCount/1000.
func scoreResult(query string, r *SearchResult) float64 {
	titleLower := strings.ToLower(r.DisplayTitle())
	queryLower := strings.ToLower(query)
	match := 0.0
	if strings.Contains(titleLower, queryLower) || queryMatchesTitleAlias(query, r.DisplayTitle()) {
		match = 1.0
	}
	return match + (r.VoteAverage / voteAverageDivisor) + float64(r.VoteCount)/voteCountDivisor
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

func tokenizeForComparison(input string) []string {
	lower := strings.ToLower(input)
	var tokens []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tokens = append(tokens, b.String())
		b.Reset()
	}
	for _, r := range lower {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func acronym(tokens []string) string {
	var b strings.Builder
	for _, token := range tokens {
		for _, r := range token {
			b.WriteRune(r)
			break
		}
	}
	return b.String()
}

// queryMatchesTitleAlias reports whether the query tokens can be aligned to the
// title tokens, allowing a query token to match either an exact title token or
// the acronym of one or more consecutive title tokens. This lets queries like
// "Star Trek TNG" match titles like "Star Trek The Next Generation" while
// keeping the logic deterministic and title-based.
func queryMatchesTitleAlias(query, title string) bool {
	queryTokens := tokenizeForComparison(query)
	titleTokens := tokenizeForComparison(title)
	if len(queryTokens) == 0 || len(titleTokens) == 0 {
		return false
	}

	var match func(qi, ti int) bool
	match = func(qi, ti int) bool {
		if qi == len(queryTokens) {
			return ti == len(titleTokens)
		}
		if ti >= len(titleTokens) {
			return false
		}
		if queryTokens[qi] == titleTokens[ti] && match(qi+1, ti+1) {
			return true
		}
		for end := ti + 1; end <= len(titleTokens); end++ {
			if queryTokens[qi] == acronym(titleTokens[ti:end]) && match(qi+1, end) {
				return true
			}
		}
		return false
	}

	return match(0, 0)
}

// releaseYear returns the release year of a search result as an int, or 0.
func releaseYear(r *SearchResult) int {
	y, _ := strconv.Atoi(r.Year())
	return y
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
func SelectBestResult(results []SearchResult, query string, year, minVoteCountExact int, logger *slog.Logger) *SearchResult {
	if len(results) == 0 {
		return nil
	}

	queryNorm := normalizeForComparison(query)

	var best *SearchResult
	var bestScore float64
	var bestIsExact bool
	var bestExact *SearchResult
	var bestExactScore float64

	for i := range results {
		r := &results[i]
		score := scoreResult(query, r)
		titleNorm := normalizeForComparison(r.DisplayTitle())

		exactMatch := titleNorm == queryNorm
		yearMatch := true
		if exactMatch && year > 0 {
			yearMatch = releaseYear(r) == year
			exactMatch = yearMatch
		}

		logger.Debug("TMDB candidate scored",
			"decision_type", logs.DecisionTMDBSearch,
			"decision_result", r.DisplayTitle(),
			"score", score,
			"exact_match", exactMatch,
			"year_match", yearMatch,
			"vote_avg", r.VoteAverage,
			"vote_count", r.VoteCount,
		)

		if exactMatch && score > bestExactScore {
			bestExact = r
			bestExactScore = score
		}
		if score > bestScore {
			best = r
			bestScore = score
			bestIsExact = exactMatch
		}
	}

	if best == nil {
		return nil
	}

	// Prefer exact match over highest-scoring result if it meets thresholds.
	selected := best
	selectedScore := bestScore
	selectedExact := bestIsExact
	if bestExact != nil && bestExact.VoteAverage >= exactMatchMinVoteAverage &&
		bestExact.VoteCount >= minVoteCountExact {
		logger.Info("TMDB exact match preferred",
			"decision_type", logs.DecisionTMDBMatchPreference,
			"decision_result", "exact_preferred",
			"decision_reason", fmt.Sprintf("exact=%q non_exact=%q", bestExact.DisplayTitle(), best.DisplayTitle()),
		)
		selected = bestExact
		selectedScore = bestExactScore
		selectedExact = true
	}

	// Apply acceptance thresholds.
	if selectedExact {
		if selected.VoteAverage < exactMatchMinVoteAverage || selected.VoteCount < minVoteCountExact {
			logger.Info("TMDB match rejected",
				"decision_type", logs.DecisionTMDBMatch,
				"decision_result", "rejected",
				"decision_reason", fmt.Sprintf("exact match below thresholds: title=%q vote_avg=%.1f vote_count=%d min_vote_avg=%.1f min_vote_count=%d", selected.DisplayTitle(), selected.VoteAverage, selected.VoteCount, exactMatchMinVoteAverage, minVoteCountExact),
			)
			return nil
		}
	} else {
		if selected.VoteAverage < nonExactMatchMinVoteAverage {
			logger.Info("TMDB match rejected",
				"decision_type", logs.DecisionTMDBMatch,
				"decision_result", "rejected",
				"decision_reason", fmt.Sprintf("non-exact match vote_avg too low: title=%q vote_avg=%.1f min=%.1f", selected.DisplayTitle(), selected.VoteAverage, nonExactMatchMinVoteAverage),
			)
			return nil
		}
		if selectedScore < nonExactMatchBaseThreshold+float64(selected.VoteCount)/voteCountDivisor {
			logger.Info("TMDB match rejected",
				"decision_type", logs.DecisionTMDBMatch,
				"decision_result", "rejected",
				"decision_reason", fmt.Sprintf("non-exact match score below threshold: title=%q score=%.3f threshold=%.3f", selected.DisplayTitle(), selectedScore, nonExactMatchBaseThreshold+float64(selected.VoteCount)/voteCountDivisor),
			)
			return nil
		}
	}

	logger.Info("TMDB match selected",
		"decision_type", logs.DecisionTMDBMatch,
		"decision_result", "accepted",
		"decision_reason", fmt.Sprintf("title=%q score=%.3f exact=%v vote_avg=%.1f vote_count=%d", selected.DisplayTitle(), selectedScore, selectedExact, selected.VoteAverage, selected.VoteCount),
	)
	return selected
}
