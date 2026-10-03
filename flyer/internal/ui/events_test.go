package ui

import (
	"encoding/json"
	"fmt"
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
		{Type: "activity_ended", Stage: "encoding", Substage: "Chunking"},
		{Type: "stage_complete", Stage: "encoding"},
	}
	m.width = 100
	shown := strings.Join(strings.Fields(stripANSI(m.renderItemEvents())), " ")
	if strings.Contains(shown, "Encoding Started") || strings.Contains(stripANSI(m.renderItemEvents()), "\n\n") ||
		!strings.Contains(shown, "Ripping Started") || !strings.Contains(shown, "Encoding Chunking Encoding Completed") ||
		!strings.Contains(shown, "Ripping Completed") {
		t.Fatalf("events = %q", shown)
	}
}

// Snake-case activities read as sentences, a bare row restating the
// previous row folds away, and a run of one stage dims its repeats.
func TestItemEventsNormalizeAndGroupRows(t *testing.T) {
	m := New(Options{PrefsPath: t.TempDir() + "/prefs.toml"})
	m.width = 100
	m.itemEvents.loaded = true
	m.itemEvents.events = []spindle.ItemEvent{
		{Type: "activity_ended", Stage: "encoding", EpisodeKey: "s01_002", Substage: "Crop detection", DurationSeconds: 3.5},
		{Type: "activity_ended", Stage: "encoding", EpisodeKey: "s01_002", Substage: "crop_detection"},
		{Type: "activity_ended", Stage: "encoding", EpisodeKey: "s01_002", Substage: "validation"},
		{Type: "stage_complete", Stage: "analysis"},
	}
	lines := strings.Split(stripANSI(m.renderItemEvents()), "\n")
	want := []string{
		" Encoding     Crop detection (s01_002) 3.5s",
		" Encoding     Validation (s01_002)",
		" Analyzing    Completed",
	}
	if len(lines) != len(want) {
		t.Fatalf("rows = %q, want %q", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, lines[i], want[i])
		}
	}
	styles := m.theme.Styles()
	raw := strings.Split(m.renderItemEvents(), "\n")
	if !strings.Contains(raw[0], styles.AccentText.Render("Encoding    ")) || !strings.Contains(raw[1], styles.FaintText.Render("Encoding    ")) {
		t.Fatalf("stage run must lead in accent and dim repeats: %q", raw[:2])
	}
}

// A start/end pair for one operation reads as a single row with its
// duration; a still-open operation keeps its state word.
func TestItemEventsFoldActivityPairs(t *testing.T) {
	m := New(Options{PrefsPath: t.TempDir() + "/prefs.toml"})
	m.width = 120
	m.itemEvents.loaded = true
	m.itemEvents.events = []spindle.ItemEvent{
		{Type: "activity_running", Stage: "encoding", TaskID: 4, Attempt: 1, EpisodeKey: "s01_001", Substage: "Video probe", Message: "Video probe"},
		{Type: "activity_ended", Stage: "encoding", TaskID: 4, Attempt: 1, EpisodeKey: "s01_001", Substage: "Video probe", Message: "Video probe", DurationSeconds: 0.01},
		{Type: "activity_running", Stage: "encoding", TaskID: 4, Attempt: 1, EpisodeKey: "s01_001", Substage: "Crop detection", Message: "Crop detection"},
		{Type: "activity_ended", Stage: "encoding", TaskID: 4, Attempt: 1, EpisodeKey: "s01_001", Substage: "Crop detection", Message: "Crop detection ended", DurationSeconds: 3.2},
		{Type: "activity_running", Stage: "encoding", TaskID: 4, Attempt: 1, EpisodeKey: "s01_001", Substage: "Chunking", Message: "Detecting shot cuts"},
	}
	lines := strings.Split(stripANSI(m.renderItemEvents()), "\n")
	if len(lines) != 3 {
		t.Fatalf("pairs must fold to one row each: %q", lines)
	}
	for i, want := range []string{"Video probe (s01_001)", "Crop detection (s01_001) 3.2s", "Chunking running (s01_001) - Detecting shot cuts"} {
		if !strings.Contains(lines[i], "Encoding") || !strings.Contains(lines[i], want) {
			t.Errorf("row %d = %q, want %q", i, lines[i], want)
		}
	}
	if strings.Contains(lines[0], "0.0s") || strings.Contains(lines[0], "Video probe - Video probe") || strings.Contains(lines[1], "- Crop detection ended") || strings.Contains(lines[0], "task 4") {
		t.Fatalf("zero duration, repeated message, and first-run task tags must be dropped: %q", lines)
	}
	m.itemEvents.events[4].Attempt = 2
	if got := stripANSI(m.renderItemEvents()); !strings.Contains(got, "(run 2)") {
		t.Fatalf("retries must be marked: %q", got)
	}
}

// The Events tab opens on the newest events and keeps following them while
// the operator stays at the bottom.
func TestItemEventsTabFollowsNewest(t *testing.T) {
	item := spindle.QueueItem{ID: 42}
	m := New(Options{PrefsPath: t.TempDir() + "/prefs.toml"})
	m.width, m.height = 100, 12
	m.initInspectorViewport()
	m.snapshot = state.Snapshot{Queue: []spindle.QueueItem{item}}
	m.inspecting, m.inspectedID = true, 42
	model, _ := m.switchInspectorTab(tabEvents)
	m = model.(Model)
	var events []spindle.ItemEvent
	for i := range 30 {
		events = append(events, spindle.ItemEvent{ID: int64(i + 1), ItemID: 42, Type: "activity_ended", Stage: "encoding", Substage: fmt.Sprintf("step-%02d", i)})
	}
	m.handleItemEventBatch(itemEventBatchMsg{itemID: 42, batch: spindle.ItemEventBatch{Next: 30, Events: events}})
	if view := m.inspectorViewport.View(); !m.inspectorViewport.AtBottom() || !strings.Contains(view, "Step-29") {
		t.Fatalf("events tab must open on the newest line: %q", stripANSI(view))
	}
	m.inspectorViewport.GotoTop()
	m.handleItemEventBatch(itemEventBatchMsg{itemID: 42, batch: spindle.ItemEventBatch{Next: 31, Events: []spindle.ItemEvent{{ID: 31, ItemID: 42, Type: "activity_ended", Stage: "encoding", Substage: "step-30"}}}})
	if m.inspectorViewport.AtBottom() {
		t.Fatal("scrolling up must stop following")
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
				{ID: 2, ItemID: 42, Type: "activity_running", Stage: "encoding", EpisodeKey: "s01e01", Substage: "Chunking", Message: "Detecting shot cuts", Percent: 40},
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
	if shown := stripANSI(m.renderItemEvents()); !strings.Contains(shown, "Worker reserved (may wait for input)") {
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
