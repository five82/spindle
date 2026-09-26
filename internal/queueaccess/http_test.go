package queueaccess

import (
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/queue"
)

func TestOpenHTTPHealth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			t.Errorf("health path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	if _, err := OpenHTTP(path, "token"); err != nil {
		t.Fatalf("OpenHTTP healthy daemon: %v", err)
	}
}

func TestQueueHTTPMethods(t *testing.T) {
	access := &HTTPAccess{token: "secret", client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		var response string
		switch r.Method + " " + r.URL.Path {
		case "GET /api/logs":
			want := "component=worker&daemon_only=1&item=9&lane=encode&level=warn&limit=12&request=req-1&since=3&tail=1"
			if r.URL.RawQuery != want {
				t.Errorf("logs query = %q, want %q", r.URL.RawQuery, want)
			}
			response = `{"events":[{}],"next":4}`
		case "GET /api/queue":
			if r.URL.Query().Get("stage") != "failed" {
				t.Errorf("queue filter = %q", r.URL.RawQuery)
			}
			response = `{"items":[{"id":9}]}`
		case "GET /api/queue/9", "POST /api/queue/enqueue-cached":
			response = `{"item":{"id":9}}`
		case "GET /api/status":
			response = `{}`
		case "POST /api/queue/retry", "POST /api/queue/stop":
			if !strings.Contains(string(body), `"ids":[9]`) {
				t.Errorf("request body = %q", body)
			}
			response = `{"updated":1}`
		case "POST /api/queue/retry-episode":
			if !strings.Contains(string(body), `"episode_key":"s01e01"`) {
				t.Errorf("episode body = %q", body)
			}
			response = `{"result":"retried"}`
		case "POST /api/queue/clear":
			if !strings.Contains(string(body), `"scope":"all"`) {
				t.Errorf("clear body = %q", body)
			}
			response = `{"removed":2}`
		case "DELETE /api/queue/9":
			response = `{"removed":1}`
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			response = `{}`
		}
		if r.Method == http.MethodPost && r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("POST content type = %q", r.Header.Get("Content-Type"))
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
	})}}
	logs, next, err := access.Logs(LogsQuery{Since: 3, Limit: 12, Tail: true, ItemID: 9, Component: "worker", Lane: "encode", Request: "req-1", Level: "warn", DaemonOnly: true})
	if err != nil || len(logs) != 1 || next != 4 {
		t.Fatalf("Logs = %+v, %d, %v", logs, next, err)
	}
	items, err := access.List(queue.StageFailed)
	if err != nil || len(items) != 1 || items[0].ID != 9 {
		t.Fatalf("List = %+v, %v", items, err)
	}
	item, err := access.GetByID(9)
	if err != nil || item.ID != 9 {
		t.Fatalf("GetByID = %+v, %v", item, err)
	}
	if status, err := access.Status(); err != nil || status == nil {
		t.Fatalf("Status = %+v, %v", status, err)
	}
	if n, err := access.Retry(9); err != nil || n != 1 {
		t.Fatalf("Retry = %d, %v", n, err)
	}
	if result, err := access.RetryEpisode(9, "s01e01"); err != nil || result != "retried" {
		t.Fatalf("RetryEpisode = %q, %v", result, err)
	}
	if n, err := access.Stop(9); err != nil || n != 1 {
		t.Fatalf("Stop = %d, %v", n, err)
	}
	if item, err := access.EnqueueCached(EnqueueCachedRequest{DiscTitle: "Movie"}); err != nil || item.ID != 9 {
		t.Fatalf("EnqueueCached = %+v, %v", item, err)
	}
	if n, err := access.Clear("all"); err != nil || n != 2 {
		t.Fatalf("Clear = %d, %v", n, err)
	}
	if n, err := access.Remove(9); err != nil || n != 1 {
		t.Fatalf("Remove = %d, %v", n, err)
	}
}

func TestQueueHTTPFailures(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"unauthorized", http.StatusUnauthorized, `{"error":"no"}`, "daemon rejected the API token"},
		{"structured", http.StatusConflict, `{"error":"item busy"}`, "status 409: item busy"},
		{"plain", http.StatusInternalServerError, "server broke", "status 500: server broke"},
		{"bad json", http.StatusOK, "not json", "decode response from /api/queue"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := &HTTPAccess{client: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body))}, nil
			})}}
			if _, err := a.List(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("List error = %v, want %q", err, tt.want)
			}
		})
	}
	a := &HTTPAccess{client: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}}
	if _, err := a.List(); !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("disconnected List = %v", err)
	}
}
