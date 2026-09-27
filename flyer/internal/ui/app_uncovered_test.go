package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/five82/spindle/flyer/internal/spindle"
	"github.com/five82/spindle/flyer/internal/state"
)

func TestAppTickRefreshesVisibleLogsAndProblems(t *testing.T) {
	m := newAppTestModel(t)
	m, _ = updateApp(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m.snapshot.HasStatus = true
	m.snapshot.Status.Running = true
	for _, tc := range []struct {
		name       string
		view       View
		inspecting bool
		tab        inspectorTab
		wantFetch  bool
	}{
		{"daemon logs", ViewLogs, false, tabOverview, true},
		{"inspector logs", ViewQueue, true, tabLogs, true},
		{"inspector problems", ViewQueue, true, tabProblems, true},
		{"missing item", ViewQueue, true, tabProblems, false},
		{"offline", ViewLogs, false, tabOverview, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			test := m
			test.currentView, test.inspecting, test.inspectorTab = tc.view, tc.inspecting, tc.tab
			test.inspectedID = 1
			if tc.name == "missing item" {
				test.inspectedID = 999
			}
			if tc.name == "offline" {
				test.snapshot.ConsecutiveFailures = 2
			}
			test.logState.follow = true
			test.logState.lastRefresh = time.Time{}
			// A nil client skips log fetches; use a real client without executing commands.
			client, err := spindle.NewClient("http://127.0.0.1:1")
			if err != nil {
				t.Fatal(err)
			}
			test.client = client
			_, cmd := updateApp(t, test, tickMsg(time.Now()))
			if !tc.wantFetch {
				if cmd == nil {
					t.Fatal("next tick not scheduled")
				}
				return
			}
			batch, ok := cmd().(tea.BatchMsg)
			if !ok {
				t.Fatalf("tick command returned %T, want batch", cmd)
			}
			if len(batch) != 2 {
				t.Fatalf("tick scheduled %d commands, want 2", len(batch))
			}
		})
	}
}

func TestAppLogSourceAndManualRefreshBranches(t *testing.T) {
	m := newAppTestModel(t)
	m, _ = updateApp(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m.logState.mode = logSourceItem
	m.logState.rawLines = []spindle.LogEvent{{Sequence: 5}}
	m.logState.streamCursor = 5
	m.logState.searchActive = true
	m.logState.searchQuery = "old"
	m.currentView = ViewLogs
	if m.logSearchCapturing() != true {
		t.Fatal("daemon search should capture")
	}
	m.inspecting, m.inspectorTab = true, tabLogs
	if !m.logSearchCapturing() {
		t.Fatal("inspector logs should capture search")
	}
	m.inspectorTab = tabOverview
	if m.logSearchCapturing() {
		t.Fatal("overview should not capture search")
	}
	m.inspecting = false
	m.logState.searchActive = false
	model, _ := m.openDaemonLogs()
	m = model.(Model)
	if m.logState.mode != logSourceDaemon || len(m.logState.rawLines) != 0 || m.logState.streamCursor != 0 || m.logState.searchQuery != "" {
		t.Fatalf("log source not reset: %+v", m.logState)
	}

	store := &state.Store{}
	store.Update(&spindle.StatusResponse{PID: 29}, nil, nil)
	m.store = store
	m, _ = updateApp(t, m, tea.WindowSizeMsg{Width: 90, Height: 20})
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init with store should schedule commands")
	}
	if got := fetchSnapshotCmd(store)().(snapshotMsg).Status.PID; got != 29 {
		t.Fatalf("snapshot PID = %d", got)
	}
	calls := 0
	m.refreshFn = func() error { calls++; return errors.New("offline") }
	m.currentView = ViewQueue
	if got := m.manualRefreshCmds()().(snapshotMsg).Status.PID; got != 29 || calls != 1 {
		t.Fatalf("refresh returned PID %d after %d calls", got, calls)
	}
	m.store = nil
	m.refreshFn = nil
	if got := m.manualRefreshCmds()(); got != nil {
		t.Fatalf("nil-store refresh = %T", got)
	}
}

func TestAppUpdateRoutingAndRenderingEdges(t *testing.T) {
	m := newAppTestModel(t)
	m, _ = updateApp(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m.currentView = ViewLogs
	m.logState.searchActive = true
	m.logState.searchInput.Focus()
	searched, _ := m.Update(appKey("q"))
	m = *searched.(*Model)
	if m.logState.searchInput.Value() != "q" {
		t.Fatal("search should capture global quit key")
	}
	m.logState.searchActive = false
	m, _ = updateApp(t, m, appKey("d"))
	m.queueFilterActive = true
	m.queueFilterInput.Focus()
	m, _ = updateApp(t, m, appKey("q"))
	if m.queueFilterQuery != "q" {
		t.Fatal("queue filter should capture quit key")
	}
	m.queueFilterActive = false
	m.currentView = View(99)
	if got := m.renderContent(); got != "" {
		t.Fatalf("unknown view = %q", got)
	}
	m, _ = updateApp(t, m, appKey("z"))
	m.inspecting = true
	m.inspectedID = 1
	if got := m.renderContent(); !strings.Contains(stripANSI(got), "Alpha") {
		t.Fatalf("inspector content missing item: %q", got)
	}
	m, _ = updateApp(t, m, problemsLogErrorMsg{err: errors.New("offline")})
	if m.errorMsg != "Problems fetch failed" {
		t.Fatalf("error = %q", m.errorMsg)
	}
	m, cmd := updateApp(t, m, struct{}{})
	if cmd != nil {
		t.Fatal("unknown message scheduled a command")
	}
	m.queueFilterActive = false
	m.inspecting = false
	m.currentView = ViewQueue
	m.snapshot.Queue = nil
	m, _ = updateApp(t, m, appKey("j"))
	if m.selectedRow != 0 {
		t.Fatalf("empty queue selection = %d", m.selectedRow)
	}
	store := &state.Store{}
	store.Update(&spindle.StatusResponse{PID: 51}, nil, nil)
	m.store = store
	_, cmd = updateApp(t, m, tickMsg(time.Now()))
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("store tick batch = %T", cmd())
	}
	if got := batch[0]().(snapshotMsg).Status.PID; got != 51 {
		t.Fatalf("tick snapshot PID = %d", got)
	}
}
