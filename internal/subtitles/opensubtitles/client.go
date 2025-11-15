package opensubtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL     = "https://api.opensubtitles.com/api/v1"
	defaultUserAgent   = "Spindle/dev"
	defaultHTTPTimeout = 45 * time.Second
)

// Config describes the OpenSubtitles client configuration.
type Config struct {
	APIKey     string
	UserAgent  string
	UserToken  string
	BaseURL    string
	HTTPClient *http.Client
}

// Client wraps the OpenSubtitles REST API.
type Client struct {
	apiKey    string
	userAgent string
	userToken string
	baseURL   *url.URL
	http      *http.Client
}

// New creates a Client from the supplied configuration.
func New(cfg Config) (*Client, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("opensubtitles: api key is required")
	}
	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = defaultBaseURL
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("opensubtitles: parse base url: %w", err)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &Client{
		apiKey:    apiKey,
		userAgent: userAgent,
		userToken: strings.TrimSpace(cfg.UserToken),
		baseURL:   baseURL,
		http:      client,
	}, nil
}

// SearchRequest describes subtitle discovery filters.
type SearchRequest struct {
	TMDBID          int64
	ParentTMDBID    int64
	IMDBID          string
	Query           string
	Languages       []string
	Season          int
	Episode         int
	MediaType       string
	Year            string
	HearingImpaired bool
}

// Subtitle represents a subtitle candidate returned by OpenSubtitles.
type Subtitle struct {
	ID              string
	FileID          int64
	Language        string
	Release         string
	FeatureTitle    string
	FeatureYear     int
	FeatureType     string
	Downloads       int
	HearingImpaired bool
	HD              bool
	AITranslated    bool
}

// SearchResponse bundles the subtitles returned by a query.
type SearchResponse struct {
	Subtitles []Subtitle
	Total     int
}

// DownloadOptions controls subtitle downloads.
type DownloadOptions struct {
	Format string
}

// DownloadResult captures the downloaded subtitle payload.
type DownloadResult struct {
	Data        []byte
	FileName    string
	Language    string
	DownloadURL string
}

// Search queries the OpenSubtitles API for matching subtitles.
func (c *Client) Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	if c == nil {
		return SearchResponse{}, errors.New("opensubtitles: client is nil")
	}
	endpoint := c.baseURL.JoinPath("subtitles")
	params := url.Values{}
	if req.TMDBID > 0 {
		params.Set("tmdb_id", strconv.FormatInt(req.TMDBID, 10))
	}
	if req.ParentTMDBID > 0 {
		params.Set("parent_tmdb_id", strconv.FormatInt(req.ParentTMDBID, 10))
	}
	if imdb := sanitizeIMDBID(req.IMDBID); imdb != "" {
		params.Set("imdb_id", imdb)
	}
	if req.Query != "" {
		params.Set("query", req.Query)
	}
	if len(req.Languages) > 0 {
		params.Set("languages", strings.Join(req.Languages, ","))
	}
	if req.Season > 0 {
		params.Set("season_number", strconv.Itoa(req.Season))
	}
	if req.Episode > 0 {
		params.Set("episode_number", strconv.Itoa(req.Episode))
	}
	if req.MediaType != "" {
		params.Set("type", req.MediaType)
	}
	if req.Year != "" {
		params.Set("year", req.Year)
	}
	if req.HearingImpaired {
		params.Set("hearing_impaired", "true")
	}
	if params.Get("type") == "" {
		if req.Season > 0 || req.Episode > 0 {
			params.Set("type", "episode")
		} else if req.TMDBID > 0 || req.IMDBID != "" {
			params.Set("type", "movie")
		}
	}
	params.Set("order_by", "download_count")
	params.Set("order_direction", "desc")
	endpoint.RawQuery = params.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return SearchResponse{}, fmt.Errorf("opensubtitles: build search request: %w", err)
	}
	c.applyHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return SearchResponse{}, fmt.Errorf("opensubtitles: search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return SearchResponse{}, fmt.Errorf("opensubtitles: search failed (%s): %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return SearchResponse{}, fmt.Errorf("opensubtitles: decode search response: %w", err)
	}

	subtitles := make([]Subtitle, 0, len(payload.Data))
	for _, entry := range payload.Data {
		if entry.Attributes.Language == "" {
			continue
		}
		fileID := entry.Attributes.PrimaryFileID()
		if fileID == 0 {
			continue
		}
		subtitles = append(subtitles, Subtitle{
			ID:              entry.ID,
			FileID:          fileID,
			Language:        entry.Attributes.Language,
			Release:         entry.Attributes.Release,
			FeatureTitle:    entry.Attributes.FeatureDetails.Title,
			FeatureYear:     entry.Attributes.FeatureDetails.Year,
			FeatureType:     entry.Attributes.FeatureDetails.FeatureType,
			Downloads:       entry.Attributes.DownloadCount,
			HearingImpaired: entry.Attributes.HearingImpaired,
			HD:              entry.Attributes.HD,
			AITranslated:    entry.Attributes.AITranslated || entry.Attributes.MachineTranslated,
		})
	}

	return SearchResponse{
		Subtitles: subtitles,
		Total:     payload.Meta.Total,
	}, nil
}

// Download retrieves the subtitle contents for the specified subtitle file.
func (c *Client) Download(ctx context.Context, fileID int64, opts DownloadOptions) (DownloadResult, error) {
	if c == nil {
		return DownloadResult{}, errors.New("opensubtitles: client is nil")
	}
	if fileID <= 0 {
		return DownloadResult{}, errors.New("opensubtitles: invalid file id")
	}
	body := map[string]any{
		"file_id": fileID,
	}
	format := strings.TrimSpace(opts.Format)
	if format == "" {
		format = "srt"
	}
	body["sub_format"] = format

	payload, err := json.Marshal(body)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("opensubtitles: encode download request: %w", err)
	}

	endpoint := c.baseURL.JoinPath("download")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return DownloadResult{}, fmt.Errorf("opensubtitles: build download request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.applyHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("opensubtitles: download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return DownloadResult{}, fmt.Errorf("opensubtitles: download negotiation failed (%s): %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var info downloadResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return DownloadResult{}, fmt.Errorf("opensubtitles: decode download response: %w", err)
	}
	if info.Link == "" {
		return DownloadResult{}, errors.New("opensubtitles: download response missing link")
	}

	downloadURL, err := endpoint.Parse(info.Link)
	if err != nil {
		downloadURL, err = url.Parse(info.Link)
		if err != nil {
			return DownloadResult{}, fmt.Errorf("opensubtitles: parse download url: %w", err)
		}
	}

	dataReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL.String(), nil)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("opensubtitles: build link request: %w", err)
	}
	dataReq.Header.Set("User-Agent", c.userAgent)
	dataResp, err := c.http.Do(dataReq)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("opensubtitles: fetch subtitle payload: %w", err)
	}
	defer dataResp.Body.Close()

	if dataResp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(dataResp.Body, 4096))
		return DownloadResult{}, fmt.Errorf("opensubtitles: subtitle download failed (%s): %s", dataResp.Status, strings.TrimSpace(string(body)))
	}
	data, err := io.ReadAll(dataResp.Body)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("opensubtitles: read subtitle data: %w", err)
	}

	return DownloadResult{
		Data:        data,
		FileName:    info.FileName,
		Language:    info.Language,
		DownloadURL: downloadURL.String(),
	}, nil
}

func (c *Client) applyHeaders(req *http.Request) {
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	if c.userToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.userToken)
	}
}

func sanitizeIMDBID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "tt")
	if _, err := strconv.ParseInt(value, 10, 64); err != nil {
		return ""
	}
	return value
}

type searchResponse struct {
	Data []struct {
		ID         string           `json:"id"`
		Attributes searchAttributes `json:"attributes"`
	} `json:"data"`
	Meta struct {
		Total int `json:"total_count"`
	} `json:"meta"`
}

type searchAttributes struct {
	Language          string         `json:"language"`
	Release           string         `json:"release"`
	DownloadCount     int            `json:"download_count"`
	HearingImpaired   bool           `json:"hearing_impaired"`
	HD                bool           `json:"hd"`
	AITranslated      bool           `json:"ai_translated"`
	MachineTranslated bool           `json:"machine_translated"`
	FeatureDetails    featureDetails `json:"feature_details"`
	Files             []searchFile   `json:"files"`
}

func (a searchAttributes) PrimaryFileID() int64 {
	if len(a.Files) == 0 {
		return 0
	}
	return a.Files[0].FileID
}

type featureDetails struct {
	FeatureType string `json:"feature_type"`
	Title       string `json:"title"`
	Year        int    `json:"year"`
}

type searchFile struct {
	FileID int64 `json:"file_id"`
}

type downloadResponse struct {
	Link     string `json:"link"`
	FileName string `json:"file_name"`
	Language string `json:"language"`
}
