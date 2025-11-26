package presetllm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	classification, err := client.ClassifyPreset(context.Background(), "Example Movie")
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
