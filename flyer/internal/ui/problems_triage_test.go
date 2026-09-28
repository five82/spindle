package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/flyer/internal/spindle"
)

func problemsTestModel(t *testing.T) Model {
	t.Helper()
	m := New(Options{ThemeName: "slate", PrefsPath: t.TempDir() + "/prefs.json"})
	m.width, m.height = 100, 8
	return m
}

func TestTriageSelectionAndNavigation(t *testing.T) {
	m := problemsTestModel(t)
	m.currentView = ViewProblems
	m.snapshot.Queue = []spindle.QueueItem{
		{ID: 8, Stage: "completed"},
		{ID: 4, Stage: "failed", DisplayTitle: "broken"},
		{ID: 3, Stage: "ripping", NeedsReview: true, ReviewReasons: []string{"check subtitles"}},
		{ID: 2, Stage: "failed", NeedsReview: true},
	}
	items := m.getTriageItems()
	if len(items) != 3 {
		t.Fatalf("triage items = %v", items)
	}
	for i, item := range items {
		m.problemsRow = i
		if got := m.getTriageItem(); got == nil || got.ID != item.ID {
			t.Fatalf("row %d = %v, want %d", i, got, item.ID)
		}
	}
	m.problemsRow = 99
	m.clampProblemsRow()
	if m.problemsRow != 2 {
		t.Fatalf("clamped row = %d", m.problemsRow)
	}
	for _, tc := range []struct {
		key  string
		want int
	}{{"k", 1}, {"g", 0}, {"j", 1}, {"G", 2}, {"j", 2}} {
		next, _ := m.handleProblemsKey(appKey(tc.key))
		m = next.(Model)
		if m.problemsRow != tc.want {
			t.Fatalf("key %q: row = %d, want %d", tc.key, m.problemsRow, tc.want)
		}
	}
	if got := stripANSI(m.renderProblems()); !strings.Contains(got, "Problems (3)") || !strings.Contains(got, "broken") {
		t.Fatalf("triage panel: %q", got)
	}
	next, _ := m.handleProblemsKey(appKey("enter"))
	opened := next.(Model)
	if !opened.inspecting || opened.inspectorTab != tabProblems || opened.inspectedID != items[2].ID {
		t.Fatalf("inspect selected row: %+v", opened)
	}
	next, _ = m.handleProblemsKey(appKey("esc"))
	if next.(Model).currentView != ViewQueue {
		t.Fatal("escape did not return to queue")
	}
	m.snapshot.Queue = nil
	m.clampProblemsRow()
	if m.problemsRow != 0 || m.getTriageItem() != nil {
		t.Fatal("empty triage must clear selection")
	}
	next, _ = m.handleProblemsKey(appKey("j"))
	if next.(Model).problemsRow != 0 || !strings.Contains(stripANSI(m.renderProblems()), "No current structured issues") {
		t.Fatal("empty triage must not navigate")
	}
}

func TestTriageLeadReasonPriority(t *testing.T) {
	tests := []struct {
		name string
		item spindle.QueueItem
		want string
	}{
		{"task", spindle.QueueItem{Tasks: []spindle.Task{{Type: "encoding", State: "failed", Error: "  disk full  "}}, NeedsReview: true}, "Encoding failed: disk full"},
		{"review", spindle.QueueItem{NeedsReview: true, ReviewReasons: []string{"first", "second"}}, "Review: first"},
		{"error", spindle.QueueItem{ErrorMessage: "  crashed  "}, "crashed"},
		{"stage", spindle.QueueItem{FailedAtStage: "ripping"}, "Ripping failed"},
		{"generic review", spindle.QueueItem{NeedsReview: true}, "Needs operator review"},
		{"healthy", spindle.QueueItem{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := triageLeadReason(tt.item); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestItemProblemsSectionsAndLogs(t *testing.T) {
	m := problemsTestModel(t)
	item := &spindle.QueueItem{
		ID: 7, NeedsReview: true, ReviewReasons: []string{"  missing subtitle ", "  "}, ErrorMessage: "bad disc",
		Episodes: []spindle.EpisodeStatus{{Key: "S01E01", Title: "Pilot", Status: "FAILED", ErrorMessage: "read error"}, {Key: "S01E02", Status: "completed"}},
		Encoding: &spindle.EncodingStatus{
			Error:   &spindle.EncodingIssue{Title: "Encode failed", Message: "no space", Context: "/output", Suggestion: "free disk"},
			Warning: "quality low", Validation: &spindle.EncodingValidation{Steps: []spindle.EncodingValidationStep{{Name: "video", Passed: true}, {Name: "audio", Details: "missing track"}}},
		},
	}
	m.problemsState.lastItemID = 7
	m.problemsState.logLines = []spindle.LogEvent{{Sequence: 1, Level: "warn", Message: "daemon warning"}}
	got := stripANSI(m.renderItemProblems(item))
	for _, want := range []string{"Current issues", "missing subtitle", "bad disc", "S01E01", "read error", "Encode failed", "no space", "Context:", "/output", "Suggestion:", "free disk", "Warning", "quality low", "Reel intermediate", "audio", "missing track", "Diagnostic history", "Historical/unclassified", "daemon warning"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from %q", want, got)
		}
	}
	if strings.Contains(got, "S01E02") {
		t.Fatalf("successful episode included: %q", got)
	}
	m.problemsState.lastItemID = 8
	m.problemsState.logLines = nil
	if got := stripANSI(m.renderItemProblems(&spindle.QueueItem{ID: 8})); !strings.Contains(got, "No current structured issues") || !strings.Contains(got, "Diagnostics loading") {
		t.Fatalf("empty problems = %q", got)
	}
	item.Encoding.Validation = &spindle.EncodingValidation{Passed: true, Steps: []spindle.EncodingValidationStep{{Name: "video", Passed: true}}}
	if got := stripANSI(m.renderItemProblems(item)); strings.Contains(got, "Reel intermediate: video") {
		t.Fatalf("passing validation omitted: %q", got)
	}
}

func TestProblemsLogRefreshAndBatch(t *testing.T) {
	requests := make(chan string, 3)
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.RawQuery
		if fail {
			http.Error(w, `{"error":"unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `{"events":[{"seq":9,"level":"warn","msg":"check"}],"next":9}`)
	}))
	defer server.Close()
	client, err := spindle.NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	m := problemsTestModel(t)
	item := &spindle.QueueItem{ID: 7}
	if m.refreshProblemsLogs(item) != nil {
		t.Fatal("nil client should not fetch")
	}
	m.client = client
	if m.refreshProblemsLogs(nil) != nil {
		t.Fatal("nil item should not fetch")
	}
	m.snapshot.ConsecutiveFailures = 2
	if m.refreshProblemsLogs(item) != nil {
		t.Fatal("offline should not fetch")
	}
	m.snapshot.ConsecutiveFailures = 0
	cmd := m.refreshProblemsLogs(item)
	if cmd == nil || m.problemsState.lastItemID != 7 {
		t.Fatal("first fetch should reset item and return command")
	}
	msg, ok := cmd().(problemsLogBatchMsg)
	if !ok || msg.itemID != 7 || msg.next != 9 || len(msg.events) != 1 {
		t.Fatalf("first fetch: %#v", msg)
	}
	if query := <-requests; !strings.Contains(query, "tail=1") || !strings.Contains(query, "item=7") || !strings.Contains(query, "level=warn") || !strings.Contains(query, "limit=100") {
		t.Fatalf("initial query: %q", query)
	}
	m.handleProblemsLogBatch(msg)
	if m.refreshProblemsLogs(item) != nil {
		t.Fatal("refresh should be throttled")
	}
	m.problemsState.lastRefresh = time.Now().Add(-problemsRefreshInterval)
	fail = true
	if _, ok := m.refreshProblemsLogs(item)().(problemsLogErrorMsg); !ok {
		t.Fatal("HTTP failure should return error message")
	}
	if query := <-requests; !strings.Contains(query, "since=9") || strings.Contains(query, "tail=1") {
		t.Fatalf("incremental query: %q", query)
	}
	m.problemsState.lastRefresh = time.Now().Add(-problemsRefreshInterval)
	m.problemsState.logLines = []spindle.LogEvent{{Sequence: 9}}
	if m.refreshProblemsLogs(&spindle.QueueItem{ID: 8}) == nil || m.problemsState.lastItemID != 8 || m.problemsState.logCursor != 0 || len(m.problemsState.logLines) != 0 {
		t.Fatal("switching items must reset logs and cursor")
	}
	m.handleProblemsLogBatch(problemsLogBatchMsg{itemID: 7, next: 99, events: []spindle.LogEvent{{Sequence: 99}}})
	if m.problemsState.logCursor != 0 {
		t.Fatal("stale batch changed cursor")
	}
}
