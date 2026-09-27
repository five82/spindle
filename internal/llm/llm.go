package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/logs"
)

const defaultModel = "deepseek/deepseek-v4.1-flash"

// Client sends chat completions and typed decisions through OpenRouter.
type Client struct {
	apiKey  string
	baseURL string
	model   string
	referer string
	title   string
	client  *http.Client
	logger  *slog.Logger
}

// New creates an LLM client from the configured LLM section. Returns nil if
// APIKey is empty.
func New(cfg config.LLMConfig, logger *slog.Logger) *Client {
	if cfg.APIKey == "" {
		return nil
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1/chat/completions"
	}
	model := cfg.Model
	if model == "" {
		model = defaultModel
	}
	logger = logs.Default(logger)
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Client{
		apiKey:  cfg.APIKey,
		baseURL: baseURL,
		model:   model,
		referer: cfg.Referer,
		title:   cfg.Title,
		client:  &http.Client{Timeout: timeout},
		logger:  logger,
	}
}

// CompleteJSON sends a chat completion request with system and user messages,
// then parses the response content as JSON into result.
// Returns an error if the client is nil (not configured).
func (c *Client) CompleteJSON(ctx context.Context, systemPrompt, userPrompt string, result any) error {
	if c == nil {
		return fmt.Errorf("llm client not configured")
	}

	request := map[string]any{
		"model":           c.model,
		"messages":        []map[string]string{{"role": "system", "content": systemPrompt}, {"role": "user", "content": userPrompt}},
		"response_format": map[string]string{"type": "json_object"},
		// Keep classification at low effort even when a preset selects the model.
		"reasoning": map[string]string{"effort": "low"},
	}
	return c.complete(ctx, c.baseURL, c.model, request, func(body []byte) error {
		var resp struct {
			Choices []struct{ Message struct{ Content string } }
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return fmt.Errorf("unmarshal chat response: %w", err)
		}
		if len(resp.Choices) == 0 {
			return fmt.Errorf("no choices in response")
		}
		if err := json.Unmarshal([]byte(sanitizeJSON(resp.Choices[0].Message.Content)), result); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
		return nil
	})
}

// complete shares transport, retries and request logging across API surfaces.
func (c *Client) complete(ctx context.Context, endpoint, model string, request any, decode func([]byte) error) error {
	bodyBytes, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	const maxAttempts = 5
	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second}

	start := time.Now()
	c.logger.Info("LLM request started",
		"event_type", "llm_request_start",
		"model", model,
	)

	var lastErr error
	for attempt := range maxAttempts {
		attemptStart := time.Now()
		body, err := c.doRequest(ctx, endpoint, bodyBytes)
		if err == nil {
			err = decode(body)
		}
		if err == nil {
			c.logger.Info("LLM request completed",
				"event_type", "llm_request_complete",
				"model", model,
				"attempt", attempt+1,
				"attempt_duration_ms", time.Since(attemptStart).Milliseconds(),
				"duration_ms", time.Since(start).Milliseconds(),
			)
			return nil
		}

		lastErr = err

		// Only retry on retryable errors.
		if !isRetryable(err) {
			c.logger.Warn("LLM request failed (non-retryable)",
				"event_type", "llm_request_failed",
				"error_hint", "non-retryable error",
				"impact", "request abandoned",
				"error", err.Error(),
			)
			return err
		}

		c.logger.Warn("retrying LLM request",
			"event_type", "llm_retry",
			"error_hint", fmt.Sprintf("attempt %d/%d", attempt+1, maxAttempts),
			"impact", "delayed response",
			"error", err.Error(),
		)

		// Don't sleep after the last attempt.
		if attempt < maxAttempts-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delays[attempt]):
			}
		}
	}

	return fmt.Errorf("after %d attempts: %w", maxAttempts, lastErr)
}

// retryableError wraps an error with a retryable flag.
type retryableError struct {
	err error
}

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

func isRetryable(err error) bool {
	_, ok := err.(*retryableError)
	return ok
}

// doRequest performs a single HTTP request and returns the response content.
func (c *Client) doRequest(ctx context.Context, endpoint string, bodyBytes []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	if c.referer != "" {
		req.Header.Set("HTTP-Referer", c.referer)
	}
	if c.title != "" {
		req.Header.Set("X-Title", c.title)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		httpErr := fmt.Errorf("http %d: %s", resp.StatusCode, string(respBody))
		if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, &retryableError{err: httpErr}
		}
		return nil, httpErr
	}
	return respBody, nil
}

// sanitizeJSON strips markdown code fences and surrounding whitespace from s.
func sanitizeJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	return s
}
