package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/five82/spindle/flyer/internal/spindle"
	"github.com/five82/spindle/flyer/internal/state"
)

func TestQueueFilteringRenderingAndSelectionEdges(t *testing.T) {
	m := newAppTestModel(t)
	m.width, m.height = 120, 9
	m.snapshot.Queue = []spindle.QueueItem{
		{ID: 11, DisplayTitle: "Encoding", Stage: "encoding", DiscNumber: 2, UpdatedAt: time.Now().Add(-2 * time.Hour).Format(time.RFC3339), Tasks: []spindle.Task{{Type: "encoding", State: "running", Progress: spindle.TaskProgress{Percent: 42}}, {Type: "copy", State: "done"}}},
		{ID: 12, DisplayTitle: "Failed", Stage: "failed", Tasks: []spindle.Task{{Type: "ripping", State: "failed"}}},
		{ID: 13, DisplayTitle: "Review", NeedsReview: true},
		{ID: 14, DisplayTitle: "Done", Stage: "completed", Encoding: &spindle.EncodingStatus{EncodedSize: 100, SizeReductionPercent: 35}},
	}
	styles := m.theme.Styles()
	for _, item := range m.snapshot.Queue {
		for _, selected := range []bool{false, true} {
			row := stripANSI(m.renderQueueRow(item, computeQueueColumns(m.snapshot.Queue, m.width), selected, styles))
			if !strings.Contains(row, item.DisplayTitle) {
				t.Fatalf("row missing title: %q", row)
			}
		}
	}
	if got := runningTaskPercent(m.snapshot.Queue[0]); got != 42 {
		t.Fatalf("runningTaskPercent = %v", got)
	}
	if got := runningTaskPercent(m.snapshot.Queue[1]); got != 0 {
		t.Fatalf("failed percent = %v", got)
	}
	if got := m.queueProgressCell(m.snapshot.Queue[0], computeQueueColumns(m.snapshot.Queue, 60), styles.Text, styles, false); !strings.Contains(stripANSI(got), "42%") {
		t.Fatalf("compact progress: %q", got)
	}
	if got := m.queueProgressCell(m.snapshot.Queue[3], computeQueueColumns(m.snapshot.Queue, 120), styles.Text, styles, true); got != "-35%" {
		t.Fatalf("reduction: %q", got)
	}
	m.queueFilterActive = true
	if got := stripANSI(m.renderQueue()); !strings.Contains(got, "│ /t") {
		t.Fatalf("active filter missing: %q", got)
	}
	m.queueFilterActive = false
	m.queueFilterQuery = "nonsense"
	if got := stripANSI(m.renderQueue()); !strings.Contains(got, "No items match: nonsense") || !strings.Contains(got, "Queue (0/4)") {
		t.Fatalf("empty text filter: %q", got)
	}
	m.queueFilterQuery = ""
	m.filterMode = FilterProcessing
	if got := stripANSI(m.renderQueue()); !strings.Contains(got, "Active") {
		t.Fatalf("processing filter: %q", got)
	}
	m.filterMode = FilterReview
	if got := len(m.getSortedItems()); got != 1 {
		t.Fatalf("review count = %d", got)
	}
	m.filterMode = FilterFailed
	if got := len(m.getSortedItems()); got != 1 {
		t.Fatalf("failed count = %d", got)
	}
	m.filterMode = FilterAll
	m.queueFilterQuery = "#11"
	if got := m.getSortedItems(); len(got) != 1 || got[0].ID != 11 {
		t.Fatalf("ID match: %+v", got)
	}
	m.queueFilterQuery = ""
	m.selectedRow = 100
	m.updateQueueTable()
	if m.selectedRow != 3 {
		t.Fatalf("selection not clamped: %d", m.selectedRow)
	}
	m.snapshot.Queue = nil
	m.updateQueueTable()
	if m.selectedRow != 0 {
		t.Fatalf("empty selection: %d", m.selectedRow)
	}
	m.filterMode = FilterFailed
	if got := stripANSI(m.renderQueue()); !strings.Contains(got, "No items match filter: Failed") {
		t.Fatalf("empty mode: %q", got)
	}
	if got := scrollRangeFooter(1, 3, 5, 2); got != "2-3 of 5" {
		t.Fatalf("footer: %q", got)
	}
	if got := clampQueueScroll(5, 0, 2, 10); got != 0 {
		t.Fatalf("scroll back: %d", got)
	}
	if got := clampQueueScroll(0, 5, 2, 10); got != 4 {
		t.Fatalf("scroll forward: %d", got)
	}
}

func TestAppNavigationContextAndTickPaths(t *testing.T) {
	m := newAppTestModel(t)
	m, _ = updateApp(t, m, tea.WindowSizeMsg{Width: 100, Height: 22})
	for _, tc := range []struct {
		view       View
		inspecting bool
		tab        inspectorTab
		want       string
	}{
		{ViewQueue, false, tabOverview, "Queue"}, {ViewLogs, false, tabOverview, "Logs"},
		{ViewProblems, false, tabOverview, "Views"}, {ViewQueue, true, tabOverview, "Inspector"},
		{ViewQueue, true, tabLogs, "Logs"},
	} {
		m.currentView, m.inspecting, m.inspectorTab = tc.view, tc.inspecting, tc.tab
		if got := m.helpContext(); got != tc.want {
			t.Fatalf("context = %q, want %q", got, tc.want)
		}
	}
	m.inspecting = false
	m.currentView = ViewQueue
	for _, tc := range []struct {
		mode  QueueFilter
		label string
	}{{FilterAll, "All"}, {FilterFailed, "Failed"}, {FilterReview, "Review"}, {FilterProcessing, "Active"}} {
		m.filterMode = tc.mode
		if got := m.filterLabel(); got != tc.label {
			t.Fatalf("label = %q", got)
		}
	}
	m.filterMode = FilterAll
	m.queueFilterQuery = "absent"
	m, _ = updateApp(t, m, appKey("enter"))
	if m.inspecting {
		t.Fatal("empty filtered queue opened inspector")
	}
	m.queueFilterQuery = ""
	m, _ = updateApp(t, m, appKey("T"))
	if m.theme.Name != "Nightfox" {
		t.Fatalf("theme did not cycle: %s", m.theme.Name)
	}
	m, _ = updateApp(t, m, appKey("r"))
	m, _ = updateApp(t, m, appKey("l"))
	m, _ = updateApp(t, m, appKey("l")) // keep daemon source while reopening
	m, _ = updateApp(t, m, appKey("?"))
	if m.activeModal == nil {
		t.Fatal("help did not open")
	}
	m, _ = updateApp(t, m, appKey("x"))
	if m.activeModal != nil {
		t.Fatal("help modal should close on a key")
	}
	m.currentView = View(99)
	if got := m.renderContent(); got != "" {
		t.Fatalf("unexpected content: %q", got)
	}
	m.snapshot = state.Snapshot{HasStatus: true, Status: spindle.StatusResponse{Running: true}}
	m.currentView = ViewLogs
	m.logState.follow = true
	if _, cmd := updateApp(t, m, tickMsg(time.Now())); cmd == nil {
		t.Fatal("log tick not scheduled")
	}
	m.snapshot.ConsecutiveFailures = 1
	if _, cmd := updateApp(t, m, tickMsg(time.Now())); cmd == nil {
		t.Fatal("offline tick not scheduled")
	}
}

func TestNowBandHolderFiguresAndFallback(t *testing.T) {
	m := newAppTestModel(t)
	item := spindle.QueueItem{ID: 42, Stage: "encoding", Tasks: []spindle.Task{{Type: "encoding", State: "running", Progress: spindle.TaskProgress{Percent: 51}}}, Encoding: &spindle.EncodingStatus{FPS: 78, ETASeconds: 125}}
	m.snapshot.Queue = []spindle.QueueItem{item}
	h := spindle.ResourceHolder{ItemID: 42, Task: "encoding"}
	extras := strings.Join(m.holderExtras(h), " ")
	for _, want := range []string{"51%", "78 fps", "ETA 2m 5s"} {
		if !strings.Contains(extras, want) {
			t.Fatalf("extras = %q, missing %q", extras, want)
		}
	}
	if got := m.holderExtras(spindle.ResourceHolder{ItemID: 99, Task: "encoding"}); got != nil {
		t.Fatalf("unknown holder: %v", got)
	}
	if got := m.holderExtras(spindle.ResourceHolder{ItemID: 42, Task: "ripping"}); got != nil {
		t.Fatalf("wrong task: %v", got)
	}
	m.snapshot.Status.Scheduler = &spindle.SchedulerStatus{Resources: map[string]spindle.ResourceStatus{"encoder": {Used: 1, Holders: []spindle.ResourceHolder{h}}}}
	m.width = 120
	if got := stripANSI(m.nowBandContent(m.theme.BandStyles())); !strings.Contains(got, "51%") || !strings.Contains(got, "78 fps") {
		t.Fatalf("wide band: %q", got)
	}
	m.width = 70
	if got := stripANSI(m.nowBandContent(m.theme.BandStyles())); strings.Contains(got, "fps") {
		t.Fatalf("compact band: %q", got)
	}
	m.snapshot.Status.Scheduler = nil
	if got := stripANSI(m.nowBandContent(m.theme.BandStyles())); !strings.Contains(got, "Active: 1") {
		t.Fatalf("fallback: %q", got)
	}
}

func TestRelativeTimeAndFormattingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{{-time.Second, "just now"}, {59 * time.Second, "just now"}, {5 * time.Minute, "5m ago"}, {3 * time.Hour, "3h ago"}, {48 * time.Hour, "2d ago"}} {
		if got := humanizeDuration(tc.d); got != tc.want {
			t.Fatalf("humanize(%v) = %q", tc.d, got)
		}
	}
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{{0, ""}, {30 * time.Second, "0m"}, {75 * time.Minute, "1h 15m"}} {
		if got := humanizeDurationLong(tc.d); got != tc.want {
			t.Fatalf("long(%v) = %q", tc.d, got)
		}
	}
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{{0, ""}, {7 * time.Second, "7s"}, {65 * time.Second, "1m 5s"}, {time.Hour + time.Minute + time.Second, "1h 1m 1s"}} {
		if got := formatDuration(tc.d); got != tc.want {
			t.Fatalf("duration(%v) = %q", tc.d, got)
		}
	}
	for _, tc := range []struct {
		n    float64
		want float64
	}{{-1, 0}, {50, 50}, {110, 100}} {
		if got := clampPercent(tc.n); got != tc.want {
			t.Fatalf("clamp(%v) = %v", tc.n, got)
		}
	}
	if got := formatBytes(512); got != "0.00 MiB" {
		t.Fatalf("bytes: %q", got)
	}
	now := time.Now()
	if got := formatTimestamp(now, now); got != now.Format("15:04") {
		t.Fatalf("today: %q", got)
	}
	if got := formatTimestamp(now.AddDate(0, 0, -2), now); got == "" || got == now.Format("15:04") {
		t.Fatalf("older: %q", got)
	}
}
