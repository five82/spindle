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
)

// Client sends chat completion requests to an OpenRouter-compatible API.
type Client struct {
	apiKey  string
	baseURL string
	model   string
	referer string
	title   string
	timeout time.Duration
	client  *http.Client
	logger  *slog.Logger
}

// New creates an LLM client. Returns nil if apiKey is empty.
func New(apiKey, baseURL, model, referer, title string, timeoutSeconds int, logger *slog.Logger) *Client {
	if apiKey == "" {
		return nil
	}
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1/chat/completions"
	}
	if model == "" {
		model = "google/gemini-3-flash-preview"
	}
	if logger == nil {
		logger = slog.Default()
	}
	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		referer: referer,
		title:   title,
		timeout: timeout,
		client:  &http.Client{Timeout: timeout},
		logger:  logger,
	}
}

// chatRequest is the OpenAI-compatible chat completion request body.
type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

// responseFormat constrains the LLM response to a specific format.
type responseFormat struct {
	Type string `json:"type"`
}

// chatMessage is a single message in the chat completion request.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse is the OpenAI-compatible chat completion response.
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// CompleteJSON sends a chat completion request with system and user messages,
// then parses the response content as JSON into result.
// Returns an error if the client is nil (not configured).
func (c *Client) CompleteJSON(ctx context.Context, systemPrompt, userPrompt string, result any) error {
	if c == nil {
		return fmt.Errorf("llm client not configured")
	}

	reqBody := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature:    0,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	const maxAttempts = 5
	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second}

	var lastErr error
	for attempt := range maxAttempts {
		content, err := c.doRequest(ctx, bodyBytes)
		if err == nil {
			sanitized := sanitizeJSON(content)
			if unmarshalErr := json.Unmarshal([]byte(sanitized), result); unmarshalErr != nil {
				return fmt.Errorf("unmarshal response: %w", unmarshalErr)
			}
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
func (c *Client) doRequest(ctx context.Context, bodyBytes []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
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
		return "", fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		httpErr := fmt.Errorf("http %d: %s", resp.StatusCode, string(respBody))
		if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return "", &retryableError{err: httpErr}
		}
		return "", httpErr
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshal chat response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return chatResp.Choices[0].Message.Content, nil
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
