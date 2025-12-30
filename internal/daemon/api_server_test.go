package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spindle/internal/api"
	"spindle/internal/logging"
	"spindle/internal/queue"
)

type queueStoreStub struct {
	items []*queue.Item
}

func (s *queueStoreStub) List(context.Context, ...queue.Status) ([]*queue.Item, error) {
	return s.items, nil
}

func (s *queueStoreStub) Stats(context.Context) (map[queue.Status]int, error) {
	return map[queue.Status]int{queue.StatusPending: len(s.items)}, nil
}

func (s *queueStoreStub) GetByID(context.Context, int64) (*queue.Item, error) {
	if len(s.items) == 0 {
		return nil, nil
	}
	return s.items[0], nil
}

func TestAPIServerHandleQueue(t *testing.T) {
	store := &queueStoreStub{items: []*queue.Item{{ID: 1, DiscTitle: "Example", Status: queue.StatusPending}}}
	srv := &apiServer{queueSvc: api.NewQueueService(store)}

	req := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	w := httptest.NewRecorder()
	srv.handleQueue(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
	var resp api.QueueListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	if resp.Items[0].DiscTitle != "Example" {
		t.Fatalf("unexpected disc title: %q", resp.Items[0].DiscTitle)
	}
}

func TestAPIServerHandleLogTail(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "background.log")
	if err := os.WriteFile(logPath, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	store := &queueStoreStub{items: []*queue.Item{{
		ID:          1,
		DiscTitle:   "Example",
		Status:      queue.StatusPending,
		ItemLogPath: logPath,
	}}}
	srv := &apiServer{queueSvc: api.NewQueueService(store)}

	req := httptest.NewRequest(http.MethodGet, "/api/logtail?item=1&offset=-1&limit=2", nil)
	w := httptest.NewRecorder()
	srv.handleLogTail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
	var resp api.LogTailResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(resp.Lines))
	}
	if resp.Lines[0] != "two" || resp.Lines[1] != "three" {
		t.Fatalf("unexpected lines: %#v", resp.Lines)
	}
	if resp.Offset <= 0 {
		t.Fatalf("expected positive offset, got %d", resp.Offset)
	}
}

func TestAPIServerHandleLogsDefaultsToForeground(t *testing.T) {
	hub := logging.NewStreamHub(16)
	hub.Publish(logging.LogEvent{Timestamp: time.Now().UTC(), Message: "bg", Lane: "background"})
	hub.Publish(logging.LogEvent{Timestamp: time.Now().UTC(), Message: "fg", Lane: "foreground"})
	hub.Publish(logging.LogEvent{Timestamp: time.Now().UTC(), Message: "daemon"})

	srv := &apiServer{daemon: &Daemon{logHub: hub}}

	req := httptest.NewRequest(http.MethodGet, "/api/logs?tail=1&limit=10", nil)
	w := httptest.NewRecorder()
	srv.handleLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
	var resp api.LogStreamResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(resp.Events))
	}
	if resp.Events[0].Lane != "foreground" || resp.Events[0].Message != "fg" {
		t.Fatalf("unexpected first event: %#v", resp.Events[0])
	}
	if resp.Events[1].Lane != "" || resp.Events[1].Message != "daemon" {
		t.Fatalf("unexpected second event: %#v", resp.Events[1])
	}

	req = httptest.NewRequest(http.MethodGet, "/api/logs?tail=1&limit=10&lane=background", nil)
	w = httptest.NewRecorder()
	srv.handleLogs(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
	resp = api.LogStreamResponse{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(resp.Events))
	}
	if resp.Events[0].Lane != "background" || resp.Events[0].Message != "bg" {
		t.Fatalf("unexpected background event: %#v", resp.Events[0])
	}
}
