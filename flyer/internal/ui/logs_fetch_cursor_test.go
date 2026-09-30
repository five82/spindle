package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/five82/spindle/flyer/internal/spindle"
)

func TestLogFetchCommandsAndCursors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var queries []url.Values
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/logs" {
			t.Errorf("path = %q", r.URL.Path)
		}
		queries = append(queries, r.URL.Query())
		if fail {
			http.Error(w, `{"error":"unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(spindle.LogBatch{Events: []spindle.LogEvent{{Sequence: 11, Message: "hello"}}, Next: 11})
	}))
	defer server.Close()
	client, err := spindle.NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	m := New(Options{Client: client, PrefsPath: filepath.Join(home, "prefs.toml")})
	m.width, m.height = 100, 24
	m.initLogState()
	m.initLogViewport()
	m.logState.filterLevel = "warn"
	m.logState.filterStage = "ripping"
	cmd := m.refreshLogs(nil)
	if cmd == nil {
		t.Fatal("online log refresh must return command")
	}
	msg, ok := cmd().(logBatchMsg)
	if !ok || msg.source != logSourceDaemon || msg.next != 11 {
		t.Fatalf("daemon batch = %#v", msg)
	}
	if q := queries[0]; q.Get("tail") != "1" || q.Get("daemon_only") != "1" || q.Get("level") != "warn" || q.Get("stage") != "ripping" {
		t.Fatalf("daemon query = %v", q)
	}
	if cmd := m.refreshLogs(nil); cmd != nil {
		t.Fatal("refresh before interval must be throttled")
	}
	m.logState.lastRefresh = time.Time{}
	m.logState.streamCursor = 11
	_ = m.refreshLogs(nil)()
	if q := queries[1]; q.Get("since") != "11" || q.Has("tail") {
		t.Fatalf("incremental daemon query = %v", q)
	}

	item := spindle.QueueItem{ID: 42}
	m.logState.mode = logSourceItem
	m.logState.rawLines = []spindle.LogEvent{{Sequence: 3}}
	m.logState.itemCursor = 3
	m.logState.lastItemID = 8
	m.logState.lastRefresh = time.Time{}
	cmd = m.refreshLogs(&item)
	if m.logState.lastItemID != 42 || m.logState.itemCursor != 0 || len(m.logState.rawLines) != 0 {
		t.Fatal("switching items must reset cursor and buffer")
	}
	msg, ok = cmd().(logBatchMsg)
	if !ok || msg.source != logSourceItem || msg.itemID != 42 {
		t.Fatalf("item batch = %#v", msg)
	}
	if q := queries[2]; q.Get("item") != "42" || q.Get("tail") != "1" || q.Has("daemon_only") {
		t.Fatalf("initial item query = %v", q)
	}
	m.logState.itemCursor = 11
	m.logState.lastRefresh = time.Time{}
	_ = m.refreshLogs(&item)()
	if q := queries[3]; q.Get("since") != "11" || q.Has("tail") {
		t.Fatalf("incremental item query = %v", q)
	}
	if cmd := m.fetchItemLogs(nil); cmd != nil {
		t.Fatal("nil item must not fetch")
	}
	fail = true
	if _, ok := m.fetchItemLogs(&item)().(logErrorMsg); !ok {
		t.Fatal("item HTTP failure must return log error")
	}
	if _, ok := m.fetchDaemonLogs()().(logErrorMsg); !ok {
		t.Fatal("daemon HTTP failure must return log error")
	}
}

func TestRefreshLogsSkipsUnavailableAPI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := New(Options{PrefsPath: filepath.Join(home, "prefs.toml")})
	if cmd := m.refreshLogs(nil); cmd != nil {
		t.Fatal("nil client must skip logs")
	}
	client, err := spindle.NewClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	m.client = client
	m.snapshot.ConsecutiveFailures = 2
	if cmd := m.refreshLogs(nil); cmd != nil {
		t.Fatal("offline API must skip logs")
	}
}
