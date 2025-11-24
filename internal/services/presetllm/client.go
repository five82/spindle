package presetllm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	jsonResponseType   = "json_object"
	defaultHTTPTimeout = 15 * time.Second
)

// Config captures the runtime settings required to talk to the preset LLM.
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
	Referer string
	Title   string
}

// Client wraps the OpenRouter chat completion API.
type Client struct {
	cfg        Config
	httpClient *http.Client
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

// NewClient constructs a preset LLM client using the supplied configuration.
func NewClient(cfg Config, opts ...Option) *Client {
	client := &Client{
		cfg: Config{
			APIKey:  strings.TrimSpace(cfg.APIKey),
			BaseURL: strings.TrimSpace(cfg.BaseURL),
			Model:   strings.TrimSpace(cfg.Model),
			Referer: strings.TrimSpace(cfg.Referer),
			Title:   strings.TrimSpace(cfg.Title),
		},
		httpClient: &http.Client{Timeout: defaultHTTPTimeout},
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

// Classification captures the JSON payload returned by the LLM.
type Classification struct {
	Profile    string  `json:"profile"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
	Raw        string  `json:"-"`
}

// ClassifyPreset issues a classification request for the supplied description.
func (c *Client) ClassifyPreset(ctx context.Context, description string) (Classification, error) {
	var empty Classification
	description = strings.TrimSpace(description)
	if description == "" {
		return empty, errors.New("preset llm classify: description required")
	}
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return empty, errors.New("preset llm classify: api key required")
	}
	requestBody, err := buildChatRequest(c.cfg.Model, description)
	if err != nil {
		return empty, err
	}
	completion, err := c.sendChatRequest(ctx, requestBody)
	if err != nil {
		return empty, err
	}
	if len(completion.Choices) == 0 {
		return empty, errors.New("preset llm classify: empty choices")
	}
	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	if content == "" {
		return empty, errors.New("preset llm classify: empty content")
	}
	var parsed Classification
	parsed.Raw = content
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return empty, fmt.Errorf("preset llm classify: parse payload: %w", err)
	}
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
		return errors.New("preset llm health: api key required")
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
	completion, err := c.sendChatRequest(ctx, payload)
	if err != nil {
		return err
	}
	if len(completion.Choices) == 0 {
		return errors.New("preset llm health: empty response")
	}
	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	if content == "" {
		return errors.New("preset llm health: empty content")
	}
	var parsed struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return fmt.Errorf("preset llm health: parse payload: %w", err)
	}
	if !parsed.OK {
		return errors.New("preset llm health: unexpected response")
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
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func buildChatRequest(model, description string) (chatCompletionRequest, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return chatCompletionRequest{}, errors.New("preset llm classify: model required")
	}
	description = strings.TrimSpace(description)
	if description == "" {
		return chatCompletionRequest{}, errors.New("preset llm classify: description required")
	}
	return chatCompletionRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: PresetClassificationPrompt},
			{Role: "user", Content: description},
		},
		Temperature:    0,
		ResponseFormat: map[string]string{"	type": jsonResponseType},
	}, nil
}

func (c *Client) sendChatRequest(ctx context.Context, payload chatCompletionRequest) (chatCompletionResponse, error) {
	var completion chatCompletionResponse
	endpoint, err := url.JoinPath(c.cfg.BaseURL, "")
	if err != nil {
		return completion, fmt.Errorf("preset llm request: build url: %w", err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return completion, fmt.Errorf("preset llm request: encode body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return completion, fmt.Errorf("preset llm request: new request: %w", err)
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
		return completion, fmt.Errorf("preset llm request: http error: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return completion, fmt.Errorf("preset llm request: read body: %w", err)
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		return completion, fmt.Errorf("preset llm request: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, &completion); err != nil {
		return completion, fmt.Errorf("preset llm request: decode response: %w", err)
	}
	if completion.Error != nil {
		return completion, fmt.Errorf("preset llm request: api error: %s", strings.TrimSpace(completion.Error.Message))
	}
	return completion, nil
}
