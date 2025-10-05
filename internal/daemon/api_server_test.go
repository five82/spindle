package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"spindle/internal/api"
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
