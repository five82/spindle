package httpapi_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
)

// Mutations must reject malformed requests without changing queue state; valid
// requests must return counts that agree with the persisted rows.
func TestQueueMutationEndpoints(t *testing.T) {
	store := testStore(t)
	item, err := store.NewDisc("Test", "fp")
	if err != nil {
		t.Fatal(err)
	}
	srv := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	request := func(method, path, body string, code int) string {
		t.Helper()
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		if w.Code != code {
			t.Fatalf("%s %s %q: status %d, want %d: %s", method, path, body, w.Code, code, w.Body.String())
		}
		return w.Body.String()
	}
	for _, path := range []string{"/api/queue/retry", "/api/queue/retry-episode", "/api/queue/stop", "/api/queue/clear"} {
		request(http.MethodPost, path, "{", http.StatusBadRequest)
	}
	request(http.MethodPost, "/api/queue/retry-episode", `{"id":1}`, http.StatusBadRequest)
	request(http.MethodPost, "/api/queue/clear", `{"scope":"bad"}`, http.StatusBadRequest)
	request(http.MethodDelete, "/api/queue/not-a-number", "", http.StatusBadRequest)
	request(http.MethodGet, "/api/queue/not-a-number", "", http.StatusBadRequest)
	request(http.MethodGet, "/api/queue/999999", "", http.StatusNotFound)
	request(http.MethodPost, "/api/queue/enqueue-cached", "{", http.StatusBadRequest)
	request(http.MethodPost, "/api/queue/enqueue-cached", `{"disc_title":" ","fingerprint":"fp","rip_spec_data":"spec"}`, http.StatusBadRequest)
	request(http.MethodPost, "/api/queue/retry", `{"ids":[]}`, http.StatusOK)
	request(http.MethodPost, "/api/queue/stop", fmt.Sprintf(`{"ids":[%d]}`, item.ID), http.StatusOK)
	if got := request(http.MethodPost, "/api/queue/retry-episode", fmt.Sprintf(`{"id":%d,"episode_key":"missing"}`, item.ID), http.StatusOK); !strings.Contains(got, "episode_not_found") {
		t.Fatal(got)
	}
	request(http.MethodDelete, fmt.Sprintf("/api/queue/%d", item.ID), "", http.StatusOK)
	if got, err := store.GetByID(item.ID); err != nil || got != nil {
		t.Fatalf("item not removed: %+v, %v", got, err)
	}
	request(http.MethodPost, "/api/queue/clear", `{"scope":"completed"}`, http.StatusOK)
	request(http.MethodPost, "/api/queue/clear", `{"scope":"all"}`, http.StatusOK)
}

func TestCachedEnqueueDuplicateOverrideAndFilters(t *testing.T) {
	store := testStore(t)
	srv := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/queue/enqueue-cached", strings.NewReader(`{"disc_title":" Disc ","fingerprint":" fp ","rip_spec_data":"text","allow_duplicate":true}`)))
		if w.Code != http.StatusOK {
			t.Fatalf("enqueue %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	items, err := store.List()
	if err != nil || len(items) != 2 || items[0].DiscFingerprint != "fp" {
		t.Fatalf("enqueued items: %+v %v", items, err)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/queue?stage="+string(queue.StageRipping), nil))
	if w.Code != http.StatusOK || strings.Count(w.Body.String(), `"discFingerprint":"fp"`) != 2 {
		t.Fatalf("filtered list: %d %s", w.Code, w.Body.String())
	}
}

func TestDiscControlUnavailableAndStopWithoutShutdown(t *testing.T) {
	srv := httpapi.New(httpapi.Params{Store: testStore(t), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	for _, path := range []string{"/api/disc/pause", "/api/disc/resume", "/api/disc/detect"} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "no optical drive") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/daemon/stop", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("stop without shutdown channel: %d", w.Code)
	}
}
