package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/five82/spindle/flyer/internal/spindle"
	"github.com/five82/spindle/flyer/internal/state"
)

func TestItemEventsRenderKeepsLifecycleOutcomes(t *testing.T) {
	m := New(Options{PrefsPath: t.TempDir() + "/prefs.toml"})
	m.itemEvents.loaded = true
	m.itemEvents.events = []spindle.ItemEvent{
		{Type: "stage_start", Stage: "ripping"},
		{Type: "stage_start", Stage: "encoding"},
		{Type: "stage_complete", Stage: "ripping"},
		{Type: "encoding_substage", Stage: "encoding", Substage: "Chunking"},
		{Type: "stage_complete", Stage: "encoding"},
	}
	shown := stripANSI(m.renderItemEvents())
	if strings.Contains(shown, "encoding started") || strings.Contains(shown, "\n\n") ||
		!strings.Contains(shown, "ripping started") || !strings.Contains(shown, "encoding Chunking") ||
		!strings.Contains(shown, "encoding completed") {
		t.Fatalf("events = %q", shown)
	}
}

func TestItemEventsTabPagesAndIgnoresStaleReplies(t *testing.T) {
	var cursors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/queue/42/events" {
			t.Errorf("events path = %q", r.URL.Path)
		}
		since := r.URL.Query().Get("since")
		cursors = append(cursors, since)
		batch := spindle.ItemEventBatch{Next: 2}
		if since == "0" {
			batch = spindle.ItemEventBatch{Next: 1, Events: []spindle.ItemEvent{
				{ID: 1, ItemID: 42, Type: "stage_start", Stage: "encoding", Time: "2026-01-01T00:00:00Z"},
			}}
		} else {
			batch.Events = []spindle.ItemEvent{
				{ID: 1, ItemID: 42, Type: "stage_start", Stage: "encoding"}, // overlapping poll
				{ID: 2, ItemID: 42, Type: "encoding_substage", Stage: "encoding", EpisodeKey: "s01e01", Substage: "Chunking", Message: "Detecting shot cuts", Percent: 40},
			}
		}
		_ = json.NewEncoder(w).Encode(batch)
	}))
	defer server.Close()
	client, err := spindle.NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	item := spindle.QueueItem{ID: 42}
	m := New(Options{Client: client, PrefsPath: t.TempDir() + "/prefs.toml"})
	m.width, m.height = 100, 24
	m.snapshot = state.Snapshot{Queue: []spindle.QueueItem{item}}
	m.inspecting, m.inspectedID, m.inspectorTab = true, 42, tabEvents
	first := m.fetchItemEvents(&item)
	if first == nil {
		t.Fatal("missing events fetch command")
	}
	msg, ok := first().(itemEventBatchMsg)
	if !ok {
		t.Fatalf("first response: %#v", msg)
	}
	m.handleItemEventBatch(msg)
	if m.itemEvents.cursor != 1 || len(m.itemEvents.events) != 1 {
		t.Fatalf("first page: %+v", m.itemEvents)
	}
	if shown := stripANSI(m.renderItemEvents()); !strings.Contains(shown, "worker reserved (may wait for input)") {
		t.Fatalf("idle encoding shown as work: %q", shown)
	}
	m.handleItemEventBatch(msg) // duplicate response must not append twice
	m.handleItemEventBatch(itemEventBatchMsg{itemID: 99, batch: spindle.ItemEventBatch{Next: 99}})
	second := m.fetchItemEvents(&item)
	m.handleItemEventBatch(second().(itemEventBatchMsg))
	if strings.Join(cursors, ",") != "0,1" || m.itemEvents.cursor != 2 || len(m.itemEvents.events) != 2 {
		t.Fatalf("pages %v, state %+v", cursors, m.itemEvents)
	}
	shown := stripANSI(m.renderItemEvents())
	if strings.Contains(shown, "encoding started") || strings.Contains(shown, "\n\n") {
		t.Fatalf("idle encoding shown as work: %q", shown)
	}
	if !strings.Contains(shown, "Chunking") || !strings.Contains(shown, "s01e01") || !strings.Contains(shown, "Detecting shot cuts") || !strings.Contains(shown, "40.0%") {
		t.Fatalf("missing substage context: %q", shown)
	}
	m.inspectorTab = tabLogs
	m.handleItemEventBatch(itemEventBatchMsg{itemID: 42, batch: spindle.ItemEventBatch{Next: 3, Events: []spindle.ItemEvent{{ID: 3}}}})
	if m.itemEvents.cursor != 2 {
		t.Fatal("background response modified inactive tab")
	}
	other := spindle.QueueItem{ID: 43}
	if cmd := m.fetchItemEvents(&other); cmd == nil || m.itemEvents.itemID != 43 || m.itemEvents.cursor != 0 || len(m.itemEvents.events) != 0 {
		t.Fatalf("item switch: %+v", m.itemEvents)
	}
	if cmd := m.fetchItemEvents(nil); cmd != nil {
		t.Fatal("nil item should not fetch")
	}
}
