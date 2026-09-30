package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/five82/spindle/flyer/internal/spindle"
)

const (
	problemsRefreshInterval = 2 * time.Second
	problemsFetchTimeout    = 5 * time.Second
	problemsFetchLimit      = 100
	problemsBufferLimit     = 500
)

type problemsState struct {
	logLines    []spindle.LogEvent
	logCursor   uint64
	lastItemID  int64
	lastRefresh time.Time
	fetchError  error
	loaded      bool
}

func (m *Model) getTriageItems() []spindle.QueueItem {
	var items []spindle.QueueItem
	for _, item := range m.snapshot.Queue {
		if needsAttention(item) {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		pi, pj := itemSortRank(items[i]), itemSortRank(items[j])
		if pi != pj {
			return pi < pj
		}
		return items[i].ID < items[j].ID
	})
	return items
}
func (m *Model) getTriageItem() *spindle.QueueItem {
	items := m.getTriageItems()
	if m.problemsRow < 0 || m.problemsRow >= len(items) {
		return nil
	}
	return &items[m.problemsRow]
}
func (m *Model) clampProblemsRow() {
	if count := len(m.getTriageItems()); m.problemsRow >= count {
		m.problemsRow = max(count-1, 0)
	}
}
func (m *Model) problemsVisibleRows() int { return max(m.height-4, 1) }
func (m Model) renderProblems() string {
	styles := m.theme.Styles()
	visibleRows := m.problemsVisibleRows()
	items := m.getTriageItems()
	var lines []string
	footer := ""
	if len(items) == 0 {
		lines = append(lines, styles.SuccessText.Render("✓ No problems"))
	} else {
		scroll := clampQueueScroll(m.problemsScroll, m.problemsRow, visibleRows, len(items))
		end := min(scroll+visibleRows, len(items))
		for i := scroll; i < end; i++ {
			lines = append(lines, m.renderTriageRow(items[i], i == m.problemsRow, styles))
		}
		footer = scrollRangeFooter(scroll, end, len(items), visibleRows)
	}
	for len(lines) < visibleRows {
		lines = append(lines, "")
	}
	return renderPanel(fmt.Sprintf("Problems (%d)", len(items)), strings.Join(lines, "\n"), footer, m.width, styles)
}
func (m Model) renderTriageRow(item spindle.QueueItem, selected bool, styles Styles) string {
	marker, style := "?", styles.WarningText
	if item.Stage == "failed" {
		marker, style = "!", styles.DangerText
	}
	inner := panelInnerWidth(m.width)
	id := fmt.Sprintf("#%d", item.ID)
	title := truncate(composeTitle(item), 40)
	reason := truncate(triageLeadReason(item), max(inner-(2+len(id)+1+lipgloss.Width(title)+2), 10))
	if selected {
		line := fmt.Sprintf("%s %s %s  %s", marker, id, title, reason)
		return styles.Selected.Render(line + strings.Repeat(" ", max(inner-lipgloss.Width(line), 0)))
	}
	return style.Render(marker) + " " + styles.MutedText.Render(id) + " " + styles.Text.Render(title) + "  " + styles.MutedText.Render(reason)
}
func triageLeadReason(item spindle.QueueItem) string {
	issues := itemProblems(item)
	if len(issues) > 0 {
		return issues[0]
	}
	return ""
}
func (m Model) handleProblemsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Inspect):
		return m.openInspector(tabProblems)
	case key.Matches(msg, m.keys.InspectLogs):
		return m.openInspector(tabLogs)
	case key.Matches(msg, m.keys.Escape):
		m.currentView = ViewQueue
		return m, nil
	}
	items := m.getTriageItems()
	if len(items) == 0 {
		return m, nil
	}
	switch {
	case key.Matches(msg, m.keys.Down):
		m.problemsRow = min(m.problemsRow+1, len(items)-1)
	case key.Matches(msg, m.keys.Up):
		m.problemsRow = max(m.problemsRow-1, 0)
	case key.Matches(msg, m.keys.Top):
		m.problemsRow = 0
	case key.Matches(msg, m.keys.Bottom):
		m.problemsRow = len(items) - 1
	}
	m.problemsScroll = clampQueueScroll(m.problemsScroll, m.problemsRow, m.problemsVisibleRows(), len(items))
	return m, nil
}

func (m *Model) renderItemProblems(item *spindle.QueueItem) string {
	styles := m.theme.Styles()
	var b strings.Builder
	state := m.problemsState
	current := state.lastItemID == item.ID
	issues := itemProblems(*item)
	switch {
	case len(issues) > 0:
		m.renderProblemSection(&b, "Current issues and nonfatal outcomes", styles.WarningText)
		for _, issue := range issues {
			for _, line := range wrapText(issue, max(panelInnerWidth(m.width)-2, 20)) {
				fmt.Fprintf(&b, "  %s\n", styles.Text.Render(line))
			}
		}
	case current && state.loaded && state.fetchError == nil && len(state.logLines) == 0:
		// Healthy on both counts: one line, not two negatives.
		fmt.Fprintln(&b, styles.SuccessText.Render("✓ No problems")+styles.FaintText.Render(" · no warnings or errors in recent item logs"))
		return b.String()
	default:
		fmt.Fprintln(&b, styles.MutedText.Render("No current issues"))
	}
	switch {
	case !current || !state.loaded && state.fetchError == nil:
		fmt.Fprintln(&b, styles.FaintText.Render("Checking recent item logs for warnings/errors"))
	case state.fetchError != nil:
		fmt.Fprintln(&b, styles.WarningText.Render("Diagnostics fetch failed: "+state.fetchError.Error()+"; retained history may be stale"))
	case len(state.logLines) == 0:
		fmt.Fprintln(&b, styles.FaintText.Render("No warnings or errors in recent item logs"))
	}
	if current && len(state.logLines) > 0 {
		fmt.Fprintf(&b, "\n%s\n", styles.MutedText.Bold(true).Render("Diagnostic history (recent warnings/errors; not a list of unresolved faults)"))
		for _, event := range state.logLines {
			scope := "Historical/unclassified"
			for _, task := range item.Tasks {
				if event.Fields["task_id"] == fmt.Sprint(task.ID) && event.Fields["attempt"] == fmt.Sprint(task.Attempts) {
					scope = "Current run diagnostic"
					if task.IsDone() {
						scope = "Completed run diagnostic"
					}
				}
			}
			fmt.Fprintf(&b, "%s\n%s\n", styles.FaintText.Render(scope), m.styleLogEvent(event, styles, true, true, panelInnerWidth(m.width)))
		}
	}
	return b.String()
}
func (m *Model) renderProblemSection(b *strings.Builder, title string, style lipgloss.Style) {
	fmt.Fprintln(b, style.Bold(true).Render(title))
}

func (m *Model) refreshProblemsLogs(item *spindle.QueueItem) tea.Cmd {
	if m.client == nil || item == nil {
		return nil
	}
	if m.snapshot.IsOffline() {
		m.problemsState.fetchError = fmt.Errorf("API offline; diagnostics fetch paused")
		return nil
	}
	if item.ID != m.problemsState.lastItemID {
		m.problemsState = problemsState{lastItemID: item.ID}
	}
	if time.Since(m.problemsState.lastRefresh) < problemsRefreshInterval {
		return nil
	}
	m.problemsState.lastRefresh = time.Now()
	itemID, cursor := item.ID, m.problemsState.logCursor
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), problemsFetchTimeout)
		defer cancel()
		query := spindle.LogQuery{Since: cursor, Limit: problemsFetchLimit, ItemID: itemID, Level: "warn", Tail: cursor == 0}
		batch, err := m.client.FetchLogs(ctx, query)
		if err != nil {
			return problemsLogErrorMsg{err: err, itemID: itemID}
		}
		return problemsLogBatchMsg{events: batch.Events, next: batch.Next, itemID: itemID}
	}
}

type problemsLogBatchMsg struct {
	events []spindle.LogEvent
	next   uint64
	itemID int64
}
type problemsLogErrorMsg struct {
	err    error
	itemID int64
}

func (m *Model) handleProblemsLogBatch(msg problemsLogBatchMsg) {
	if msg.itemID != m.problemsState.lastItemID {
		return
	}
	m.problemsState.loaded, m.problemsState.fetchError = true, nil
	m.problemsState.logCursor = msg.next
	var last uint64
	if n := len(m.problemsState.logLines); n > 0 {
		last = m.problemsState.logLines[n-1].Sequence
	}
	for _, event := range msg.events {
		if event.Sequence > last {
			m.problemsState.logLines = append(m.problemsState.logLines, event)
			last = event.Sequence
		}
	}
	m.problemsState.logLines = trimLogBuffer(m.problemsState.logLines, problemsBufferLimit)
	m.updateInspectorViewport()
}
