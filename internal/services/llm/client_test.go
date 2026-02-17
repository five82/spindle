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

func TestClientClassifyPresetCodeFence(t *testing.T) {
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
	classification, err := client.ClassifyPreset(context.Background(), "test prompt", "Example Movie")
	if err != nil {
		t.Fatalf("ClassifyPreset returned error: %v", err)
	}
	if classification.Profile != "grain" {
		t.Fatalf("expected profile grain, got %q", classification.Profile)
	}
	if classification.Confidence != 0.82 {
		t.Fatalf("expected confidence 0.82, got %v", classification.Confidence)
	}
	if classification.Raw == "" || !strings.Contains(classification.Raw, "```") {
		t.Fatalf("expected raw payload to retain code fence, got %q", classification.Raw)
	}
}

func TestClientClassifyPresetToolCallsArguments(t *testing.T) {
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
	classification, err := client.ClassifyPreset(context.Background(), "test prompt", "Example Movie")
	if err != nil {
		t.Fatalf("ClassifyPreset returned error: %v", err)
	}
	if classification.Profile != "clean" {
		t.Fatalf("expected profile clean, got %q", classification.Profile)
	}
	if classification.Raw == "" || !strings.Contains(classification.Raw, "\"profile\"") {
		t.Fatalf("expected raw payload to contain JSON arguments, got %q", classification.Raw)
	}
}

func TestClientClassifyPresetEmptyContentHasSnippet(t *testing.T) {
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
	_, err := client.ClassifyPreset(context.Background(), "test prompt", "Example Movie")
	if err == nil {
		t.Fatal("expected classify to fail")
	}
	if !strings.Contains(err.Error(), "empty content") || !strings.Contains(err.Error(), "response_snippet=") {
		t.Fatalf("expected empty-content error to include snippet, got %v", err)
	}
}

func TestClientClassifyPresetDeltaContent(t *testing.T) {
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
	classification, err := client.ClassifyPreset(context.Background(), "test prompt", "Example Movie")
	if err != nil {
		t.Fatalf("ClassifyPreset returned error: %v", err)
	}
	if classification.Profile != "grain" {
		t.Fatalf("expected profile grain, got %q", classification.Profile)
	}
	if classification.Confidence != 0.74 {
		t.Fatalf("expected confidence 0.74, got %v", classification.Confidence)
	}
}

func TestClientClassifyPresetLegacyText(t *testing.T) {
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
	classification, err := client.ClassifyPreset(context.Background(), "test prompt", "Example Movie")
	if err != nil {
		t.Fatalf("ClassifyPreset returned error: %v", err)
	}
	if classification.Profile != "clean" {
		t.Fatalf("expected profile clean, got %q", classification.Profile)
	}
	if classification.Confidence != 0.8 {
		t.Fatalf("expected confidence 0.8, got %v", classification.Confidence)
	}
}

func TestClientRetriesOnHTTP429(t *testing.T) {
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
						"content": `{"profile":"grain","confidence":0.9,"reason":"demo"}`,
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
	classification, err := client.ClassifyPreset(context.Background(), "test prompt", "Example Movie")
	if err != nil {
		t.Fatalf("ClassifyPreset returned error: %v", err)
	}
	if classification.Profile != "grain" {
		t.Fatalf("expected profile grain, got %q", classification.Profile)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
	if len(slept) != 1 || slept[0] != time.Second {
		t.Fatalf("expected single sleep of 1s, got %v", slept)
	}
}

func TestClientRetriesOnEmptyContentThenSucceeds(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		content := ""
		if calls >= 3 {
			content = `{"profile":"clean","confidence":0.75,"reason":"demo"}`
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
	classification, err := client.ClassifyPreset(context.Background(), "test prompt", "Example Movie")
	if err != nil {
		t.Fatalf("ClassifyPreset returned error: %v", err)
	}
	if classification.Profile != "clean" {
		t.Fatalf("expected profile clean, got %q", classification.Profile)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}
