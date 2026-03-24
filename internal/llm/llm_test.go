package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestNewEmptyAPIKey(t *testing.T) {
	c := New("", "", "", "", "", 0, nil)
	if c != nil {
		t.Fatal("expected nil client for empty API key")
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

	c := New("test-key", srv.URL, "test-model", "http://example.com", "TestApp", 10, nil)

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

func TestCompleteJSONRetryOn429(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
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

	c := New("test-key", srv.URL, "test-model", "", "", 10, nil)

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
