package httpapi_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
)

func testStore(t *testing.T) *queue.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := queue.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestHealthEndpoint(t *testing.T) {
	store := testStore(t)
	srv := httpapi.New(store, "", nil, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)), httpapi.StatusInfo{}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %q", body["status"])
	}
}

func TestAuthRejectsMissingToken(t *testing.T) {
	store := testStore(t)
	srv := httpapi.New(store, "secret-token", nil, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)), httpapi.StatusInfo{}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthAcceptsValidToken(t *testing.T) {
	store := testStore(t)
	srv := httpapi.New(store, "secret-token", nil, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)), httpapi.StatusInfo{}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestQueueListReturnsWrappedEmptyArray(t *testing.T) {
	store := testStore(t)
	srv := httpapi.New(store, "", nil, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)), httpapi.StatusInfo{}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Items) != 0 {
		t.Fatalf("expected empty items array, got %d items", len(body.Items))
	}
}

func TestQueueEnqueueCachedCreatesRippingItem(t *testing.T) {
	store := testStore(t)
	srv := httpapi.New(store, "", nil, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)), httpapi.StatusInfo{}, nil, nil)

	body := `{"disc_title":"Cached Disc","fingerprint":"fp1","rip_spec_data":"{\"version\":1}","metadata_json":"{\"title\":\"Cached Disc\"}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/queue/enqueue-cached", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Item struct {
			ID          int64  `json:"id"`
			Stage       string `json:"stage"`
			Fingerprint string `json:"discFingerprint"`
		} `json:"item"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Item.ID == 0 || resp.Item.Stage != string(queue.StageRipping) || resp.Item.Fingerprint != "fp1" {
		t.Fatalf("unexpected item response: %+v", resp.Item)
	}

	item, err := store.GetByID(resp.Item.ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if item == nil || item.Stage != queue.StageRipping || item.RipSpecData == "" || item.MetadataJSON == "" {
		t.Fatalf("cached item not persisted correctly: %+v", item)
	}
}

func TestQueueEnqueueCachedRejectsDuplicate(t *testing.T) {
	store := testStore(t)
	if _, err := store.NewDisc("Existing", "fp1"); err != nil {
		t.Fatalf("new disc: %v", err)
	}
	srv := httpapi.New(store, "", nil, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)), httpapi.StatusInfo{}, nil, nil)

	body := `{"disc_title":"Cached Disc","fingerprint":"fp1","rip_spec_data":"{\"version\":1}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/queue/enqueue-cached", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestStatusReturnsStructuredResponse(t *testing.T) {
	store := testStore(t)
	srv := httpapi.New(store, "", nil, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)), httpapi.StatusInfo{}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body struct {
		Running  bool `json:"running"`
		PID      int  `json:"pid"`
		Workflow struct {
			Running    bool           `json:"running"`
			QueueStats map[string]int `json:"queueStats"`
		} `json:"workflow"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.Running {
		t.Fatal("expected running=true")
	}
	if body.PID <= 0 {
		t.Fatalf("expected positive PID, got %d", body.PID)
	}
	if !body.Workflow.Running {
		t.Fatal("expected workflow.running=true")
	}
}
