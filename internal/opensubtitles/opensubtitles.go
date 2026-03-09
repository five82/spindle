package opensubtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Client communicates with the OpenSubtitles API.
type Client struct {
	apiKey    string
	userAgent string
	userToken string
	baseURL   string
	client    *http.Client
	lastCall  time.Time
	rateDelay time.Duration
}

// New creates an OpenSubtitles client. Returns nil if apiKey is empty.
func New(apiKey, userAgent, userToken, baseURL string) *Client {
	if apiKey == "" {
		return nil
	}
	if baseURL == "" {
		baseURL = "https://api.opensubtitles.com/api/v1"
	}
	if userAgent == "" {
		userAgent = "Spindle/dev"
	}
	return &Client{
		apiKey:    apiKey,
		userAgent: userAgent,
		userToken: userToken,
		baseURL:   baseURL,
		client:    &http.Client{Timeout: 45 * time.Second},
		rateDelay: 3 * time.Second,
	}
}

// SubtitleResult represents a search result.
type SubtitleResult struct {
	ID         string             `json:"id"`
	Attributes SubtitleAttributes `json:"attributes"`
}

// SubtitleAttributes contains metadata about a subtitle.
type SubtitleAttributes struct {
	Language         string         `json:"language"`
	DownloadCount    int            `json:"download_count"`
	ForeignPartsOnly bool           `json:"foreign_parts_only"`
	HearingImpaired  bool           `json:"hearing_impaired"`
	Files            []SubtitleFile `json:"files"`
}

// SubtitleFile represents a downloadable file within a subtitle result.
type SubtitleFile struct {
	FileID   int    `json:"file_id"`
	FileName string `json:"file_name"`
}

// DownloadResponse is the response from the download endpoint.
type DownloadResponse struct {
	Link      string `json:"link"`
	Remaining int    `json:"remaining"`
}

type searchResponse struct {
	Data []SubtitleResult `json:"data"`
}

// Search queries for subtitles by TMDB ID, season/episode, and languages.
func (c *Client) Search(ctx context.Context, tmdbID int, season, episode int, languages []string) ([]SubtitleResult, error) {
	if c == nil {
		return nil, fmt.Errorf("opensubtitles: client not configured")
	}
	c.rateLimit()

	params := url.Values{}
	params.Set("tmdb_id", fmt.Sprintf("%d", tmdbID))
	if season > 0 {
		params.Set("season_number", fmt.Sprintf("%d", season))
	}
	if episode > 0 {
		params.Set("episode_number", fmt.Sprintf("%d", episode))
	}
	if len(languages) > 0 {
		params.Set("languages", strings.Join(languages, ","))
	}

	body, err := c.doGet(ctx, "/subtitles", params)
	if err != nil {
		return nil, fmt.Errorf("opensubtitles search: %w", err)
	}

	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("opensubtitles search: decode: %w", err)
	}
	return resp.Data, nil
}

// Download negotiates a subtitle download and returns the download link.
func (c *Client) Download(ctx context.Context, fileID int) (*DownloadResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("opensubtitles: client not configured")
	}
	c.rateLimit()

	payload := struct {
		FileID    int    `json:"file_id"`
		SubFormat string `json:"sub_format"`
	}{
		FileID:    fileID,
		SubFormat: "srt",
	}

	body, err := c.doPost(ctx, "/download", payload)
	if err != nil {
		return nil, fmt.Errorf("opensubtitles download: %w", err)
	}

	var resp DownloadResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("opensubtitles download: decode: %w", err)
	}
	return &resp, nil
}

// DownloadToFile downloads a subtitle and saves it to destPath.
func (c *Client) DownloadToFile(ctx context.Context, fileID int, destPath string) error {
	dlResp, err := c.Download(ctx, fileID)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlResp.Link, nil)
	if err != nil {
		return fmt.Errorf("opensubtitles fetch: create request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("opensubtitles fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("opensubtitles fetch: status %d", resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("opensubtitles fetch: create dir: %w", err)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("opensubtitles fetch: create file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("opensubtitles fetch: write: %w", err)
	}
	return f.Close()
}

// CheckHealth verifies connectivity by hitting the /infos/formats endpoint.
func (c *Client) CheckHealth(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("opensubtitles: client not configured")
	}
	body, err := c.doGet(ctx, "/infos/formats", nil)
	if err != nil {
		return fmt.Errorf("opensubtitles health: %w", err)
	}
	_ = body
	return nil
}

// rateLimit sleeps if needed to maintain the minimum delay between API calls.
func (c *Client) rateLimit() {
	if c.lastCall.IsZero() {
		c.lastCall = time.Now()
		return
	}
	elapsed := time.Since(c.lastCall)
	if elapsed < c.rateDelay {
		time.Sleep(c.rateDelay - elapsed)
	}
	c.lastCall = time.Now()
}

// doGet performs an authenticated HTTP GET request.
func (c *Client) doGet(ctx context.Context, path string, params url.Values) ([]byte, error) {
	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// doPost performs an authenticated HTTP POST request with a JSON body.
func (c *Client) doPost(ctx context.Context, path string, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// setHeaders adds the standard authentication and content-type headers.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Content-Type", "application/json")
	if c.userToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.userToken)
	}
}

var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// CleanSRT removes HTML tags, normalizes line endings, and trims empty cues.
func CleanSRT(content string) string {
	// Normalize line endings to \n.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	// Remove HTML tags.
	content = htmlTagRe.ReplaceAllString(content, "")

	// Trim empty cues: collapse runs of 3+ newlines into 2.
	for strings.Contains(content, "\n\n\n") {
		content = strings.ReplaceAll(content, "\n\n\n", "\n\n")
	}

	return strings.TrimSpace(content) + "\n"
}
