package spindle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientTokenAndDaemonLogQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			http.Error(w, "wrong authorization", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/logs" || r.URL.Query().Get("daemon_only") != "1" || len(r.URL.Query()) != 1 {
			http.Error(w, "wrong query", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(LogBatch{Next: 42})
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(server.URL, WithToken("  secret  "))
	if err != nil {
		t.Fatal(err)
	}
	batch, err := client.FetchLogs(context.Background(), LogQuery{DaemonOnly: true})
	if err != nil || batch.Next != 42 {
		t.Fatalf("FetchLogs = %+v, %v; want next=42", batch, err)
	}
}

func TestClientInvalidEndpointAndNilReceiver(t *testing.T) {
	for _, endpoint := range []string{" ", "http://[invalid"} {
		if _, err := NewClient(endpoint); err == nil {
			t.Errorf("NewClient(%q) succeeded", endpoint)
		}
	}
	var client *Client
	if _, err := client.FetchStatus(context.Background()); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Errorf("nil FetchStatus error = %v", err)
	}
	if _, err := client.FetchQueue(context.Background()); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Errorf("nil FetchQueue error = %v", err)
	}
	if _, err := client.FetchLogs(context.Background(), LogQuery{}); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Errorf("nil FetchLogs error = %v", err)
	}
}

func TestClientCanceledRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(StatusResponse{})
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.FetchStatus(ctx); err == nil || !strings.Contains(err.Error(), "execute request") {
		t.Fatalf("canceled FetchStatus error = %v", err)
	}
}
