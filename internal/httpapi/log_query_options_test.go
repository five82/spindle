package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/five82/spindle/internal/queue"
)

func TestLogsEndpointParsesQueryOptionsAndFiltersOldItemHistory(t *testing.T) {
	store, err := queue.Open(t.TempDir() + "/queue.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	item, err := store.NewDisc("Disc", "fp")
	if err != nil {
		t.Fatal(err)
	}
	buffer := NewLogBuffer(16)
	old := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	buffer.Append(LogEntry{Time: old, ItemID: item.ID, Stage: "ripping", Level: "WARN", Msg: "old generation"})
	buffer.Append(LogEntry{Time: now, ItemID: item.ID, Stage: "ripping", Level: "WARN", Msg: "current item"})
	buffer.Append(LogEntry{Time: now, ItemID: item.ID + 1, Stage: "ripping", Level: "WARN", Msg: "other item"})
	srv := New(Params{Store: store, LogBuffer: buffer, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/logs?item=1&stage=ripping&level=warn&limit=10&since=0&tail=true&daemon_only=0", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Events []LogEntry `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 1 || response.Events[0].Msg != "current item" {
		t.Fatalf("events: %+v", response.Events)
	}
	for _, attempt := range []string{"1", "2"} {
		buffer.Append(LogEntry{Time: now, ItemID: item.ID, Stage: "encoding", Level: "WARN", Msg: "scoped warning", Fields: map[string]string{"episode_key": "title03", "task_id": "99", "attempt": attempt}})
	}
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/logs?item=1&stage=encoding&asset=title03&task=99&attempt=2", nil))
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 1 || response.Events[0].Fields["attempt"] != "2" {
		t.Fatalf("scoped filters leaked another attempt: %+v", response.Events)
	}
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/logs?item=invalid&limit=bad&since=bad", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("invalid optional parameters: %d", w.Code)
	}
}
