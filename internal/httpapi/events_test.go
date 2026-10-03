package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/five82/spindle/internal/queue"
)

func TestItemEventsAPI(t *testing.T) {
	store, err := queue.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	item, err := store.NewDisc("disc", "fp")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"stage_start", "activity_running"} {
		if err := store.RecordEvent(queue.Event{ItemID: item.ID, Type: kind, Stage: queue.StageEncoding, Substage: "chunking"}); err != nil {
			t.Fatal(err)
		}
	}
	srv := New(Params{Store: store, Token: "secret"})
	fetch := func(path, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w
	}
	// A fixed first item ID keeps the URL readable while the cursor is tested.
	if item.ID != 1 {
		t.Fatalf("first item ID = %d", item.ID)
	}
	if got := fetch("/api/queue/1/events", "").Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", got)
	}
	if got := fetch("/api/queue/999/events", "secret").Code; got != http.StatusNotFound {
		t.Fatalf("missing item status = %d", got)
	}
	if got := fetch("/api/queue/1/events?since=bad", "secret").Code; got != http.StatusBadRequest {
		t.Fatalf("invalid cursor status = %d", got)
	}
	var page struct {
		Events []queue.Event `json:"events"`
		Next   int64         `json:"next"`
	}
	w := fetch("/api/queue/1/events", "secret")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Events) != 2 || page.Events[1].Substage != "chunking" {
		t.Fatalf("first page status=%d body=%s", w.Code, w.Body.String())
	}
	w = fetch("/api/queue/1/events?since=1", "secret")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Events) != 1 || page.Next != 2 {
		t.Fatalf("cursor page status=%d body=%s", w.Code, w.Body.String())
	}
}
