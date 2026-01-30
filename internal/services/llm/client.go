package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	jsonResponseType      = "json_object"
	defaultHTTPTimeout    = 15 * time.Second
	defaultRetryMaxDelay  = 10 * time.Second
	defaultRetryBaseDelay = 1 * time.Second
	defaultRetryAttempts  = 5
)

// Config captures the runtime settings required to talk to the LLM.
type Config struct {
	APIKey         string
	BaseURL        string
	Model          string
	Referer        string
	Title          string
	TimeoutSeconds int
}

// DefaultHTTPTimeout returns the default timeout used for LLM requests.
func DefaultHTTPTimeout() time.Duration {
	return defaultHTTPTimeout
}

// Client wraps the OpenRouter chat completion API.
type Client struct {
	cfg        Config
	httpClient *http.Client

	retryMaxAttempts int
	retryBaseDelay   time.Duration
	retryMaxDelay    time.Duration
	sleeper          func(time.Duration)
}

// Option customizes the client.
type Option func(*Client)

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// WithRetryMaxAttempts overrides the default retry count (defaults to 5).
func WithRetryMaxAttempts(attempts int) Option {
	return func(c *Client) {
		c.retryMaxAttempts = attempts
	}
}

// WithRetryBackoff overrides the retry backoff delays.
func WithRetryBackoff(baseDelay, maxDelay time.Duration) Option {
	return func(c *Client) {
		c.retryBaseDelay = baseDelay
		c.retryMaxDelay = maxDelay
	}
}

// WithSleeper overrides how retry sleeps are performed (useful for tests).
func WithSleeper(sleeper func(time.Duration)) Option {
	return func(c *Client) {
		c.sleeper = sleeper
	}
}

// NewClient constructs an LLM client using the supplied configuration.
func NewClient(cfg Config, opts ...Option) *Client {
	timeout := defaultHTTPTimeout
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	client := &Client{
		cfg: Config{
			APIKey:         strings.TrimSpace(cfg.APIKey),
			BaseURL:        strings.TrimSpace(cfg.BaseURL),
			Model:          strings.TrimSpace(cfg.Model),
			Referer:        strings.TrimSpace(cfg.Referer),
			Title:          strings.TrimSpace(cfg.Title),
			TimeoutSeconds: cfg.TimeoutSeconds,
		},
		httpClient:       &http.Client{Timeout: timeout},
		retryMaxAttempts: defaultRetryAttempts,
		retryBaseDelay:   defaultRetryBaseDelay,
		retryMaxDelay:    defaultRetryMaxDelay,
	}
	for _, opt := range opts {
		opt(client)
	}
	if client.cfg.BaseURL == "" {
		client.cfg.BaseURL = "https://openrouter.ai/api/v1/chat/completions"
	}
	if client.httpClient == nil {
		client.httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return client
}

// Classification captures the JSON payload returned by the LLM for preset classification.
type Classification struct {
	Profile    string  `json:"profile"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
	Raw        string  `json:"-"`
}

type httpStatusError struct {
	StatusCode int
	Body       string
	RetryAfter time.Duration
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("llm request: http %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
}

type emptyContentError struct {
	Op           string
	FinishReason string
	Refusal      string
	Snippet      string
}

func (e *emptyContentError) Error() string {
	return fmt.Sprintf(
		"%s: empty content (finish_reason=%q, refusal=%q, response_snippet=%s)",
		e.Op,
		e.FinishReason,
		e.Refusal,
		e.Snippet,
	)
}

// CompleteJSON issues a JSON-only chat completion request with the supplied prompts.
// It returns the raw JSON payload produced by the model.
func (c *Client) CompleteJSON(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	systemPrompt = strings.TrimSpace(systemPrompt)
	userPrompt = strings.TrimSpace(userPrompt)
	if systemPrompt == "" {
		return "", errors.New("llm complete: system prompt required")
	}
	if userPrompt == "" {
		return "", errors.New("llm complete: user prompt required")
	}
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return "", errors.New("llm complete: api key required")
	}
	payload := chatCompletionRequest{
		Model: c.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature:    0,
		ResponseFormat: map[string]string{"type": jsonResponseType},
	}
	return c.completionContentWithRetry(ctx, payload, "llm complete")
}

// ClassifyPreset issues a classification request for the supplied description.
func (c *Client) ClassifyPreset(ctx context.Context, description string) (Classification, error) {
	var empty Classification
	description = strings.TrimSpace(description)
	if description == "" {
		return empty, errors.New("llm classify: description required")
	}
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return empty, errors.New("llm classify: api key required")
	}
	content, err := c.CompleteJSON(ctx, PresetClassificationPrompt, description)
	if err != nil {
		return empty, err
	}
	var parsed Classification
	if err := DecodeLLMJSON(content, &parsed); err != nil {
		return empty, fmt.Errorf("llm classify: parse payload: %w", err)
	}
	parsed.Raw = content
	parsed.Profile = strings.ToLower(strings.TrimSpace(parsed.Profile))
	if parsed.Confidence < 0 {
		parsed.Confidence = 0
	}
	if parsed.Confidence > 1 {
		parsed.Confidence = 1
	}
	parsed.Reason = strings.TrimSpace(parsed.Reason)
	return parsed, nil
}

// HealthCheck issues a fast ping to verify the API key and model are usable.
func (c *Client) HealthCheck(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return errors.New("llm health: api key required")
	}
	payload := chatCompletionRequest{
		Model: c.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: "You must respond with JSON only."},
			{Role: "user", Content: "Respond with {\"ok\":true}"},
		},
		Temperature:    0,
		ResponseFormat: map[string]string{"type": jsonResponseType},
	}
	content, err := c.completionContentWithRetry(ctx, payload, "llm health")
	if err != nil {
		return err
	}
	var parsed struct {
		OK bool `json:"ok"`
	}
	if err := DecodeLLMJSON(content, &parsed); err != nil {
		return fmt.Errorf("llm health: parse payload: %w", err)
	}
	if !parsed.OK {
		return errors.New("llm health: unexpected response")
	}
	return nil
}

type chatCompletionRequest struct {
	Model          string            `json:"model"`
	Messages       []chatMessage     `json:"messages"`
	Temperature    float64           `json:"temperature"`
	ResponseFormat map[string]string `json:"response_format"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatCompletionMessage `json:"message"`
		// Some providers mistakenly return the streaming schema (delta) even when
		// stream=false, so tolerate it as a fallback.
		Delta chatCompletionMessage `json:"delta"`
		// Legacy "text" field (completion-style responses).
		Text         string `json:"text"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type chatCompletionMessage struct {
	Content      string        `json:"content"`
	ToolCalls    []toolCall    `json:"tool_calls"`
	FunctionCall *functionCall `json:"function_call"`
	Refusal      string        `json:"refusal"`
}

type toolCall struct {
	Type     string       `json:"type"`
	ID       string       `json:"id"`
	Index    int          `json:"index"`
	Function functionCall `json:"function"`
}

type functionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (c *Client) completionContentWithRetry(ctx context.Context, payload chatCompletionRequest, op string) (string, error) {
	attempts := c.retryAttempts()
	var lastErr error

	for attempt := 1; attempt <= attempts; attempt++ {
		completion, body, err := c.sendChatRequestOnce(ctx, payload)
		if err == nil {
			content, finishReason := extractCompletionPayload(completion)
			if content == "" {
				if len(completion.Choices) == 0 {
					err = fmt.Errorf("%s: empty choices", op)
				} else {
					err = &emptyContentError{
						Op:           op,
						FinishReason: finishReason,
						Refusal:      extractCompletionRefusal(completion),
						Snippet:      summarizePayloadSnippet(string(body)),
					}
				}
			} else {
				return content, nil
			}
		}

		delay, retry := c.retryDelay(ctx, err, attempt, attempts)
		if !retry {
			return "", err
		}
		if err := c.sleep(ctx, delay); err != nil {
			return "", err
		}
		lastErr = err
	}

	if lastErr == nil {
		lastErr = errors.New("unknown retry failure")
	}
	return "", fmt.Errorf("%s: failed after %d attempts: %w", op, attempts, lastErr)
}

func extractCompletionPayload(completion chatCompletionResponse) (string, string) {
	var finishReason string
	for _, choice := range completion.Choices {
		if finishReason == "" {
			finishReason = strings.TrimSpace(choice.FinishReason)
		}
		if content := firstNonEmpty(
			choice.Message.Content,
			choice.Delta.Content,
			choice.Text,
		); content != "" {
			return content, finishReason
		}
		if args := firstNonEmpty(
			functionCallArguments(choice.Message.FunctionCall),
			functionCallArguments(choice.Delta.FunctionCall),
		); args != "" {
			return args, finishReason
		}
		if args := firstNonEmpty(
			toolCallArguments(choice.Message.ToolCalls),
			toolCallArguments(choice.Delta.ToolCalls),
		); args != "" {
			return args, finishReason
		}
	}
	return "", finishReason
}

func extractCompletionRefusal(completion chatCompletionResponse) string {
	for _, choice := range completion.Choices {
		if refusal := firstNonEmpty(choice.Message.Refusal, choice.Delta.Refusal); refusal != "" {
			return refusal
		}
	}
	return ""
}

func functionCallArguments(fc *functionCall) string {
	if fc == nil {
		return ""
	}
	return strings.TrimSpace(fc.Arguments)
}

func toolCallArguments(calls []toolCall) string {
	for _, call := range calls {
		if args := strings.TrimSpace(call.Function.Arguments); args != "" {
			return args
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (c *Client) sendChatRequestOnce(ctx context.Context, payload chatCompletionRequest) (chatCompletionResponse, []byte, error) {
	var completion chatCompletionResponse
	endpoint, err := url.JoinPath(c.cfg.BaseURL, "")
	if err != nil {
		return completion, nil, fmt.Errorf("llm request: build url: %w", err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return completion, nil, fmt.Errorf("llm request: encode body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return completion, nil, fmt.Errorf("llm request: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.Referer != "" {
		req.Header.Set("HTTP-Referer", c.cfg.Referer)
		req.Header.Set("Referer", c.cfg.Referer)
	}
	if c.cfg.Title != "" {
		req.Header.Set("X-Title", c.cfg.Title)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return completion, nil, fmt.Errorf("llm request: http error (timeout=%s): %w", c.timeoutDuration(), err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return completion, nil, fmt.Errorf("llm request: read body (timeout=%s): %w", c.timeoutDuration(), err)
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		retryAfter, _ := parseRetryAfter(resp.Header.Get("Retry-After"))
		return completion, body, &httpStatusError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
			RetryAfter: retryAfter,
		}
	}
	if err := json.Unmarshal(body, &completion); err != nil {
		return completion, body, fmt.Errorf("llm request: decode response: %w", err)
	}
	if completion.Error != nil {
		return completion, body, fmt.Errorf("llm request: api error: %s", strings.TrimSpace(completion.Error.Message))
	}
	return completion, body, nil
}

func (c *Client) timeoutDuration() time.Duration {
	if c == nil || c.httpClient == nil {
		return defaultHTTPTimeout
	}
	if c.httpClient.Timeout <= 0 {
		return defaultHTTPTimeout
	}
	return c.httpClient.Timeout
}

func (c *Client) retryAttempts() int {
	if c == nil {
		return 1
	}
	if c.retryMaxAttempts <= 0 {
		return 1
	}
	return c.retryMaxAttempts
}

func (c *Client) retryDelay(ctx context.Context, err error, attempt, maxAttempts int) (time.Duration, bool) {
	if attempt >= maxAttempts {
		return 0, false
	}
	if err == nil {
		return 0, false
	}
	if ctx == nil {
		return 0, false
	}
	if ctx.Err() != nil {
		return 0, false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 0, false
	}

	if _, ok := err.(*emptyContentError); ok {
		return c.backoffDelay(attempt), true
	}

	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		switch {
		case statusErr.StatusCode == http.StatusRequestTimeout,
			statusErr.StatusCode == http.StatusTooManyRequests,
			statusErr.StatusCode >= http.StatusInternalServerError:
			if statusErr.RetryAfter > 0 {
				return c.capDelay(statusErr.RetryAfter), true
			}
			return c.backoffDelay(attempt), true
		default:
			return 0, false
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return c.backoffDelay(attempt), true
		}
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		// url.Error often wraps net.Error types, but keep a conservative retry for
		// non-context errors anyway.
		if urlErr.Timeout() {
			return c.backoffDelay(attempt), true
		}
	}

	return 0, false
}

func (c *Client) backoffDelay(attempt int) time.Duration {
	base := defaultRetryBaseDelay
	maxDelay := defaultRetryMaxDelay
	if c != nil {
		if c.retryBaseDelay >= 0 {
			base = c.retryBaseDelay
		}
		if c.retryMaxDelay > 0 {
			maxDelay = c.retryMaxDelay
		}
	}
	if base <= 0 {
		return 0
	}

	retryCount := attempt // attempt is 1-based, delay is for the next attempt.
	if retryCount <= 0 {
		retryCount = 1
	}

	// attempt 1 -> base, attempt 2 -> base*2, attempt 3 -> base*4, ...
	delay := base
	for i := 1; i < retryCount; i++ {
		if delay > maxDelay/2 {
			delay = maxDelay
			break
		}
		delay *= 2
	}
	return c.capDelay(delay)
}

func (c *Client) capDelay(delay time.Duration) time.Duration {
	if delay < 0 {
		return 0
	}
	maxDelay := defaultRetryMaxDelay
	if c != nil && c.retryMaxDelay > 0 {
		maxDelay = c.retryMaxDelay
	}
	if maxDelay > 0 && delay > maxDelay {
		return maxDelay
	}
	return delay
}

func (c *Client) sleep(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	if ctx == nil {
		return errors.New("llm retry: nil context")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if c != nil && c.sleeper != nil {
		c.sleeper(delay)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseRetryAfter(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	if when, err := http.ParseTime(value); err == nil {
		delay := time.Until(when)
		if delay < 0 {
			return 0, false
		}
		return delay, true
	}
	return 0, false
}

// DecodeLLMJSON decodes JSON from an LLM response, handling common formatting quirks.
func DecodeLLMJSON(content string, target any) error {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return errors.New("empty payload")
	}

	// Try direct unmarshal first
	directErr := json.Unmarshal([]byte(trimmed), target)
	if directErr == nil {
		return nil
	}

	// Try sanitizing (strip code fences, extract JSON object/array)
	sanitized := sanitizeJSONPayload(trimmed)
	if sanitized == "" || sanitized == trimmed {
		return fmt.Errorf("%w (payload snippet: %s)", directErr, summarizePayloadSnippet(trimmed))
	}

	sanitizedErr := json.Unmarshal([]byte(sanitized), target)
	if sanitizedErr == nil {
		return nil
	}
	return fmt.Errorf("%w (sanitized payload snippet: %s)", sanitizedErr, summarizePayloadSnippet(sanitized))
}

func sanitizeJSONPayload(content string) string {
	trimmed := strings.TrimSpace(stripCodeFenceBlock(content))
	if trimmed == "" {
		return ""
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		return trimmed
	}
	if start := strings.Index(trimmed, "{"); start >= 0 {
		if end := strings.LastIndex(trimmed, "}"); end > start {
			return strings.TrimSpace(trimmed[start : end+1])
		}
	}
	if start := strings.Index(trimmed, "["); start >= 0 {
		if end := strings.LastIndex(trimmed, "]"); end > start {
			return strings.TrimSpace(trimmed[start : end+1])
		}
	}
	return trimmed
}

func stripCodeFenceBlock(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	body := trimmed[3:]
	body = strings.TrimLeft(body, " \t\r\n")
	if len(body) >= 4 && strings.EqualFold(body[:4], "json") {
		body = body[4:]
		body = strings.TrimLeft(body, " \t\r\n")
	}
	if idx := strings.LastIndex(body, "```"); idx >= 0 {
		body = body[:idx]
	}
	return strings.TrimSpace(body)
}

func summarizePayloadSnippet(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "<empty>"
	}
	replacer := strings.NewReplacer("\r", " ", "\n", " ", "\t", " ")
	clean := replacer.Replace(trimmed)
	clean = strings.Join(strings.Fields(clean), " ")
	const limit = 160
	runes := []rune(clean)
	if len(runes) > limit {
		clean = string(runes[:limit]) + "..."
	}
	return clean
}
