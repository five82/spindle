package opensubtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/five82/spindle/internal/logs"
)

// Client communicates with the OpenSubtitles API.
type Client struct {
	apiKey    string
	userAgent string
	userToken string
	baseURL   string
	logger    *slog.Logger
	client    *http.Client
	lastCall  time.Time
	rateDelay time.Duration
}

// New creates an OpenSubtitles client. Returns nil if apiKey is empty.
func New(apiKey, userAgent, userToken, baseURL string, logger *slog.Logger) *Client {
	if apiKey == "" {
		return nil
	}
	if baseURL == "" {
		baseURL = "https://api.opensubtitles.com/api/v1"
	}
	if userAgent == "" {
		userAgent = "Spindle/dev v0.1.0"
	}
	logger = logs.Default(logger)
	return &Client{
		apiKey:    apiKey,
		userAgent: userAgent,
		userToken: userToken,
		baseURL:   baseURL,
		logger:    logger,
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
	c.logger.Info("OpenSubtitles search started",
		"event_type", "opensubtitles_search_start",
		"tmdb_id", tmdbID,
	)

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
	c.logger.Info("OpenSubtitles search completed",
		"event_type", "opensubtitles_search_complete",
		"tmdb_id", tmdbID,
		"results", len(resp.Data),
	)
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
	c.logger.Debug("OpenSubtitles download negotiated", "file_id", fileID, "remaining", resp.Remaining)
	return &resp, nil
}

// DownloadToFile downloads a subtitle and saves it to destPath.
func (c *Client) DownloadToFile(ctx context.Context, fileID int, destPath string) error {
	c.logger.Info("downloading subtitle file",
		"event_type", "opensubtitles_download_start",
		"file_id", fileID,
		"dest", destPath,
	)

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
	if err := f.Close(); err != nil {
		return fmt.Errorf("opensubtitles fetch: close: %w", err)
	}

	c.logger.Info("subtitle file downloaded",
		"event_type", "opensubtitles_download_complete",
		"file_id", fileID,
		"dest", destPath,
	)
	return nil
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
		c.logger.Debug("OpenSubtitles rate limit sleep", "sleep_ms", (c.rateDelay - elapsed).Milliseconds())
		time.Sleep(c.rateDelay - elapsed)
	}
	c.lastCall = time.Now()
}

// doGet performs an authenticated HTTP GET request with retry.
func (c *Client) doGet(ctx context.Context, path string, params url.Values) ([]byte, error) {
	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	return c.doWithRetry(ctx, func() ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		c.setHeaders(req)
		return c.executeRequest(req)
	})
}

// doPost performs an authenticated HTTP POST request with a JSON body and retry.
func (c *Client) doPost(ctx context.Context, path string, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	return c.doWithRetry(ctx, func() ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		c.setHeaders(req)
		return c.executeRequest(req)
	})
}

// executeRequest performs a single HTTP request and returns the response body.
func (c *Client) executeRequest(req *http.Request) ([]byte, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return nil, &statusError{code: resp.StatusCode}
	}

	return io.ReadAll(resp.Body)
}

// statusError records an HTTP status code for retry classification.
type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("status %d", e.code)
}

// doWithRetry executes fn with fixed-delay retry on transient errors.
// Retries up to 3 times with a 5-second wait between attempts.
// Retriable: status 429, 502, 503, 504, timeouts, and connection errors.
func (c *Client) doWithRetry(ctx context.Context, fn func() ([]byte, error)) ([]byte, error) {
	const maxRetries = 3
	const retryDelay = 5 * time.Second

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			c.logger.Warn("retrying OpenSubtitles request",
				"event_type", "opensubtitles_retry",
				"error_hint", fmt.Sprintf("attempt %d/%d", attempt, maxRetries),
				"impact", "delayed response",
				"error", lastErr.Error(),
			)
			timer := time.NewTimer(retryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}

		result, err := fn()
		if err == nil {
			return result, nil
		}

		lastErr = err
		if !isRetryable(err) {
			c.logger.Warn("OpenSubtitles request failed (non-retryable)",
				"event_type", "opensubtitles_request_failed",
				"error_hint", "status not retryable",
				"impact", "request abandoned",
				"error", err.Error(),
			)
			return nil, err
		}
	}

	return nil, fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}

// isRetryable returns true if the error is transient and worth retrying.
func isRetryable(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		switch se.code {
		case 429, 502, 503, 504:
			return true
		}
		return false
	}
	// Retry on timeouts and connection errors.
	return os.IsTimeout(err) || errors.Is(err, context.DeadlineExceeded)
}

// setHeaders adds the standard authentication and content negotiation headers.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "*/*")
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
