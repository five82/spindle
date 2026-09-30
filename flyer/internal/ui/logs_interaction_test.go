package ui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/five82/spindle/flyer/internal/spindle"
)

func newLogInteractionModel(t *testing.T) Model {
	t.Helper()
	m, _ := updateApp(t, newAppTestModel(t), tea.WindowSizeMsg{Width: 90, Height: 12})
	m.currentView = ViewLogs
	m.logState.rawLines = []spindle.LogEvent{{Sequence: 1, Message: "first retry", Level: "warn"}, {Sequence: 2, Message: "second", Level: "info"}, {Sequence: 3, Message: "last retry", Level: "error"}}
	m.logState.contentVersion++
	m.updateLogViewport()
	return m
}

func TestLogSearchInputNavigationAndEscape(t *testing.T) {
	m := newLogInteractionModel(t)
	next := func(k string) {
		t.Helper()
		model, _ := m.handleLogsKey(appKey(k))
		switch v := model.(type) {
		case Model:
			m = v
		case *Model:
			m = *v
		default:
			t.Fatalf("unexpected model type %T", model)
		}
	}
	next("/")
	if !m.logState.searchActive {
		t.Fatal("search did not open")
	}
	m.logState.searchInput.SetValue("[")
	next("enter")
	if !m.logState.searchActive {
		t.Fatal("invalid regex should keep search open")
	}
	m.logState.searchInput.SetValue("retry")
	next("enter")
	if m.logState.searchActive || m.logState.searchQuery != "retry" || !equalIntSlices(m.logState.searchMatches, []int{0, 2}) || m.logState.follow {
		t.Fatalf("search state = %+v", m.logState)
	}
	if got := stripANSI(m.renderLogContent()); !strings.Contains(got, "first retry") || !strings.Contains(got, "last retry") {
		t.Fatalf("renderLogContent() = %q", got)
	}
	next("n")
	if m.logState.searchMatchIdx != 1 {
		t.Fatalf("next match index = %d", m.logState.searchMatchIdx)
	}
	next("n")
	if m.logState.searchMatchIdx != 0 {
		t.Fatalf("next match did not wrap: %d", m.logState.searchMatchIdx)
	}
	next("N")
	if m.logState.searchMatchIdx != 1 {
		t.Fatalf("previous match did not wrap: %d", m.logState.searchMatchIdx)
	}
	next("esc")
	if m.logState.searchRegex != nil || m.currentView != ViewLogs {
		t.Fatal("first escape should clear search, not leave logs")
	}
	next("esc")
	if m.currentView != ViewQueue {
		t.Fatal("second escape should return to queue")
	}
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLogSearchCancellationAndNoMatches(t *testing.T) {
	m := newLogInteractionModel(t)
	model, _ := m.handleLogsKey(appKey("/"))
	m = model.(Model)
	model, _ = m.handleLogsKey(appKey("enter"))
	m = *model.(*Model)
	if m.logState.searchActive || m.logState.searchRegex != nil {
		t.Fatal("empty search should close without applying")
	}
	model, _ = m.handleLogsKey(appKey("/"))
	m = model.(Model)
	m.logState.searchInput.SetValue("abandoned")
	model, _ = m.handleLogsKey(appKey("esc"))
	m = *model.(*Model)
	if m.logState.searchActive || m.logState.searchInput.Value() != "" {
		t.Fatal("escape should cancel input")
	}
	m.logState.searchRegex = regexp.MustCompile("missing")
	m.findSearchMatches()
	if len(m.logState.searchMatches) != 0 {
		t.Fatal("unexpected search matches")
	}
	m.nextSearchMatch()
	m.previousSearchMatch()
	if m.logState.searchMatchIdx != 0 {
		t.Fatal("no matches should not move cursor")
	}
}

func TestLogFiltersApplyAndCancel(t *testing.T) {
	m := newLogInteractionModel(t)
	model, _ := m.handleLogsKey(appKey("f"))
	m = model.(Model)
	if !m.showLogFilters || m.logFilterInputs[0].Value() != "info" {
		t.Fatal("filters did not open with default level")
	}
	m.logFilterInputs[0].SetValue(" error ")
	m.logFilterInputs[1].SetValue(" api ")
	m.logFilterInputs[2].SetValue(" rip ")
	m.logFilterInputs[3].SetValue(" abc ")
	model, _ = m.handleLogFiltersKey(appKey("enter"))
	m = model.(Model)
	if m.showLogFilters || m.logState.filterLevel != "error" || m.logState.filterComponent != "api" || m.logState.filterLane != "rip" || m.logState.filterRequest != "abc" || len(m.logState.rawLines) != 0 || m.logState.streamCursor != 0 {
		t.Fatalf("applied filters = %+v", m.logState)
	}
	model, _ = m.handleLogsKey(appKey("f"))
	m = model.(Model)
	model, _ = m.handleLogFiltersKey(appKey("esc"))
	m = model.(Model)
	if m.showLogFilters || m.logState.filterLevel != "error" {
		t.Fatal("cancel should preserve filters")
	}
	model, _ = m.handleLogsKey(appKey("f"))
	m = model.(Model)
	model, _ = m.handleLogFiltersKey(appKey("j"))
	m = model.(Model)
	if m.logFilterFocusIdx != 1 {
		t.Fatal("down should advance filter focus")
	}
	model, _ = m.handleLogFiltersKey(appKey("k"))
	m = model.(Model)
	if m.logFilterFocusIdx != 0 {
		t.Fatal("up should reverse filter focus")
	}
	model, _ = m.handleLogFiltersKey(appKey("ctrl+x"))
	m = model.(Model)
	for _, input := range m.logFilterInputs {
		if input.Value() != "" {
			t.Fatal("ctrl+x should clear filters")
		}
	}
	if _, cmd := m.handleLogFiltersKey(appKey("ctrl+c")); cmd == nil {
		t.Fatal("ctrl+c should quit from the filter modal like everywhere else")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ctrl+c should return tea.Quit")
	}
}

func TestLogViewNavigationAndStatus(t *testing.T) {
	m := newLogInteractionModel(t)
	if got := stripANSI(m.renderLogStatus(m.theme.Styles())); !strings.Contains(got, "Daemon log · 3 events · following") {
		t.Fatalf("status = %q", got)
	}
	for _, k := range []string{" ", "g", "G", "j", "k"} {
		model, _ := m.handleLogsKey(appKey(k))
		switch v := model.(type) {
		case Model:
			m = v
		case *Model:
			m = *v
		default:
			t.Fatalf("unexpected model type %T", model)
		}
	}
	if m.logState.follow {
		t.Fatal("scrolling should disable follow")
	}
	model, _ := m.handleLogsKey(appKey("G"))
	m = model.(Model)
	if !m.logState.follow {
		t.Fatal("bottom should enable follow")
	}
}

// Search scrolls to the matched event's rendered line, which differs from
// its index once structured fields expand events over several rows.
func TestLogSearchScrollsToExpandedEvent(t *testing.T) {
	m := newLogInteractionModel(t)
	m.logState.rawLines = nil
	for i := range 20 {
		m.logState.rawLines = append(m.logState.rawLines, spindle.LogEvent{Sequence: uint64(i + 1), Level: "info", Message: fmt.Sprintf("event-%02d", i),
			Fields: map[string]string{"a": "1", "b": "2", "c": "3"}})
	}
	model, _ := m.handleLogsKey(appKey("t"))
	m = model.(Model)
	if !m.logState.showFields {
		t.Fatal("t must expand fields")
	}
	m.logState.searchMatches, m.logState.searchMatchIdx = []int{15}, 0
	m.scrollToSearchMatch()
	if view := stripANSI(m.logViewport.View()); !strings.Contains(view, "event-15") {
		t.Fatalf("match not visible after scroll:\n%s", view)
	}
}

// Enter on a malformed pattern says why nothing happened; typing clears it.
func TestLogSearchReportsInvalidPattern(t *testing.T) {
	m := newLogInteractionModel(t)
	next := func(k string) {
		t.Helper()
		model, _ := m.handleLogsKey(appKey(k))
		switch v := model.(type) {
		case Model:
			m = v
		case *Model:
			m = *v
		}
	}
	for _, k := range []string{"/", "(", "enter"} {
		next(k)
	}
	if !m.logState.searchActive || !strings.Contains(stripANSI(m.renderLogStatus(m.theme.Styles())), "invalid pattern") {
		t.Fatalf("status = %q", stripANSI(m.renderLogStatus(m.theme.Styles())))
	}
	next(")")
	if strings.Contains(stripANSI(m.renderLogStatus(m.theme.Styles())), "invalid pattern") {
		t.Fatal("editing the pattern should clear the error")
	}
}

// The default INFO level is not a filter worth flagging in the title, but
// the status line still explains the window.
func TestLogTitleFlagsOnlyNonDefaultFilters(t *testing.T) {
	m := newLogInteractionModel(t)
	if got := m.getLogTitle(); got != "Daemon Log" {
		t.Fatalf("default title = %q", got)
	}
	if got := stripANSI(m.renderLogStatus(m.theme.Styles())); !strings.Contains(got, "level=info") {
		t.Fatalf("status = %q", got)
	}
	for _, level := range []string{"", "INFO"} {
		m.logState.filterLevel = level
		if got := m.getLogTitle(); got != "Daemon Log" {
			t.Fatalf("level %q title = %q", level, got)
		}
	}
	m.logState.filterLevel = "warn"
	if got := m.getLogTitle(); got != "Daemon Log (filtered)" {
		t.Fatalf("warn title = %q", got)
	}
	m.logState.filterLevel, m.logState.filterStage = "info", "encoding"
	if got := m.getLogTitle(); got != "Daemon Log (filtered)" {
		t.Fatalf("stage title = %q", got)
	}
}

// Log rows carry no buffer-position gutter, and the inspector drops the
// item ID every row would otherwise repeat.
func TestLogRowsOmitGutterAndInspectedItemID(t *testing.T) {
	m := newLogInteractionModel(t)
	m.logState.rawLines = []spindle.LogEvent{{Sequence: 1, ItemID: 7, Stage: "encoding", Message: "encoding progress", Level: "info"}}
	if got := stripANSI(m.renderLogContent()); strings.Contains(got, "│") || strings.Contains(got, "   1 ") || !strings.Contains(got, "ID #7 (encoding)") {
		t.Fatalf("daemon row = %q", got)
	}
	m.inspecting = true
	if got := stripANSI(m.renderLogContent()); strings.Contains(got, "ID #7") || !strings.Contains(got, "INFO encoding – encoding progress") {
		t.Fatalf("inspector row = %q", got)
	}
}

// The filter modal's key hint fits on one row.
func TestLogFiltersModalHintFits(t *testing.T) {
	m := newLogInteractionModel(t)
	m.openLogFilters()
	got := stripANSI(m.renderLogFilters())
	if !strings.Contains(got, "Enter apply · Esc cancel · Ctrl+X clear") {
		t.Fatalf("modal = %s", got)
	}
}
