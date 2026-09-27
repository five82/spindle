package httpapi_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/httpapi"
)

func TestQueueEndpointsWhenDatabaseUnavailable(t *testing.T) {
	store := testStore(t)
	item, err := store.NewDisc("Disc", "fp")
	if err != nil {
		t.Fatal(err)
	}
	srv := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	paths := []struct{ method, path, body string }{
		{"GET", "/api/queue", ""},
		{"GET", fmt.Sprintf("/api/queue/%d", item.ID), ""},
		{"GET", "/api/status", ""},
		{"POST", "/api/queue/retry", fmt.Sprintf(`{"ids":[%d]}`, item.ID)},
		{"POST", "/api/queue/retry-episode", fmt.Sprintf(`{"id":%d,"episode_key":"ep1"}`, item.ID)},
		{"POST", "/api/queue/stop", fmt.Sprintf(`{"ids":[%d]}`, item.ID)},
		{"POST", "/api/queue/enqueue-cached", `{"disc_title":"Disc","fingerprint":"fp","rip_spec_data":"{}"}`},
		{"DELETE", fmt.Sprintf("/api/queue/%d", item.ID), ""},
		{"POST", "/api/queue/clear", `{"scope":"all"}`},
		{"POST", "/api/queue/clear", `{"scope":"completed"}`},
	}
	for _, tc := range paths {
		t.Run(tc.method+" "+tc.path+tc.body, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestQueueEnqueueClosedDatabaseWithDuplicateOverride(t *testing.T) {
	store := testStore(t)
	srv := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("POST", "/api/queue/enqueue-cached", strings.NewReader(`{"disc_title":"Disc","fingerprint":"fp","rip_spec_data":"{\"version\":1}","allow_duplicate":true}`)))
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "failed to enqueue") {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
}
