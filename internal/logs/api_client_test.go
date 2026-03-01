package logs_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"spindle/internal/api"
	"spindle/internal/logs"
)

func TestNewStreamClientEmptyBind(t *testing.T) {
	client, err := logs.NewStreamClient("")
	if err != nil {
		t.Fatalf("NewStreamClient error: %v", err)
	}
	if client != nil {
		t.Fatal("expected nil client for empty bind")
	}
}

func TestStreamClientFetchBuildsQueryAndDecodes(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.LogStreamResponse{
			Events: []api.LogEvent{{
				Timestamp: time.Now().UTC(),
				Level:     "info",
				Message:   "hello",
			}},
			Next: 42,
		})
	}))
	defer srv.Close()

	client, err := logs.NewStreamClient(srv.URL)
	if err != nil {
		t.Fatalf("NewStreamClient error: %v", err)
	}

	resp, err := client.Fetch(context.Background(), logs.StreamQuery{
		Since:         3,
		Limit:         50,
		Follow:        true,
		Tail:          true,
		Component:     "workflow",
		Lane:          "background",
		CorrelationID: "req-1",
		ItemID:        99,
		Level:         "warn",
		Alert:         "error",
		DecisionType:  "selector",
		Search:        "needle",
	})
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if len(resp.Events) != 1 || resp.Next != 42 {
		t.Fatalf("unexpected response: %+v", resp)
	}

	for key, want := range map[string]string{
		"since":          "3",
		"limit":          "50",
		"follow":         "1",
		"tail":           "1",
		"component":      "workflow",
		"lane":           "background",
		"correlation_id": "req-1",
		"item":           "99",
		"level":          "warn",
		"alert":          "error",
		"decision_type":  "selector",
		"search":         "needle",
	} {
		if got := gotQuery.Get(key); got != want {
			t.Fatalf("query[%s]: expected %q, got %q", key, want, got)
		}
	}
}

func TestIsAPIUnavailable(t *testing.T) {
	if !logs.IsAPIUnavailable(logs.ErrAPIUnavailable) {
		t.Fatal("expected ErrAPIUnavailable to be unavailable")
	}
	if logs.IsAPIUnavailable(errors.New("other")) {
		t.Fatal("did not expect generic error to be unavailable")
	}
}
