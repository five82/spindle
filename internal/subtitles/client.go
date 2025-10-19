package subtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultMistralBaseURL = "https://api.mistral.ai"
	defaultMistralModel   = "voxtral-mini-2507"
	granularitySegment    = "segment"
	mistralAPITimeout     = 10 * time.Minute
	mistralHeaderAPIKey   = "x-api-key"
	mistralTranscribePath = "/v1/audio/transcriptions"
)

// Client transcribes audio into structured segments.
type Client interface {
	Transcribe(ctx context.Context, req TranscriptionRequest) (TranscriptionResponse, error)
}

// TranscriptionRequest describes the payload sent to Mistral.
type TranscriptionRequest struct {
	FilePath    string
	Language    string
	Model       string
	Granularity string
}

// TranscriptionResponse mirrors the subset of fields returned by Mistral needed for SRT generation.
type TranscriptionResponse struct {
	Text     string            `json:"text"`
	Language string            `json:"language"`
	Duration float64           `json:"duration"`
	Segments []TranscribedSpan `json:"segments"`
}

// TranscribedSpan represents a single caption span from the API response.
type TranscribedSpan struct {
	ID    int     `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// MistralClient calls the Mistral transcription API.
type MistralClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// MistralOption customizes a client.
type MistralOption func(*MistralClient)

// WithHTTPClient overrides the HTTP client used for requests.
func WithHTTPClient(client *http.Client) MistralOption {
	return func(mc *MistralClient) {
		if client != nil {
			mc.http = client
		}
	}
}

// WithBaseURL overrides the default base URL.
func WithBaseURL(baseURL string) MistralOption {
	return func(mc *MistralClient) {
		if strings.TrimSpace(baseURL) != "" {
			mc.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

// NewMistralClient constructs a client for the Voxtral transcription API.
func NewMistralClient(apiKey string, opts ...MistralOption) *MistralClient {
	client := &MistralClient{
		baseURL: defaultMistralBaseURL,
		apiKey:  strings.TrimSpace(apiKey),
		http: &http.Client{
			Timeout: mistralAPITimeout,
		},
	}

	for _, opt := range opts {
		opt(client)
	}
	return client
}

// Transcribe uploads an audio file to the Mistral API and returns the structured response.
func (m *MistralClient) Transcribe(ctx context.Context, req TranscriptionRequest) (TranscriptionResponse, error) {
	if m == nil {
		return TranscriptionResponse{}, fmt.Errorf("mistral client: nil client")
	}
	filePath := strings.TrimSpace(req.FilePath)
	if filePath == "" {
		return TranscriptionResponse{}, fmt.Errorf("mistral client: empty file path")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = defaultMistralModel
	}
	granularity := strings.TrimSpace(req.Granularity)
	if granularity == "" {
		granularity = granularitySegment
	}
	if m.apiKey == "" {
		return TranscriptionResponse{}, fmt.Errorf("mistral client: missing api key")
	}

	file, err := os.Open(filePath)
	if err != nil {
		return TranscriptionResponse{}, fmt.Errorf("mistral client: open audio: %w", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	if err := writer.WriteField("model", model); err != nil {
		return TranscriptionResponse{}, fmt.Errorf("mistral client: write model field: %w", err)
	}
	if req.Language != "" {
		if err := writer.WriteField("language", req.Language); err != nil {
			return TranscriptionResponse{}, fmt.Errorf("mistral client: write language field: %w", err)
		}
	}
	if err := writer.WriteField("timestamp_granularities", granularity); err != nil {
		return TranscriptionResponse{}, fmt.Errorf("mistral client: write granularity field: %w", err)
	}

	field, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return TranscriptionResponse{}, fmt.Errorf("mistral client: create file field: %w", err)
	}
	if _, err := io.Copy(field, file); err != nil {
		return TranscriptionResponse{}, fmt.Errorf("mistral client: copy audio: %w", err)
	}

	if err := writer.Close(); err != nil {
		return TranscriptionResponse{}, fmt.Errorf("mistral client: close multipart writer: %w", err)
	}

	endpoint := m.baseURL + mistralTranscribePath
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return TranscriptionResponse{}, fmt.Errorf("mistral client: build request: %w", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set(mistralHeaderAPIKey, m.apiKey)

	resp, err := m.http.Do(request)
	if err != nil {
		return TranscriptionResponse{}, fmt.Errorf("mistral client: http request: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return TranscriptionResponse{}, fmt.Errorf("mistral client: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return TranscriptionResponse{}, fmt.Errorf("mistral client: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var parsed TranscriptionResponse
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return TranscriptionResponse{}, fmt.Errorf("mistral client: decode response: %w", err)
	}
	return parsed, nil
}
