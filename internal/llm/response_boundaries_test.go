package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
)

func TestLLMRejectsMalformedAndEmptyCompletionResponses(t *testing.T) {
	response := "not json"
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status); _, _ = w.Write([]byte(response)) }))
	defer server.Close()
	c := New(config.LLMConfig{APIKey: "test", BaseURL: server.URL}, nil)
	for _, tc := range []struct{ body, want string }{{"not json", "unmarshal chat response"}, {`{"choices":[]}`, "no choices in response"}} {
		response = tc.body
		if _, err := c.doRequest(context.Background(), []byte(`{}`)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%q: %v", tc.body, err)
		}
	}
	status = http.StatusBadRequest
	if _, err := c.doRequest(context.Background(), []byte(`{}`)); err == nil || isRetryable(err) {
		t.Fatalf("400 should not retry: %v", err)
	}
	status = http.StatusServiceUnavailable
	if _, err := c.doRequest(context.Background(), []byte(`{}`)); err == nil || !isRetryable(err) {
		t.Fatalf("503 should retry: %v", err)
	}
	status = http.StatusOK
	response = `{"choices":[{"message":{"content":"invalid json"}}]}`
	var out map[string]any
	if err := c.CompleteJSON(context.Background(), "system", "user", &out); err == nil || !strings.Contains(err.Error(), "unmarshal response") {
		t.Fatalf("malformed model JSON: %v", err)
	}
}
