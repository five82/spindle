package ui

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/five82/flyer/internal/spindle"
)

func TestLogStatusSearchAndItemFilters(t *testing.T) {
	m := newLogInteractionModel(t)
	styles := m.theme.Styles()
	m.logState.mode = logSourceItem
	m.logState.lastItemID = 42
	m.logState.follow = false
	m.logState.searchActive = true
	m.logState.searchInput.SetValue("needle")
	m.logState.filterComponent, m.logState.filterLane, m.logState.filterRequest = "api", "rip", "request1"
	status := stripANSI(m.renderLogStatus(styles))
	for _, want := range []string{"Item log", "item=42", "search: needle", "comp=api", "lane=rip", "req=request1"} {
		if !strings.Contains(status, want) {
			t.Errorf("status %q missing %q", status, want)
		}
	}
	if got := m.getLogTitle(); got != "Daemon Log (filtered)" {
		t.Fatalf("filtered title = %q", got)
	}
	m.logState.lastItemID = 0
	if got := stripANSI(m.renderLogStatus(styles)); strings.Contains(got, "item=") {
		t.Fatalf("item without ID status = %q", got)
	}
	m.logState.searchRegex = regexp.MustCompile("retry")
	m.logState.searchQuery = "retry"
	m.logState.searchMatches = []int{0, 2}
	m.logState.searchMatchIdx = 1
	if got := stripANSI(m.renderLogStatus(styles)); !strings.Contains(got, "2/2") {
		t.Fatalf("matched search status = %q", got)
	}
	m.logState.searchMatches = nil
	if got := stripANSI(m.renderLogStatus(styles)); !strings.Contains(got, "Pattern not found") {
		t.Fatalf("unmatched search status = %q", got)
	}
	m.logState.searchRegex = nil
	m.logState.filterLevel, m.logState.filterComponent, m.logState.filterLane, m.logState.filterRequest = "", "", "", ""
	if got := m.getLogTitle(); got != "Daemon Log" {
		t.Fatalf("plain title = %q", got)
	}
}

func TestLogFilterInputAndFocusWrap(t *testing.T) {
	m := newLogInteractionModel(t)
	m.openLogFilters()
	model, _ := m.handleLogFiltersKey(appKey("x"))
	m = model.(Model)
	if got := m.logFilterInputs[0].Value(); got != "infox" {
		t.Fatalf("typed filter = %q", got)
	}
	model, _ = m.handleLogFiltersKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = model.(Model)
	if m.logFilterFocusIdx != 1 {
		t.Fatalf("tab focus = %d", m.logFilterFocusIdx)
	}
	model, _ = m.handleLogFiltersKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = model.(Model)
	if m.logFilterFocusIdx != 0 {
		t.Fatalf("shift-tab focus = %d", m.logFilterFocusIdx)
	}
}

func TestLogPageNavigationAndFollow(t *testing.T) {
	m := newLogInteractionModel(t)
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyPgDown}, {Code: tea.KeyPgUp}, {Code: 'd', Mod: tea.ModCtrl}, {Code: 'u', Mod: tea.ModCtrl}} {
		model, _ := m.handleLogsKey(key)
		m = model.(Model)
		if m.logState.follow {
			t.Fatalf("navigation key %s did not pause following", key.String())
		}
	}
	model, _ := m.handleLogsKey(appKey("G"))
	m = model.(Model)
	if !m.logState.follow {
		t.Fatal("bottom did not resume following")
	}
}

func TestLogNavigationAndBatchStaleness(t *testing.T) {
	m := newLogInteractionModel(t)
	for _, key := range []string{"f", "esc", "/", "esc", "j", "k", "g", "G", "n", "N", "x"} {
		model, _ := m.handleLogsKey(appKey(key))
		switch v := model.(type) {
		case Model:
			m = v
		case *Model:
			m = *v
		default:
			t.Fatalf("model = %T", model)
		}
	}
	m.logState.mode = logSourceItem
	m.logState.lastItemID = 42
	m.handleLogBatch(logBatchMsg{source: logSourceDaemon, next: 10, events: []spindle.LogEvent{{Sequence: 10}}})
	m.handleLogBatch(logBatchMsg{source: logSourceItem, itemID: 43, next: 10, events: []spindle.LogEvent{{Sequence: 10}}})
	if m.logState.itemCursor != 0 || len(m.logState.rawLines) != 3 {
		t.Fatal("stale batches changed item logs")
	}
	m.logState.rawLines = nil
	m.handleLogBatch(logBatchMsg{source: logSourceItem, itemID: 42, next: 12, events: []spindle.LogEvent{{Sequence: 11}, {Sequence: 11}, {Sequence: 12}}})
	if m.logState.itemCursor != 12 || len(m.logState.rawLines) != 2 {
		t.Fatalf("item batch: cursor=%d, lines=%d", m.logState.itemCursor, len(m.logState.rawLines))
	}
	m.handleLogBatch(logBatchMsg{source: logSourceItem, itemID: 42, next: 12, events: []spindle.LogEvent{{Sequence: 12}}})
	if len(m.logState.rawLines) != 2 {
		t.Fatal("duplicate appended")
	}
}
