package jellyfin

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Client interacts with the Jellyfin API.
type Client struct {
	url    string
	apiKey string
	client *http.Client
	logger *slog.Logger
}

// New creates a Jellyfin client. Returns nil if url or apiKey is empty.
func New(url, apiKey string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	if url == "" || apiKey == "" {
		return nil
	}
	return &Client{
		url:    url,
		apiKey: apiKey,
		client: &http.Client{Timeout: 30 * time.Second},
		logger: logger,
	}
}

// Refresh triggers a Jellyfin library refresh.
// Returns nil if client is nil (Jellyfin disabled).
func (c *Client) Refresh(ctx context.Context) error {
	if c == nil {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/Library/Refresh", nil)
	if err != nil {
		return fmt.Errorf("jellyfin refresh: create request: %w", err)
	}
	req.Header.Set("X-Emby-Token", c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("jellyfin refresh: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("jellyfin refresh: status %d", resp.StatusCode)
	}
	c.logger.Info("Jellyfin library refresh triggered", "event_type", "jellyfin_refresh")
	return nil
}

// CheckHealth verifies connectivity by hitting the /Users endpoint.
func (c *Client) CheckHealth(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("jellyfin: client not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/Users", nil)
	if err != nil {
		return fmt.Errorf("jellyfin health: create request: %w", err)
	}
	req.Header.Set("X-Emby-Token", c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("jellyfin health: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("jellyfin health: status %d", resp.StatusCode)
	}
	return nil
}
