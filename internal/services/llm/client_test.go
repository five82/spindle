package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientHealthCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		payload := map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"content": `{"ok":true}`,
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test", BaseURL: server.URL, Model: "demo-model"})
	if err := client.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck returned error: %v", err)
	}
}

func TestClientHealthCheckCodeFence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"content": "```json\n{\"ok\":true}\n```",
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test", BaseURL: server.URL, Model: "demo-model"})
	if err := client.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck returned error: %v", err)
	}
}

func TestClientHealthCheckFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "bad", BaseURL: server.URL, Model: "demo"})
	if err := client.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected health check to fail")
	}
}

func TestCompleteJSONCodeFence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"content": "```json\n{\"profile\":\"grain\",\"confidence\":0.82,\"reason\":\"demo\"}\n```",
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test", BaseURL: server.URL, Model: "demo-model"})
	content, err := client.CompleteJSON(context.Background(), "test prompt", "Example Movie")
	if err != nil {
		t.Fatalf("CompleteJSON returned error: %v", err)
	}
	if !strings.Contains(content, "```") {
		t.Fatalf("expected raw content to retain code fence, got %q", content)
	}

	var parsed struct {
		Profile string `json:"profile"`
	}
	if err := DecodeLLMJSON(content, &parsed); err != nil {
		t.Fatalf("DecodeLLMJSON failed: %v", err)
	}
	if parsed.Profile != "grain" {
		t.Fatalf("expected profile grain, got %q", parsed.Profile)
	}
}

func TestCompleteJSONToolCallsArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{
			"choices": []any{
				map[string]any{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"content": "",
						"tool_calls": []any{
							map[string]any{
								"type": "function",
								"id":   "call_1",
								"function": map[string]any{
									"name":      "classify_preset",
									"arguments": `{"profile":"clean","confidence":0.91,"reason":"animated"}`,
								},
							},
						},
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test", BaseURL: server.URL, Model: "demo-model"})
	content, err := client.CompleteJSON(context.Background(), "test prompt", "Example Movie")
	if err != nil {
		t.Fatalf("CompleteJSON returned error: %v", err)
	}
	if !strings.Contains(content, "\"profile\"") {
		t.Fatalf("expected content to contain JSON arguments, got %q", content)
	}

	var parsed struct {
		Profile string `json:"profile"`
	}
	if err := DecodeLLMJSON(content, &parsed); err != nil {
		t.Fatalf("DecodeLLMJSON failed: %v", err)
	}
	if parsed.Profile != "clean" {
		t.Fatalf("expected profile clean, got %q", parsed.Profile)
	}
}

func TestCompleteJSONEmptyContentHasSnippet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{
			"choices": []any{
				map[string]any{
					"finish_reason": "stop",
					"message": map[string]any{
						"content": "",
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(
		Config{APIKey: "test", BaseURL: server.URL, Model: "demo-model"},
		WithRetryBackoff(0, 0),
		WithSleeper(func(time.Duration) {}),
	)
	_, err := client.CompleteJSON(context.Background(), "test prompt", "Example Movie")
	if err == nil {
		t.Fatal("expected CompleteJSON to fail")
	}
	if !strings.Contains(err.Error(), "empty content") || !strings.Contains(err.Error(), "response_snippet=") {
		t.Fatalf("expected empty-content error to include snippet, got %v", err)
	}
}

func TestCompleteJSONDeltaContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{
			"choices": []any{
				map[string]any{
					"finish_reason": "",
					"delta": map[string]any{
						"content": `{"profile":"grain","confidence":0.74,"reason":"film grain"}`,
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test", BaseURL: server.URL, Model: "demo-model"})
	content, err := client.CompleteJSON(context.Background(), "test prompt", "Example Movie")
	if err != nil {
		t.Fatalf("CompleteJSON returned error: %v", err)
	}

	var parsed struct {
		Profile    string  `json:"profile"`
		Confidence float64 `json:"confidence"`
	}
	if err := DecodeLLMJSON(content, &parsed); err != nil {
		t.Fatalf("DecodeLLMJSON failed: %v", err)
	}
	if parsed.Profile != "grain" {
		t.Fatalf("expected profile grain, got %q", parsed.Profile)
	}
	if parsed.Confidence != 0.74 {
		t.Fatalf("expected confidence 0.74, got %v", parsed.Confidence)
	}
}

func TestCompleteJSONLegacyText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{
			"choices": []any{
				map[string]any{
					"finish_reason": "stop",
					"text":          `{"profile":"clean","confidence":0.8,"reason":"animation"}`,
				},
			},
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test", BaseURL: server.URL, Model: "demo-model"})
	content, err := client.CompleteJSON(context.Background(), "test prompt", "Example Movie")
	if err != nil {
		t.Fatalf("CompleteJSON returned error: %v", err)
	}

	var parsed struct {
		Profile    string  `json:"profile"`
		Confidence float64 `json:"confidence"`
	}
	if err := DecodeLLMJSON(content, &parsed); err != nil {
		t.Fatalf("DecodeLLMJSON failed: %v", err)
	}
	if parsed.Profile != "clean" {
		t.Fatalf("expected profile clean, got %q", parsed.Profile)
	}
	if parsed.Confidence != 0.8 {
		t.Fatalf("expected confidence 0.8, got %v", parsed.Confidence)
	}
}

func TestCompleteJSONRetriesOnHTTP429(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "rate limited"})
			return
		}
		payload := map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"content": `{"ok":true}`,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	var slept []time.Duration
	client := NewClient(
		Config{APIKey: "test", BaseURL: server.URL, Model: "demo-model"},
		WithSleeper(func(d time.Duration) { slept = append(slept, d) }),
		WithRetryBackoff(0, 10*time.Second),
		WithRetryMaxAttempts(5),
	)
	content, err := client.CompleteJSON(context.Background(), "test prompt", "Example Movie")
	if err != nil {
		t.Fatalf("CompleteJSON returned error: %v", err)
	}
	if !strings.Contains(content, "ok") {
		t.Fatalf("expected content to contain ok, got %q", content)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
	if len(slept) != 1 || slept[0] != time.Second {
		t.Fatalf("expected single sleep of 1s, got %v", slept)
	}
}

func TestCompleteJSONRetriesOnEmptyContentThenSucceeds(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		content := ""
		if calls >= 3 {
			content = `{"ok":true}`
		}
		payload := map[string]any{
			"choices": []any{
				map[string]any{
					"finish_reason": "stop",
					"message": map[string]any{
						"content": content,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	client := NewClient(
		Config{APIKey: "test", BaseURL: server.URL, Model: "demo-model"},
		WithRetryBackoff(0, 0),
		WithSleeper(func(time.Duration) {}),
		WithRetryMaxAttempts(5),
	)
	content, err := client.CompleteJSON(context.Background(), "test prompt", "Example Movie")
	if err != nil {
		t.Fatalf("CompleteJSON returned error: %v", err)
	}
	if !strings.Contains(content, "ok") {
		t.Fatalf("expected content to contain ok, got %q", content)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}
