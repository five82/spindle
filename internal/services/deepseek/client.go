package deepseek

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
	defaultBaseURL     = "https://api.deepseek.com"
	defaultModel       = "deepseek-reasoner"
	jsonResponseType   = "json_object"
	defaultHTTPTimeout = 15 * time.Second
)

// Client wraps the DeepSeek chat completion API.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option customizes the DeepSeek client.
type Option func(*Client)

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// WithBaseURL overrides the default API base (useful for tests/mocks).
func WithBaseURL(base string) Option {
	return func(c *Client) {
		base = strings.TrimSpace(base)
		if base != "" {
			c.baseURL = strings.TrimRight(base, "/")
		}
	}
}

// NewClient constructs a DeepSeek API client.
func NewClient(apiKey string, opts ...Option) *Client {
	client := &Client{
		apiKey:     strings.TrimSpace(apiKey),
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: defaultHTTPTimeout},
	}
	for _, opt := range opts {
		opt(client)
	}
	if client.baseURL == "" {
		client.baseURL = defaultBaseURL
	}
	if client.httpClient == nil {
		client.httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return client
}

// Classification captures the JSON payload returned by DeepSeek.
type Classification struct {
	Profile    string  `json:"profile"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
	Raw        string  `json:"-"`
}

// ClassifyPreset asks DeepSeek to categorize a title description into a Drapto preset profile.
func (c *Client) ClassifyPreset(ctx context.Context, description string) (Classification, error) {
	var empty Classification
	description = strings.TrimSpace(description)
	if description == "" {
		return empty, errors.New("deepseek classify: description required")
	}
	if strings.TrimSpace(c.apiKey) == "" {
		return empty, errors.New("deepseek classify: api key required")
	}
	requestBody, err := buildChatRequest(description)
	if err != nil {
		return empty, err
	}
	endpoint, err := url.JoinPath(c.baseURL, "/chat/completions")
	if err != nil {
		return empty, fmt.Errorf("deepseek classify: build url: %w", err)
	}
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return empty, fmt.Errorf("deepseek classify: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return empty, fmt.Errorf("deepseek classify: request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return empty, fmt.Errorf("deepseek classify: request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return empty, fmt.Errorf("deepseek classify: read body: %w", err)
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		return empty, fmt.Errorf("deepseek classify: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var completion chatCompletionResponse
	if err := json.Unmarshal(body, &completion); err != nil {
		return empty, fmt.Errorf("deepseek classify: decode response: %w", err)
	}
	if completion.Error != nil {
		return empty, fmt.Errorf("deepseek classify: api error: %s", strings.TrimSpace(completion.Error.Message))
	}
	if len(completion.Choices) == 0 {
		return empty, errors.New("deepseek classify: empty choices")
	}
	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	if content == "" {
		return empty, errors.New("deepseek classify: empty content")
	}
	var parsed Classification
	parsed.Raw = content
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return empty, fmt.Errorf("deepseek classify: parse payload: %w", err)
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

func buildChatRequest(description string) (chatCompletionRequest, error) {
	description = strings.TrimSpace(description)
	if description == "" {
		return chatCompletionRequest{}, errors.New("deepseek classify: description required")
	}
	return chatCompletionRequest{
		Model: defaultModel,
		Messages: []chatMessage{
			{Role: "system", Content: PresetClassificationPrompt},
			{Role: "user", Content: description},
		},
		Temperature: 0,
		ResponseFormat: map[string]string{
			"type": jsonResponseType,
		},
	}, nil
}
