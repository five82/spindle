package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/five82/spindle/internal/config"
)

type chatRequest struct{ Reasoning struct{ Effort string } }

func TestNewEmptyAPIKey(t *testing.T) {
	c := New(config.LLMConfig{}, nil)
	if c != nil {
		t.Fatal("expected nil client for empty API key")
	}
}

func TestNewDefaultModel(t *testing.T) {
	c := New(config.LLMConfig{APIKey: "test"}, nil)
	if c.model != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("default model = %q, want deepseek/deepseek-v4.1-flash", c.model)
	}
}

func TestCompleteJSONNilClient(t *testing.T) {
	var c *Client
	err := c.CompleteJSON(context.Background(), "sys", "user", nil)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
	if err.Error() != "llm client not configured" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSanitizeJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "clean JSON",
			input: `{"key": "value"}`,
			want:  `{"key": "value"}`,
		},
		{
			name:  "with json fence",
			input: "```json\n{\"key\": \"value\"}\n```",
			want:  `{"key": "value"}`,
		},
		{
			name:  "with plain fence",
			input: "```\n{\"key\": \"value\"}\n```",
			want:  `{"key": "value"}`,
		},
		{
			name:  "with whitespace",
			input: "  \n{\"key\": \"value\"}\n  ",
			want:  `{"key": "value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeJSON(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeJSON(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCompleteJSONSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content type: %s", r.Header.Get("Content-Type"))
		}
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Reasoning.Effort != "low" {
			t.Errorf("reasoning = %#v, want low effort", req.Reasoning)
		}

		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": `{"answer": "hello"}`,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(config.LLMConfig{APIKey: "test-key", BaseURL: srv.URL, Model: defaultModel, Referer: "http://example.com", Title: "TestApp", TimeoutSeconds: 10}, nil)

	var result struct {
		Answer string `json:"answer"`
	}
	err := c.CompleteJSON(context.Background(), "system prompt", "user prompt", &result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Answer != "hello" {
		t.Fatalf("unexpected answer: %s", result.Answer)
	}
}

func TestCompleteJSONRequestPolicy(t *testing.T) {
	for _, model := range []string{"", defaultModel, "@preset/deepseek", "custom-model"} {
		t.Run(model, func(t *testing.T) {
			wantModel := model
			if wantModel == "" {
				wantModel = defaultModel
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode request: %v", err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				var gotModel string
				if err := json.Unmarshal(req["model"], &gotModel); err != nil || gotModel != wantModel {
					t.Errorf("model = %s, want %q (error: %v)", req["model"], wantModel, err)
				}
				if _, present := req["temperature"]; present {
					t.Error("temperature must be omitted for reasoning requests")
				}
				var reasoning struct{ Effort string }
				if err := json.Unmarshal(req["reasoning"], &reasoning); err != nil || reasoning.Effort != "low" {
					t.Errorf("reasoning = %s, want low effort (error: %v)", req["reasoning"], err)
				}
				var format struct{ Type string }
				if err := json.Unmarshal(req["response_format"], &format); err != nil || format.Type != "json_object" {
					t.Errorf("response_format = %s, want json_object (error: %v)", req["response_format"], err)
				}
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
			}))
			defer srv.Close()

			client := New(config.LLMConfig{APIKey: "key", BaseURL: srv.URL, Model: model}, nil)
			var result struct{ OK bool }
			if err := client.CompleteJSON(context.Background(), "system", "user", &result); err != nil {
				t.Fatal(err)
			}
			if !result.OK {
				t.Fatal("expected decoded result")
			}
		})
	}
}

func TestCompleteJSONRetryOn429(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Reasoning.Effort != "low" {
			t.Errorf("reasoning = %#v for custom model, want low effort", req.Reasoning)
		}
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate limited"))
			return
		}

		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": `{"ok": true}`,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(config.LLMConfig{APIKey: "test-key", BaseURL: srv.URL, Model: "test-model", TimeoutSeconds: 10}, nil)

	var result struct {
		OK bool `json:"ok"`
	}
	err := c.CompleteJSON(context.Background(), "sys", "user", &result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatal("expected ok to be true")
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got %d", calls.Load())
	}
}
