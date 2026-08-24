package loom

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/five82/spindle/internal/logs"
)

// Client interacts with the Loom API.
type Client struct {
	url    string
	client *http.Client
	logger *slog.Logger
}

// New creates a Loom client. It returns nil when url is empty.
func New(url string, logger *slog.Logger) *Client {
	logger = logs.Default(logger)
	if url == "" {
		logger.Info("loom integration disabled",
			"decision_type", logs.DecisionIntegrationConfig,
			"decision_result", "disabled",
			"decision_reason", "loom url not configured",
		)
		return nil
	}
	return &Client{
		url: strings.TrimRight(url, "/"),
		client: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		logger: logger,
	}
}

// Scan triggers a Loom scan of every library. It returns nil when Loom is disabled.
func (c *Client) Scan(ctx context.Context) error {
	if c == nil {
		return nil
	}
	start := time.Now()
	c.logger.Info("Loom library scan started", "event_type", "loom_scan_start")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/api/v1/scan", nil)
	if err != nil {
		return fmt.Errorf("loom scan: create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("loom scan: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("loom scan: status %d", resp.StatusCode)
	}
	c.logger.Info("Loom library scan triggered",
		"event_type", "loom_scan",
		"status", resp.StatusCode,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}
