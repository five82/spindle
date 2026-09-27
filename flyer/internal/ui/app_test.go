package ui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/five82/flyer/internal/spindle"
	"github.com/five82/flyer/internal/state"
)

func newAppTestModel(t *testing.T) Model {
	t.Helper()
	m := New(Options{PrefsPath: filepath.Join(t.TempDir(), "prefs.toml")})
	m.snapshot = state.Snapshot{
		HasStatus: true,
		Status:    spindle.StatusResponse{Running: true},
		Queue: []spindle.QueueItem{
			{ID: 1, DisplayTitle: "Alpha", Stage: "failed", ErrorMessage: "bad disc"},
			{ID: 2, DisplayTitle: "Beta", Stage: "encoding"},
			{ID: 3, DisplayTitle: "Gamma", Stage: "completed"},
		},
	}
	return m
}

func appKey(s string) tea.KeyPressMsg {
	if s == "enter" {
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	if s == "esc" {
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func updateApp(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	return updated.(Model), cmd
}

func TestAppUpdateSnapshotAndSpinner(t *testing.T) {
	m := newAppTestModel(t)
	m.snapshot = state.Snapshot{}
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init must schedule ticks")
	}
	m, _ = updateApp(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	if !m.ready || m.logViewport.Width() == 0 || m.inspectorViewport.Width() == 0 {
		t.Fatalf("window size did not initialize viewports: ready=%v", m.ready)
	}
	m, cmd := updateApp(t, m, spinnerTickMsg{})
	if !m.spinnerOn || m.spinnerFrame != 1 || cmd == nil {
		t.Fatal("connecting spinner did not advance")
	}
	m, _ = updateApp(t, m, snapshotMsg{HasStatus: true})
	m, cmd = updateApp(t, m, spinnerTickMsg{})
	if m.spinnerOn || cmd != nil {
		t.Fatal("online snapshot must stop spinner")
	}
	m, cmd = updateApp(t, m, snapshotMsg{HasStatus: true, ConsecutiveFailures: 2})
	if !m.spinnerOn || cmd == nil {
		t.Fatal("offline snapshot must restart spinner")
	}
	m, _ = updateApp(t, m, logErrorMsg{err: errors.New("log")})
	if m.errorMsg != "Log fetch failed" || m.errorExpiry.IsZero() {
		t.Fatalf("log error not shown: %q", m.errorMsg)
	}
	m, _ = updateApp(t, m, problemsLogErrorMsg{err: errors.New("problems")})
	if m.errorMsg != "Problems fetch failed" {
		t.Fatalf("problems error not shown: %q", m.errorMsg)
	}
	m.errorExpiry = time.Now().Add(-time.Second)
	m, cmd = updateApp(t, m, tickMsg(time.Now()))
	if m.errorMsg != "" || !m.errorExpiry.IsZero() || cmd == nil {
		t.Fatal("tick must expire error and schedule another tick")
	}
}

func TestAppQueueKeysAndFilters(t *testing.T) {
	m := newAppTestModel(t)
	m, _ = updateApp(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	for _, tc := range []struct {
		key  string
		want int
	}{
		{"j", 1}, {"G", 2}, {"j", 2}, {"k", 1}, {"g", 0}, {"k", 0},
	} {
		m, _ = updateApp(t, m, appKey(tc.key))
		if m.selectedRow != tc.want {
			t.Fatalf("%s: selected row = %d, want %d", tc.key, m.selectedRow, tc.want)
		}
	}
	for _, want := range []QueueFilter{FilterFailed, FilterReview, FilterProcessing, FilterAll} {
		m, _ = updateApp(t, m, appKey("f"))
		if m.filterMode != want {
			t.Fatalf("filter = %v, want %v", m.filterMode, want)
		}
	}
	m, _ = updateApp(t, m, appKey("/"))
	if !m.queueFilterCapturing() {
		t.Fatal("/ must start queue text filtering")
	}
	for _, ch := range "Beta" {
		m, _ = updateApp(t, m, appKey(string(ch)))
	}
	if m.queueFilterQuery != "Beta" || len(m.getSortedItems()) != 1 {
		t.Fatalf("live filter = %q, items = %v", m.queueFilterQuery, m.getSortedItems())
	}
	m, _ = updateApp(t, m, appKey("enter"))
	if m.queueFilterCapturing() || m.queueFilterQuery != "Beta" {
		t.Fatal("enter must commit the filter")
	}
	m, _ = updateApp(t, m, appKey("esc"))
	if m.queueFilterQuery != "" || m.queueFilterActive {
		t.Fatal("esc must clear committed filter")
	}
	m, _ = updateApp(t, m, appKey("/"))
	m, _ = updateApp(t, m, appKey("q"))
	if m.queueFilterQuery != "q" {
		t.Fatal("q must be captured by the filter, not quit")
	}
	m, _ = updateApp(t, m, appKey("esc"))
	if m.queueFilterQuery != "" {
		t.Fatal("esc must cancel active filter")
	}
}

func TestAppGlobalViewsAndHelp(t *testing.T) {
	m := newAppTestModel(t)
	m, _ = updateApp(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = updateApp(t, m, appKey("h"))
	if m.activeModal == nil {
		t.Fatal("help key must open modal")
	}
	m, _ = updateApp(t, m, appKey("esc"))
	if m.activeModal != nil {
		t.Fatal("escape must close help modal")
	}
	m, _ = updateApp(t, m, appKey("p"))
	if m.currentView != ViewProblems {
		t.Fatal("p must open problems")
	}
	m, _ = updateApp(t, m, appKey("d"))
	if m.currentView != ViewQueue {
		t.Fatal("d must open queue")
	}
	m, _ = updateApp(t, m, appKey("l"))
	if m.currentView != ViewLogs || m.logState.mode != logSourceDaemon {
		t.Fatal("l must open daemon logs")
	}
	m, _ = updateApp(t, m, appKey("d"))
	m, _ = updateApp(t, m, appKey("enter"))
	if !m.inspecting || m.inspectedID != 1 {
		t.Fatalf("enter must inspect selected item: %+v", m)
	}
	m, _ = updateApp(t, m, appKey("esc"))
	if m.inspecting {
		t.Fatal("escape must close inspector")
	}
	_, cmd := updateApp(t, m, appKey("q"))
	if cmd == nil {
		t.Fatal("q must request quit")
	}
}

func TestAppViewsAndRefresh(t *testing.T) {
	m := newAppTestModel(t)
	if got := m.View(); !got.AltScreen || !strings.Contains(got.Content, "Starting flyer") {
		t.Fatalf("startup view = %q", got.Content)
	}
	m, _ = updateApp(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	for _, view := range []View{ViewQueue, ViewProblems, ViewLogs} {
		m.currentView = view
		if got := m.View(); !got.AltScreen || got.Content == "" {
			t.Fatalf("view %v rendered empty", view)
		}
	}
	m.currentView = ViewQueue
	m.activeModal = NewHelpModal(m.keys, "Queue")
	if got := stripANSI(m.View().Content); !strings.Contains(got, "Keyboard Shortcuts") {
		t.Fatal("help modal missing from view")
	}
	m.activeModal = nil
	m.showLogFilters = true
	if got := stripANSI(m.View().Content); !strings.Contains(got, "Log Filters") {
		t.Fatal("log filter modal missing from view")
	}
	m.showLogFilters = false
	store := &state.Store{}
	store.Update(&spindle.StatusResponse{PID: 17}, nil, nil)
	m.store = store
	calls := 0
	m.refreshFn = func() error { calls++; return nil }
	cmd := m.manualRefreshCmds()
	if cmd == nil {
		t.Fatal("manual refresh must return command")
	}
	if calls != 0 {
		t.Fatal("refresh must not run before the command executes")
	}
	got, ok := cmd().(snapshotMsg)
	if !ok || got.Status.PID != 17 || calls != 1 {
		t.Fatalf("manual refresh: snapshot = %v, calls = %d", got, calls)
	}
}
